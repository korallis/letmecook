import { open, mkdir, readFile, rename, rm } from 'node:fs/promises';
import { join } from 'node:path';
import { randomUUID } from 'node:crypto';
import { canonical, keys, object, parseJSON } from './json.ts';
import { Denial, fingerprint, ref, validatePolicy, type Binding, type Outcome, type Policy } from './types.ts';

// Implementations are trusted configuration authorities outside the worker HTTP surface.
// The experiment accepts only the synthetic authority; this is not a live 9Router writer fence.
export interface FrozenAuthority {
  readFrozenPolicy(): Promise<Policy>;
  quiescent(requestIds: string[]): Promise<boolean>;
  replaceFrozenPolicy(next: Policy): Promise<Policy>;
}
export interface Reservation {
  requestId: string; attemptId: string; taskId: string; epoch: number;
  outcome: Outcome | 'active';
}
interface Journal {
  schema: 1; generation: string; phase: 'closed' | 'active' | 'draining' | 'mutating';
  policy: Policy | null; fingerprint: string | null;
  reservations: Reservation[]; counts: Record<string, number>;
}
function validateJournal(value: unknown): asserts value is Journal {
  keys(value, ['schema', 'generation', 'phase', 'policy', 'fingerprint', 'reservations', 'counts'], ['schema', 'generation', 'phase', 'policy', 'fingerprint', 'reservations', 'counts']); object(value);
  if (value.schema !== 1 || typeof value.generation !== 'string' || !['closed', 'active', 'draining', 'mutating'].includes(value.phase) || !Array.isArray(value.reservations) || value.reservations.length > 16) throw new Error();
  if (value.policy !== null && fingerprint(validatePolicy(value.policy)) !== value.fingerprint || value.policy === null && value.fingerprint !== null) throw new Error();
  const ids = new Set<string>();
  for (const r of value.reservations) {
    keys(r, ['requestId', 'attemptId', 'taskId', 'epoch', 'outcome'], ['requestId', 'attemptId', 'taskId', 'epoch', 'outcome']);
    if (!/^[a-f0-9]{32}$/.test(r.requestId) || ids.has(r.requestId) || !ref(r.attemptId) || !ref(r.taskId) || !Number.isSafeInteger(r.epoch) || r.epoch < 1 || !['active', 'completed', 'failed_before_output', 'partial_failure', 'cancelled_unknown'].includes(r.outcome)) throw new Error();
    ids.add(r.requestId);
  }
  object(value.counts);
  if (Object.keys(value.counts).length > 64 || Object.entries(value.counts).some(([key, count]) => !ref(key) || !Number.isSafeInteger(count) || (count as number) < 1 || (count as number) > 32)) throw new Error();
}
export class PolicyGate {
  private tail: Promise<unknown> = Promise.resolve();
  private state: Journal;
  private failed = false;
  private readonly directory: string;
  private readonly authority: FrozenAuthority;
  private constructor(directory: string, authority: FrozenAuthority, state: Journal) { this.directory = directory; this.authority = authority; this.state = state; }
  static async open(directory: string, authority: FrozenAuthority) {
    await mkdir(directory, { recursive: true, mode: 0o700 });
    // A stale lock intentionally needs offline operator recovery after verified supervisor death.
    const lock = await open(join(directory, 'owner.lock'), 'wx', 0o600);
    await lock.writeFile(JSON.stringify({ pid: process.pid })); await lock.sync(); await lock.close();
    let previous: Journal | undefined;
    try { const decoded = parseJSON(await readFile(join(directory, 'state.json'), 'utf8')); validateJournal(decoded); previous = decoded; }
    catch (error: any) { if (error.code !== 'ENOENT') { await rm(join(directory, 'owner.lock')); throw new Denial('boundary_closed', 503); } }
    const state: Journal = previous ?? { schema: 1, generation: '', phase: 'closed', policy: null, fingerprint: null, reservations: [], counts: {} };
    state.generation = randomUUID(); state.phase = 'closed';
    const gate = new PolicyGate(directory, authority, state);
    await gate.persist(); return gate;
  }
  private async serial<T>(action: () => Promise<T>): Promise<T> {
    const result = this.tail.then(action);
    this.tail = result.catch(() => {}); return result;
  }
  private async persist() {
    const temporary = join(this.directory, 'state.next');
    try {
      const file = await open(temporary, 'w', 0o600);
      try { await file.writeFile(JSON.stringify(this.state)); await file.sync(); } finally { await file.close(); }
      await rename(temporary, join(this.directory, 'state.json'));
      const directory = await open(this.directory, 'r');
      try { await directory.sync(); } finally { await directory.close(); }
    } catch { this.failed = true; this.state.phase = 'closed'; throw new Denial('boundary_closed', 503); }
  }
  snapshot(): Journal { return structuredClone(this.state); }
  async activate() {
    return this.serial(async () => {
      if (this.failed || this.state.phase !== 'closed') throw new Denial('boundary_closed', 503);
      const policy = validatePolicy(await this.authority.readFrozenPolicy());
      if (!await this.authority.quiescent(this.state.reservations.map(r => r.requestId))) throw new Denial('boundary_closed', 503);
      this.state.reservations = [];
      this.state.policy = policy; this.state.fingerprint = fingerprint(policy); this.state.phase = 'active';
      await this.persist(); return structuredClone(policy);
    });
  }
  async admit(binding: Binding, current: () => boolean) {
    return this.serial(async () => {
      const policy = this.state.policy;
      if (this.failed || this.state.phase !== 'active' || !policy) throw new Denial('boundary_closed', 503);
      const retired = this.state.reservations.filter(r => r.outcome !== 'active');
      if (retired.length && await this.authority.quiescent(retired.map(r => r.requestId))) this.state.reservations = this.state.reservations.filter(r => r.outcome === 'active');
      if (!current()) throw new Denial('unauthorized', 401);
      if (binding.routerId !== policy.routerId || binding.routeId !== policy.routeId || binding.epoch !== policy.epoch || binding.revision !== policy.revision) throw new Denial('policy_denied');
      if (this.state.reservations.filter(r => r.attemptId === binding.attemptId).length >= policy.limits.concurrency || this.state.reservations.length >= 16) throw new Denial('concurrency_limit', 429);
      if ((this.state.counts[binding.attemptId] ?? 0) >= policy.limits.requestCount || Object.keys(this.state.counts).length >= 64 && !Object.hasOwn(this.state.counts, binding.attemptId)) throw new Denial('attempt_limit', 429);
      const reservation: Reservation = { requestId: randomUUID().replaceAll('-', ''), attemptId: binding.attemptId, taskId: binding.taskId, epoch: policy.epoch, outcome: 'active' };
      this.state.reservations.push(reservation);
      this.state.counts[binding.attemptId] = (this.state.counts[binding.attemptId] ?? 0) + 1;
      await this.persist();
      return { reservation: structuredClone(reservation), policy: structuredClone(policy) };
    });
  }
  async finish(requestId: string, outcome: Outcome, neverForwarded = false) {
    return this.serial(async () => {
      const record = this.state.reservations.find(r => r.requestId === requestId);
      if (!record) throw new Denial('boundary_closed', 503);
      record.outcome = outcome;
      // Local cancellation/EOF cannot establish upstream quiescence.
      let quiescent = neverForwarded;
      try { quiescent ||= await this.authority.quiescent([requestId]); }
      catch { this.state.phase = 'closed'; await this.persist(); throw new Denial('boundary_closed', 503); }
      if (quiescent) this.state.reservations = this.state.reservations.filter(r => r !== record);
      await this.persist(); return quiescent;
    });
  }
  async drainAndReplace(next: Policy): Promise<boolean> {
    return this.serial(async () => {
      if (this.failed || !['active', 'draining'].includes(this.state.phase)) throw new Denial('boundary_closed', 503);
      const previous = this.state.policy!;
      const reviewed = validatePolicy(next);
      if (reviewed.epoch !== previous.epoch + 1 || reviewed.revision === previous.revision || reviewed.routerId !== previous.routerId || reviewed.routeId !== previous.routeId) throw new Denial('policy_denied');
      this.state.phase = 'draining'; await this.persist();
      // An active local stream must finish even if an authority knows upstream work has stopped.
      if (this.state.reservations.some(r => r.outcome === 'active')) return false;
      if (!await this.authority.quiescent(this.state.reservations.map(r => r.requestId))) return false;
      this.state.reservations = []; this.state.phase = 'mutating'; await this.persist();
      try {
        const actual = validatePolicy(await this.authority.replaceFrozenPolicy(reviewed));
        if (canonical(actual) !== canonical(reviewed)) throw new Error();
        this.state.policy = actual; this.state.fingerprint = fingerprint(actual); this.state.phase = 'active';
        await this.persist(); return true;
      } catch { this.state.phase = 'closed'; await this.persist(); throw new Denial('boundary_closed', 503); }
    });
  }
  async close() {
    await this.serial(async () => { this.state.phase = 'closed'; await this.persist(); });
    await rm(join(this.directory, 'owner.lock'));
  }
}
