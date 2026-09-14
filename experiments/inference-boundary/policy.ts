import { open, mkdir, readFile, rename, rm } from 'node:fs/promises';
import { join } from 'node:path';
import { randomUUID } from 'node:crypto';
import { canonical, keys, object, parseJSON } from './json.ts';
import { Denial, fingerprint, ref, validateBinding, validatePolicy, type Binding, type Outcome, type Policy } from './types.ts';
import { assertNativeBinding } from './native-policy.ts';
import { validateNativeRequest, continuationOutput } from '../router-authority-extension/overlay/native-responses.mjs';
import { abortable } from './wait.ts';
import { classifyReceipt, hashDocument, sha, validateRouterPolicy, type RouterReservation, type ReceiptEvidence } from './router-policy.ts';

// Implementations are trusted configuration authorities outside the worker HTTP surface.
// Both supported families are synthetic. Receipt authority is mandatory for schema 2.
export interface FrozenAuthority {
  readFrozenPolicy(signal: AbortSignal): Promise<Policy>;
  quiescent(requestIds: string[], signal: AbortSignal): Promise<boolean>;
  replaceFrozenPolicy(next: Policy, signal: AbortSignal): Promise<Policy>;
}
export interface ReceiptAuthority extends FrozenAuthority {
  readonly kind: 'router-receipts-v1';
  inspect(record: Reservation, signal: AbortSignal, recovery?: boolean): Promise<ReceiptEvidence>;
  waitReceipt(record: Reservation, signal: AbortSignal): Promise<ReceiptEvidence>;
  assertCurrent(record: Reservation, admission?: boolean): void;
  cancel(requestId: string): void;
  prepare?(record: Reservation): void;
}
export interface Decision {
  requestId: string; taskId: string; attemptId: string; router: RouterReservation;
  verdict: 'validated_success' | 'quiescent_failure' | 'never_sent';
  evidence: ReceiptEvidence; nativeOutput?: any[]; completionDigest: string | null; delivery: Outcome | 'unobserved';
}
export interface Reservation {
  requestId: string; attemptId: string; taskId: string; epoch: number;
  outcome: Outcome | 'active'; router?: RouterReservation;
}
interface Journal {
  schema: 1 | 2; decisions?: Decision[]; generation: string; phase: 'closed' | 'active' | 'draining' | 'mutating';
  policy: Policy | null; fingerprint: string | null;
  reservations: Reservation[]; counts: Record<string, number>;
}
function validateJournal(value: unknown): asserts value is Journal {
  keys(value, ['schema', 'generation', 'phase', 'policy', 'fingerprint', 'reservations', 'counts', 'decisions'], ['schema', 'generation', 'phase', 'policy', 'fingerprint', 'reservations', 'counts']); object(value);
  if (![1,2].includes(value.schema) || typeof value.generation !== 'string' || !['closed', 'active', 'draining', 'mutating'].includes(value.phase) || !Array.isArray(value.reservations) || value.reservations.length > 16) throw new Error();
  if (value.policy !== null && fingerprint(validatePolicy(value.policy)) !== value.fingerprint || value.policy === null && value.fingerprint !== null) throw new Error();
  if (value.schema === 1 && (value.decisions !== undefined || (value.policy?.schema === 2 || value.policy?.schema === 3)) || value.schema === 2 && (!Array.isArray(value.decisions) || value.decisions.length > 64 || ![2,3].includes(value.policy?.schema))) throw new Error();
  const ids = new Set<string>();
  for (const r of value.reservations) {
    keys(r, ['requestId', 'attemptId', 'taskId', 'epoch', 'outcome', 'router'], ['requestId', 'attemptId', 'taskId', 'epoch', 'outcome']);
    if (!/^[a-f0-9]{32}$/.test(r.requestId) || ids.has(r.requestId) || !ref(r.attemptId) || !ref(r.taskId) || !Number.isSafeInteger(r.epoch) || r.epoch < 1 || !['active', 'completed', 'failed_before_output', 'partial_failure', 'cancelled_unknown'].includes(r.outcome)) throw new Error();
    if (value.schema === 2) { validateSaved(r.router, r); if (canonical(r.router.policy) !== canonical(value.policy)) throw new Error(); } else if (r.router !== undefined) throw new Error();
    ids.add(r.requestId);
  }
  const decisions = new Set<string>();
  for (const d of value.decisions ?? []) {
    keys(d,['requestId','taskId','attemptId','router','verdict','evidence','completionDigest','delivery','nativeOutput'],['requestId','taskId','attemptId','router','verdict','evidence','completionDigest','delivery']);
    validateSaved(d.router, d);
    if (!/^[a-f0-9]{32}$/.test(d.requestId) || decisions.has(d.requestId) || !['validated_success','quiescent_failure','never_sent'].includes(d.verdict) || !['unobserved','completed','failed_before_output','partial_failure','cancelled_unknown'].includes(d.delivery)) throw new Error();
    keys(d.evidence,['disposition','receiptDigest','operations'],['disposition','receiptDigest','operations']);
    if (!['original_success','quiescent_failure','pending_or_unknown'].includes(d.evidence.disposition) || !Array.isArray(d.evidence.operations) || d.evidence.operations.length > (d.router.policy.schema===3?d.router.policy.native.scope.maxInferenceAttempts:16) || d.evidence.receiptDigest !== null && !sha(d.evidence.receiptDigest)) throw new Error();
    if (d.verdict === 'validated_success' && (!sha(d.completionDigest) || d.evidence.disposition !== 'original_success' || !sha(d.evidence.receiptDigest)) || d.verdict !== 'validated_success' && d.completionDigest !== null) throw new Error();
    if (d.verdict === 'never_sent') {
      if (d.router.send !== 'reserved' || d.evidence.operations.length || d.evidence.receiptDigest !== null) throw new Error();
    } else {
      const p = d.router.policy;
      const classified = classifyReceipt({id:d.requestId,known:true,quiescent:true,boot:p.authority.boot,generation:p.authority.generation,revision:p.revision,route:p.routerModel,handler_done:1,local_stop:'local_eof',operations:d.evidence.operations,...(p.schema===3?{native:{profileDigest:hashDocument(p.native),scopeId:p.native.scope.id,authorizationDigest:p.native.scope.authorizationDigest,bindingDigest:hashDocument(d.router.binding),requestDigest:d.router.requestDigest}}:{})},d.requestId,d.router);
      if (d.router.send !== 'send_possible' || !sha(d.evidence.receiptDigest) || classified.disposition === 'pending_or_unknown' || d.verdict === 'validated_success' && classified.disposition !== 'original_success') throw new Error();
    }
    const live = value.reservations.find((r: Reservation) => r.requestId === d.requestId);
    if(d.router.policy.schema===3&&d.verdict==='validated_success'){continuationOutput(d.nativeOutput,d.router.policy.native);if(hashDocument(d.nativeOutput)!==d.evidence.operations.at(-1)?.output_digest)throw Error();}else if(d.nativeOutput!==undefined)throw Error();
    if (live && (live.taskId !== d.taskId || live.attemptId !== d.attemptId || canonical(live.router) !== canonical(d.router))) throw new Error();
    decisions.add(d.requestId);
  }
  object(value.counts);
  if (Object.keys(value.counts).length > 64 || Object.entries(value.counts).some(([key, count]) => !ref(key) || !Number.isSafeInteger(count) || (count as number) < 1 || (count as number) > 32)) throw new Error();
  const joined = new Map([...value.reservations,...(value.decisions ?? [])].map(r => [r.requestId,r]));
  for (const r of joined.values()) if (!Object.hasOwn(value.counts,r.attemptId) || value.counts[r.attemptId] < [...joined.values()].filter(x => x.attemptId === r.attemptId).length) throw new Error();
  const reserved = new Map<string, number>();
  for (const r of value.reservations) {
    reserved.set(r.attemptId, (reserved.get(r.attemptId) ?? 0) + 1);
    if (!Object.hasOwn(value.counts, r.attemptId) || value.counts[r.attemptId] < reserved.get(r.attemptId)! || value.policy === null || r.epoch !== value.policy.epoch) throw new Error();
  }
}
function validateSaved(saved: RouterReservation, owner: {attemptId:string;taskId:string}) {
  keys(saved,['policy','binding','requestDigest','send','nativeRequest'],['policy','binding','requestDigest','send']);
  validateRouterPolicy(saved.policy); validateBinding(saved.binding);
  if(saved.policy.schema===3){assertNativeBinding(saved.binding,saved.policy);if(!saved.nativeRequest||hashDocument(JSON.stringify(saved.nativeRequest))!==saved.requestDigest)throw Error();}else if(saved.binding.native||saved.nativeRequest!==undefined)throw Error();
  if (!sha(saved.requestDigest) || !['reserved','send_possible'].includes(saved.send) || saved.binding.attemptId !== owner.attemptId || saved.binding.taskId !== owner.taskId || saved.binding.epoch !== saved.policy.epoch || saved.binding.revision !== saved.policy.revision || saved.binding.routeId !== saved.policy.routeId || saved.binding.routerId !== saved.policy.routerId) throw new Error();
}
export class PolicyGate {
  private tail: Promise<unknown> = Promise.resolve();
  private state: Journal;
  private durableDecisions: Decision[] = [];
  private failed = false;
  private lifecycle: 'open' | 'closing' | 'closed' = 'open';
  private closing: Promise<void> | undefined;
  private stop = new AbortController();
  private uncertainMutation = false;
  private readonly directory: string;
  private readonly authority: FrozenAuthority;
  private readonly owner: string;
  private readonly authorityMs: number;
  private constructor(directory: string, authority: FrozenAuthority, state: Journal, owner: string, authorityMs: number) { this.directory = directory; this.authority = authority; this.state = state; this.durableDecisions = structuredClone(state.decisions ?? []); this.owner = owner; this.authorityMs = authorityMs; }
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
    try { const raw = await readFile(join(directory, 'state.json'), 'utf8'); if (Buffer.byteLength(raw) > 16777216) throw new Error(); const decoded = parseJSON(raw, true); validateJournal(decoded); previous = decoded; }
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
      await this.syncDirectory();
      this.durableDecisions = structuredClone(this.state.decisions ?? []);
    } catch { this.failed = true; this.state.phase = 'closed'; throw new Denial('boundary_closed', 503); }
  }
  private async syncDirectory() {
    const directory = await open(this.directory, 'r');
    try { await directory.sync(); } finally { await directory.close(); }
  }
  private checkWritable(closing: boolean) {
    if (this.lifecycle === 'closed' || this.lifecycle === 'closing' && !closing) throw new Denial('boundary_closed', 503);
  }
  snapshot(): Journal { return structuredClone({...this.state,...(this.state.schema === 2 ? {decisions:this.durableDecisions} : {})}); }
  private receipts(): ReceiptAuthority {
    const a = this.authority as ReceiptAuthority;
    if (a.kind !== 'router-receipts-v1' || !['inspect','waitReceipt','assertCurrent','cancel'].every(k => typeof (a as any)[k] === 'function')) throw new Denial('boundary_closed',503);
    return a;
  }
  cancel(requestId: string) { if (this.state.schema === 2) this.receipts().cancel(requestId); }
  assertRelease(requestId: string) {
    this.check();
    const r = this.state.reservations.find(r => r.requestId === requestId);
    if (this.state.schema === 2) {
      if (!['active','draining'].includes(this.state.phase) || !r?.router || !this.state.decisions?.some(d => d.requestId === requestId && d.verdict === 'validated_success')) throw new Denial('receipt_unverified',502);
      this.receipts().assertCurrent(r);
    }
  }
  private recordDecision(r: Reservation, evidence: ReceiptEvidence, verdict: Decision['verdict'], completionDigest: string | null = null) {
    const found = this.state.decisions!.find(d => d.requestId === r.requestId);
    if (found) return found;
    if (this.state.decisions!.length >= 64) throw new Denial('attempt_limit',429);
    const d: Decision = {requestId:r.requestId,taskId:r.taskId,attemptId:r.attemptId,router:structuredClone(r.router!),verdict,evidence:structuredClone(evidence),completionDigest,delivery:'unobserved'};
    this.state.decisions!.push(d); return d;
  }
  async markSend(requestId: string) {
    return this.serial(async signal => {
      const r = this.state.reservations.find(r => r.requestId === requestId);
      if (this.state.phase !== 'active' || !r?.router || r.router.send !== 'reserved') throw new Denial('boundary_closed',503);
      this.receipts().assertCurrent(r, true); this.check(signal);
      r.router.send = 'send_possible'; await this.persist();
      if(r.router.policy.schema===3){if(!this.receipts().prepare)throw new Denial('boundary_closed',503);this.receipts().prepare!(structuredClone(r));}
    });
  }
  async finalize(requestId: string, completionDigest: string, current: () => boolean, caller: AbortSignal, nativeOutput?: any[]) {
    return this.serial(async signal => {
      const r = this.state.reservations.find(r => r.requestId === requestId);
      if (!['active','draining'].includes(this.state.phase) || !r?.router || r.router.send !== 'send_possible' || !sha(completionDigest)) throw new Denial('boundary_closed',503);
      const evidence = await this.authorityCall(s => this.receipts().waitReceipt(structuredClone(r),s),signal);
      if (evidence.disposition !== 'original_success') throw new Denial('receipt_unverified',502);
      if (!current()) throw new Denial('cancelled',409);
      this.receipts().assertCurrent(r);
      // Validate the original/translated join before mutating decision history.
      if(r.router.policy.schema===3){if(!nativeOutput||hashDocument(nativeOutput)!==evidence.operations.at(-1)?.output_digest)throw new Denial('invalid_stream',502);continuationOutput(nativeOutput,r.router.policy.native);}
      const decision=this.recordDecision(r,evidence,'validated_success',completionDigest);
      if(r.router.policy.schema===3)decision.nativeOutput=structuredClone(nativeOutput);
      await this.persist();
      this.check(signal); if (!current()) throw new Denial('cancelled',409);
      this.receipts().assertCurrent(r);
    },caller);
  }
  async activate() {
    return this.serial(async signal => {
      if (this.failed || this.state.phase !== 'closed') throw new Denial('boundary_closed', 503);
      if (this.state.schema === 2) {
        for (const r of [...this.state.reservations]) {
          const evidence = r.router!.send === 'reserved' ? {disposition:'quiescent_failure' as const,receiptDigest:null,operations:[]} : await this.authorityCall(s => this.receipts().inspect(structuredClone(r),s,true),signal);
          if (evidence.disposition === 'pending_or_unknown') throw new Denial('boundary_closed',503);
          this.recordDecision(r,evidence,r.router!.send === 'reserved' ? 'never_sent' : 'quiescent_failure');
          this.state.reservations = this.state.reservations.filter(x => x !== r); await this.persist();
        }
      }
      const policy = validatePolicy(await this.authorityCall(s => this.authority.readFrozenPolicy(s), signal));
      if (policy.schema !== 1 && this.state.schema === 1 && (this.state.policy !== null || this.state.reservations.length || Object.keys(this.state.counts).length)) throw new Denial('policy_denied');
      if (!await this.authorityCall(s => this.authority.quiescent(this.state.reservations.map(r => r.requestId), s), signal)) throw new Denial('boundary_closed', 503);
      this.state.reservations = [];
      if (policy.schema !== 1 && this.state.schema === 1 && (this.state.policy !== null || this.state.reservations.length || Object.keys(this.state.counts).length)) throw new Denial('policy_denied');
      if (policy.schema !== 1) { this.receipts(); this.state.schema = 2; this.state.decisions ??= []; }
      else if (this.state.schema === 2) throw new Denial('policy_denied');
      this.state.policy = policy; this.state.fingerprint = fingerprint(policy); this.state.phase = 'active';
      await this.persist(); return structuredClone(policy);
    });
  }
  nativePrevious(attemptId:string){const previous=this.state.decisions?.filter(d=>d.attemptId===attemptId&&d.verdict==='validated_success').at(-1);return previous?{request:previous.router.nativeRequest,output:previous.nativeOutput!}:null;}
  async admit(binding: Binding, current: () => boolean, caller?: AbortSignal, requestDigest?: string, nativeRequest?: any) {
    validateBinding(binding);
    return this.serial(async signal => {
      const policy = this.state.policy;
      if (this.failed || this.state.phase !== 'active' || !policy) throw new Denial('boundary_closed', 503);
      if (policy.schema !== 1) {
        this.receipts().assertCurrent({requestId:'',attemptId:binding.attemptId,taskId:binding.taskId,epoch:policy.epoch,outcome:'active',router:{policy,binding,requestDigest:requestDigest ?? '',send:'reserved'}},true);
        if (!sha(requestDigest) || this.state.decisions!.length + this.state.reservations.length >= 64) throw new Denial('attempt_limit',429);
        if ((policy.schema===3 || policy.profile === 'router-native-chat-translation-synthetic-v1') && this.state.reservations.length) throw new Denial('concurrency_limit',429);
      }
      if(policy.schema===3){assertNativeBinding(binding,policy);validateNativeRequest(nativeRequest,policy.native,policy.routerModel,this.nativePrevious(binding.attemptId));}else if(binding.native)throw new Denial('policy_denied');
      const retired = this.state.reservations.filter(r => r.outcome !== 'active');
      if (policy.schema === 1 && retired.length && await this.authorityCall(s => this.authority.quiescent(retired.map(r => r.requestId), s), signal)) this.state.reservations = this.state.reservations.filter(r => r.outcome === 'active');
      this.check(signal);
      if (!current()) throw new Denial('unauthorized', 401);
      if (binding.routerId !== policy.routerId || binding.routeId !== policy.routeId || binding.epoch !== policy.epoch || binding.revision !== policy.revision) throw new Denial('policy_denied');
      if (this.state.reservations.filter(r => r.attemptId === binding.attemptId).length >= policy.limits.concurrency || this.state.reservations.length >= 16) throw new Denial('concurrency_limit', 429);
      const count = Object.hasOwn(this.state.counts, binding.attemptId) ? this.state.counts[binding.attemptId] : 0;
      if (!Number.isSafeInteger(count) || count < 0) throw new Denial('boundary_closed', 503);
      if (count >= policy.limits.requestCount || Object.keys(this.state.counts).length >= 64 && !Object.hasOwn(this.state.counts, binding.attemptId)) throw new Denial('attempt_limit', 429);
      const reservation: Reservation = { requestId: randomUUID().replaceAll('-', ''), attemptId: binding.attemptId, taskId: binding.taskId, epoch: policy.epoch, outcome: 'active' };
      if (policy.schema !== 1) reservation.router = {policy:structuredClone(policy),binding:structuredClone(binding),requestDigest:requestDigest!,send:'reserved',...(policy.schema===3?{nativeRequest:structuredClone(nativeRequest)}:{})};
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
      if (record.router) {
        const noSend = record.router.send === 'reserved';
        const evidence: ReceiptEvidence = noSend ? {disposition:'quiescent_failure',receiptDigest:null,operations:[]} : await this.authorityCall(s => this.receipts().inspect(structuredClone(record),s,true),signal);
        if (evidence.disposition !== 'pending_or_unknown') {
          const d = this.recordDecision(record,evidence,noSend ? 'never_sent' : 'quiescent_failure');
          d.delivery = outcome; this.state.reservations = this.state.reservations.filter(r => r !== record);
        }
        await this.persist(); return evidence.disposition !== 'pending_or_unknown';
      }
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
      if (previous.schema !== 1) { this.state.phase = 'closed'; await this.persist(); throw new Denial('replacement_read_only'); }
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
