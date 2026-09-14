import { open, mkdir, readFile, rename, rm } from 'node:fs/promises';
import { join } from 'node:path';
import { randomUUID } from 'node:crypto';
import { canonical, keys, object, parseJSON } from './json.ts';
import { Denial, fingerprint, ref, validateBinding, validatePolicy, type Binding, type Outcome, type Policy } from './types.ts';
import { abortable } from './wait.ts';

// Implementations are trusted configuration authorities outside the worker HTTP surface.
// The experiment accepts only the synthetic authority; this is not a live 9Router writer fence.
export interface FrozenAuthority {
  readFrozenPolicy(signal: AbortSignal): Promise<Policy>;
  quiescent(requestIds: string[], signal: AbortSignal): Promise<boolean>;
  replaceFrozenPolicy(next: Policy, signal: AbortSignal): Promise<Policy>;
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
  const reserved = new Map<string, number>();
  for (const r of value.reservations) {
    reserved.set(r.attemptId, (reserved.get(r.attemptId) ?? 0) + 1);
    if (!Object.hasOwn(value.counts, r.attemptId) || value.counts[r.attemptId] < reserved.get(r.attemptId)! || value.policy === null || r.epoch !== value.policy.epoch) throw new Error();
  }
}
export class PolicyGate {
  private tail: Promise<unknown> = Promise.resolve();
  private state: Journal;
  private failed = false;
  private lifecycle: 'open' | 'closing' | 'closed' = 'open';
  private closing: Promise<void> | undefined;
  private stop = new AbortController();
  private uncertainMutation = false;
  private readonly directory: string;
  private readonly authority: FrozenAuthority;
  private readonly owner: string;
  private readonly authorityMs: number;
  private constructor(directory: string, authority: FrozenAuthority, state: Journal, owner: string, authorityMs: number) { this.directory = directory; this.authority = authority; this.state = state; this.owner = owner; this.authorityMs = authorityMs; }
  static async open(directory: string, authority: FrozenAuthority, authorityMs = 250) {
    if (!Number.isInteger(authorityMs) || authorityMs < 1 || authorityMs > 1000) throw new Denial('policy_denied');
    await mkdir(directory, { recursive: true, mode: 0o700 });
    // A stale lock intentionally needs offline operator recovery after verified supervisor death.
    const lock = await open(join(directory, 'owner.lock'), 'wx', 0o600);
    const owner = randomUUID();
    await lock.writeFile(JSON.stringify({ pid: process.pid, owner })); await lock.sync(); await lock.close();
    const ownsLock = async () => JSON.parse(await readFile(join(directory, 'owner.lock'), 'utf8')).owner === owner;
    let previous: Journal | undefined;
    // Object-form counters may contain valid IDs such as "constructor". The parser
    // constructs null-prototype objects; request JSON retains the stricter default.
    try { const decoded = parseJSON(await readFile(join(directory, 'state.json'), 'utf8'), true); validateJournal(decoded); previous = decoded; }
    catch (error: any) { if (error.code !== 'ENOENT') { if (await ownsLock()) await rm(join(directory, 'owner.lock')); throw new Denial('boundary_closed', 503); } }
    const state: Journal = previous ?? { schema: 1, generation: '', phase: 'closed', policy: null, fingerprint: null, reservations: [], counts: Object.create(null) };
    state.generation = randomUUID(); state.phase = 'closed';
    const gate = new PolicyGate(directory, authority, state, owner, authorityMs);
    await gate.persist(); return gate;
  }
  private check(signal?: AbortSignal) {
    if (signal?.aborted) throw signal.reason;
    if (this.lifecycle !== 'open' || this.failed || this.uncertainMutation) throw new Denial('boundary_closed', 503);
  }
  private async requireOwner() {
    try {
      if (JSON.parse(await readFile(join(this.directory, 'owner.lock'), 'utf8')).owner !== this.owner) throw new Error();
    } catch { this.failed = true; this.state.phase = 'closed'; throw new Denial('boundary_closed', 503); }
  }
  private async serial<T>(action: (signal: AbortSignal) => Promise<T>, caller?: AbortSignal): Promise<T> {
    this.check(caller);
    const signal = AbortSignal.any([this.stop.signal, ...(caller ? [caller] : [])]);
    const result = this.tail.then(async () => {
      this.check(signal); await this.requireOwner(); this.check(signal);
      try { return await action(signal); }
      catch (error) {
        if (this.lifecycle === 'open' && !this.failed && this.state.phase === 'closed') await this.persist();
        throw error;
      }
    });
    this.tail = result.catch(() => {}); return result;
  }
  private async authorityCall<T>(invoke: (signal: AbortSignal) => Promise<T>, caller: AbortSignal, mutating = false): Promise<T> {
    this.check(caller);
    const timeout = new AbortController();
    const timer = setTimeout(() => timeout.abort(new Denial('deadline', 408)), this.authorityMs);
    const signal = AbortSignal.any([caller, this.stop.signal, timeout.signal]);
    let started = false; let settled = false;
    const work = Promise.resolve().then(() => {
      this.check(signal); started = true; return invoke(signal);
    }).then(value => { settled = true; return value; }, error => { settled = true; throw error; });
    try {
      const value = await abortable(work, signal);
      this.check(signal); await this.requireOwner(); this.check(signal);
      return value;
    } catch (error) {
      if (mutating && started && !settled) this.uncertainMutation = true;
      this.state.phase = 'closed';
      throw error instanceof Denial ? error : new Denial('boundary_closed', 503);
    } finally { clearTimeout(timer); }
  }
  private async persist(closing = false) {
    const temporary = join(this.directory, `state.${this.owner}.next`);
    try {
      this.checkWritable(closing);
      await this.requireOwner();
      const file = await open(temporary, 'w', 0o600);
      try { await file.writeFile(JSON.stringify(this.state)); await file.sync(); } finally { await file.close(); }
      await this.requireOwner();
      this.checkWritable(closing);
      await rename(temporary, join(this.directory, 'state.json'));
      const directory = await open(this.directory, 'r');
      try { await directory.sync(); } finally { await directory.close(); }
    } catch { this.failed = true; this.state.phase = 'closed'; throw new Denial('boundary_closed', 503); }
  }
  private checkWritable(closing: boolean) {
    if (this.lifecycle === 'closed' || this.lifecycle === 'closing' && !closing) throw new Denial('boundary_closed', 503);
  }
  snapshot(): Journal { return structuredClone(this.state); }
  async activate() {
    return this.serial(async signal => {
      if (this.failed || this.state.phase !== 'closed') throw new Denial('boundary_closed', 503);
      const policy = validatePolicy(await this.authorityCall(s => this.authority.readFrozenPolicy(s), signal));
      if (!await this.authorityCall(s => this.authority.quiescent(this.state.reservations.map(r => r.requestId), s), signal)) throw new Denial('boundary_closed', 503);
      this.state.reservations = [];
      this.state.policy = policy; this.state.fingerprint = fingerprint(policy); this.state.phase = 'active';
      await this.persist(); return structuredClone(policy);
    });
  }
  async admit(binding: Binding, current: () => boolean, caller?: AbortSignal) {
    validateBinding(binding);
    return this.serial(async signal => {
      const policy = this.state.policy;
      if (this.failed || this.state.phase !== 'active' || !policy) throw new Denial('boundary_closed', 503);
      const retired = this.state.reservations.filter(r => r.outcome !== 'active');
      if (retired.length && await this.authorityCall(s => this.authority.quiescent(retired.map(r => r.requestId), s), signal)) this.state.reservations = this.state.reservations.filter(r => r.outcome === 'active');
      this.check(signal);
      if (!current()) throw new Denial('unauthorized', 401);
      if (binding.routerId !== policy.routerId || binding.routeId !== policy.routeId || binding.epoch !== policy.epoch || binding.revision !== policy.revision) throw new Denial('policy_denied');
      if (this.state.reservations.filter(r => r.attemptId === binding.attemptId).length >= policy.limits.concurrency || this.state.reservations.length >= 16) throw new Denial('concurrency_limit', 429);
      const count = Object.hasOwn(this.state.counts, binding.attemptId) ? this.state.counts[binding.attemptId] : 0;
      if (!Number.isSafeInteger(count) || count < 0) throw new Denial('boundary_closed', 503);
      if (count >= policy.limits.requestCount || Object.keys(this.state.counts).length >= 64 && !Object.hasOwn(this.state.counts, binding.attemptId)) throw new Denial('attempt_limit', 429);
      const reservation: Reservation = { requestId: randomUUID().replaceAll('-', ''), attemptId: binding.attemptId, taskId: binding.taskId, epoch: policy.epoch, outcome: 'active' };
      this.state.reservations.push(reservation);
      Object.defineProperty(this.state.counts, binding.attemptId, { value: count + 1, writable: true, enumerable: true, configurable: true });
      await this.persist();
      return { reservation: structuredClone(reservation), policy: structuredClone(policy) };
    }, caller);
  }
  async finish(requestId: string, outcome: Outcome, neverForwarded = false) {
    return this.serial(async signal => {
      const record = this.state.reservations.find(r => r.requestId === requestId);
      if (!record) throw new Denial('boundary_closed', 503);
      record.outcome = outcome;
      // Local cancellation/EOF cannot establish upstream quiescence.
      let quiescent = neverForwarded;
      quiescent ||= await this.authorityCall(s => this.authority.quiescent([requestId], s), signal);
      if (quiescent) this.state.reservations = this.state.reservations.filter(r => r !== record);
      await this.persist(); return quiescent;
    });
  }
  async drainAndReplace(next: Policy): Promise<boolean> {
    return this.serial(async signal => {
      if (this.failed || !['active', 'draining'].includes(this.state.phase)) throw new Denial('boundary_closed', 503);
      const previous = this.state.policy!;
      const reviewed = validatePolicy(next);
      if (reviewed.epoch !== previous.epoch + 1 || reviewed.revision === previous.revision || reviewed.routerId !== previous.routerId || reviewed.routeId !== previous.routeId) throw new Denial('policy_denied');
      this.state.phase = 'draining'; await this.persist();
      // An active local stream must finish even if an authority knows upstream work has stopped.
      if (this.state.reservations.some(r => r.outcome === 'active')) return false;
      if (!await this.authorityCall(s => this.authority.quiescent(this.state.reservations.map(r => r.requestId), s), signal)) return false;
      this.state.reservations = []; this.state.phase = 'mutating'; await this.persist();
      try {
        const actual = validatePolicy(await this.authorityCall(s => this.authority.replaceFrozenPolicy(reviewed, s), signal, true));
        if (canonical(actual) !== canonical(reviewed)) throw new Error();
        this.state.policy = actual; this.state.fingerprint = fingerprint(actual); this.state.phase = 'active';
        await this.persist(); return true;
      } catch { this.state.phase = 'closed'; throw new Denial('boundary_closed', 503); }
    });
  }
  close(): Promise<void> {
    if (this.closing) return this.closing;
    this.lifecycle = 'closing';
    this.closing = this.tail.then(async () => {
      this.state.phase = 'closed'; await this.persist(true);
      // An aborted writer may still be running in a non-cooperative implementation.
      // Keep its lock quarantined so that no new owner races a late external write.
      if (this.uncertainMutation) throw new Denial('boundary_closed', 503);
      await this.requireOwner(); await rm(join(this.directory, 'owner.lock'));
    }).finally(() => { this.lifecycle = 'closed'; });
    this.stop.abort(new Denial('boundary_closed', 503));
    return this.closing;
  }
}
