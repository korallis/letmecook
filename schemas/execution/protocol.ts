import { canonical, parseJSON } from '../../experiments/inference-boundary/json.ts';

export const VERSION = 'execution-provisional-v1' as const; // Historical #93 fixture format.
export const FENCED_VERSION = 'execution-provisional-v2' as const;
export const MAX_BYTES = 8192;
export type Refusal = 'ok' | 'duplicate' | 'malformed' | 'oversized' | 'unknown_version'
  | 'stale_generation' | 'stale_attempt' | 'identity_conflict' | 'revision_conflict'
  | 'invalid_transition' | 'nonce_mismatch' | 'boot_mismatch' | 'delayed_reply' | 'ack_not_durable' | 'reconciliation_required';
export type Identity = { generation: string; task_id: string; attempt_id: string; epoch: number };
export type Route = { route_ref: string; decision_digest: string; policy_digest: string;
  limits_profile: 'strict-provider-output-v1' | 'native-subscription-local-v1' | 'gateway-local-bounds-v1' };
export type Manifest = { manifest_id: string; sha256: string; bytes: number };
export type AttemptState = 'assigned' | 'starting' | 'running' | 'stopping' | 'result_pending'
  | 'succeeded' | 'failed' | 'cancelled' | 'expired' | 'unknown';
export type TaskState = 'draft' | 'ready' | 'active' | 'verifying' | 'awaiting_review'
  | 'accepted' | 'reconciling' | 'blocked' | 'failed' | 'cancelled';
type Envelope = { version: typeof VERSION | typeof FENCED_VERSION; message_id: string; identity: Identity };
export type Assignment = Envelope & { kind: 'assign'; assignment_id: string; input_digest: string; route: Route };
export type LeaseRequest = Envelope & { kind: 'lease_request'; nonce: string; runner_boot: string; daemon_boot: string; sent_ms: number };
export type LeaseReply = Envelope & { kind: 'lease_reply'; nonce: string; runner_boot: string; daemon_boot: string; validity_ms: number };
export type Transition = Envelope & { kind: 'transition'; expected_revision: number; from: AttemptState; to: AttemptState };
export type Result = Envelope & { kind: 'result'; manifest: Manifest };
export type ResultAck = Envelope & { kind: 'result_ack'; manifest: Manifest; receipt_id: string };
type FencedEnvelope = Envelope & { version: typeof FENCED_VERSION };
type Boots = { runner_boot: string; daemon_boot: string };
export type Accept = FencedEnvelope & Boots & { kind: 'accept'; assignment_id: string };
export type Refuse = FencedEnvelope & { kind: 'refuse'; in_reply_to: string; reason: Exclude<Refusal, 'ok' | 'duplicate'> };
export type Cancel = FencedEnvelope & Boots & { kind: 'cancel'; stop_id: string };
export type Terminated = FencedEnvelope & Boots & { kind: 'terminated'; stop_id: string;
  confirmed_process: 'not_started' | 'terminated'; remote_work: 'quiescent' | 'unknown'; evidence_digest: string };
export type Message = Assignment | LeaseRequest | LeaseReply | Transition | Result | ResultAck | Accept | Refuse | Cancel | Terminated;
export type Timing = { received_ms: number; drift_ms: number; termination_ms: number;
  runner_boot: string; daemon_boot: string; prior_stop_by_ms: number | null; nonce_active: boolean };
export type Receipt = { identity: Identity; manifest: Manifest; receipt_id: string;
  artifacts: 'unknown' | 'verified_durable'; metadata: 'event_only' | 'manifest_and_result_committed' };
export type Observation = { desired: 'run' | 'stop'; confirmed_process: 'not_started' | 'running' | 'terminated' | 'unknown';
  remote_work: 'quiescent' | 'unknown'; quarantined: boolean };
export type RecoveryEvent = 'partition' | 'daemon_restart' | 'runner_restart' | 'lease_expired'
  | 'stop_requested' | 'process_terminated' | 'remote_quiescent';

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const hash = /^[0-9a-f]{64}$/;
const states: AttemptState[] = ['assigned', 'starting', 'running', 'stopping', 'result_pending', 'succeeded', 'failed', 'cancelled', 'expired', 'unknown'];
const edges: Record<AttemptState, AttemptState[]> = {
  assigned: ['starting', 'stopping', 'unknown'], starting: ['running', 'stopping', 'unknown'],
  running: ['result_pending', 'stopping', 'unknown'], result_pending: ['succeeded', 'failed', 'stopping', 'unknown'],
  stopping: ['cancelled', 'expired', 'unknown'], unknown: ['stopping', 'cancelled', 'expired', 'result_pending'],
  succeeded: [], failed: [], cancelled: [], expired: [],
};
const fail = (reason: Refusal): never => { throw new Error(reason); };
const require = (condition: unknown) => { if (!condition) fail('malformed'); };
function fields(value: unknown, names: string[]): asserts value is Record<string, any> {
  require(value !== null && typeof value === 'object' && !Array.isArray(value));
  require(Object.keys(value as object).length === names.length && names.every(name => Object.hasOwn(value as object, name)));
}
const id = (v: unknown) => require(typeof v === 'string' && v.length === 36 && uuid.test(v));
const digest = (v: unknown) => require(typeof v === 'string' && v.length === 64 && hash.test(v));
const integer = (v: unknown, min = 0, max = Number.MAX_SAFE_INTEGER) => require(typeof v === 'number' && Number.isSafeInteger(v) && v >= min && v <= max);
function identity(v: unknown): asserts v is Identity {
  fields(v, ['generation', 'task_id', 'attempt_id', 'epoch']);
  id(v.generation); id(v.task_id); id(v.attempt_id); integer(v.epoch, 1);
}
function manifest(v: unknown): asserts v is Manifest {
  fields(v, ['manifest_id', 'sha256', 'bytes']); id(v.manifest_id); digest(v.sha256); integer(v.bytes, 1, 1048576);
}
function validate(v: unknown): asserts v is Message {
  require(v !== null && typeof v === 'object' && !Array.isArray(v));
  const m = v as Record<string, any>;
  if (typeof m.version === 'string' && m.version !== VERSION && m.version !== FENCED_VERSION) fail('unknown_version');
  require([VERSION, FENCED_VERSION].includes(m.version));
  const common = ['version', 'message_id', 'identity', 'kind'];
  const extra: Record<string, string[]> = {
    assign: ['assignment_id', 'input_digest', 'route'], lease_request: ['nonce', 'runner_boot', 'daemon_boot', 'sent_ms'],
    lease_reply: ['nonce', 'runner_boot', 'daemon_boot', 'validity_ms'], transition: ['expected_revision', 'from', 'to'],
    result: ['manifest'], result_ack: ['manifest', 'receipt_id'],
  };
  if (m.version === FENCED_VERSION) {
    extra.accept = ['assignment_id', 'runner_boot', 'daemon_boot'];
    extra.refuse = ['in_reply_to', 'reason'];
    extra.cancel = ['stop_id', 'runner_boot', 'daemon_boot'];
    extra.terminated = ['stop_id', 'runner_boot', 'daemon_boot', 'confirmed_process', 'remote_work', 'evidence_digest'];
  }
  require(typeof m.kind === 'string' && Object.hasOwn(extra, m.kind));
  fields(m, [...common, ...extra[m.kind]]); id(m.message_id); identity(m.identity);
  switch (m.kind) {
    case 'assign':
      id(m.assignment_id); digest(m.input_digest);
      fields(m.route, ['route_ref', 'decision_digest', 'policy_digest', 'limits_profile']);
      require(typeof m.route.route_ref === 'string' && /^[a-z][a-z0-9_-]{0,63}$/.exec(m.route.route_ref)?.[0] === m.route.route_ref);
      digest(m.route.decision_digest); digest(m.route.policy_digest);
      require(['strict-provider-output-v1', 'native-subscription-local-v1', 'gateway-local-bounds-v1'].includes(m.route.limits_profile)); break;
    case 'lease_request': case 'lease_reply':
      id(m.nonce); id(m.runner_boot); id(m.daemon_boot);
      if (m.kind === 'lease_request') integer(m.sent_ms); else integer(m.validity_ms, 1, 30000); break;
    case 'transition': integer(m.expected_revision, 1); require(states.includes(m.from) && states.includes(m.to)); break;
    case 'result': case 'result_ack': manifest(m.manifest); if (m.kind === 'result_ack') id(m.receipt_id); break;
    case 'accept': case 'cancel': case 'terminated':
      id(m.runner_boot); id(m.daemon_boot);
      if (m.kind === 'accept') id(m.assignment_id); else id(m.stop_id);
      if (m.kind === 'terminated') {
        require(['not_started', 'terminated'].includes(m.confirmed_process) && ['quiescent', 'unknown'].includes(m.remote_work));
        digest(m.evidence_digest);
      }
      break;
    case 'refuse':
      id(m.in_reply_to);
      require(['malformed', 'oversized', 'unknown_version', 'stale_generation', 'stale_attempt', 'identity_conflict',
        'revision_conflict', 'invalid_transition', 'nonce_mismatch', 'boot_mismatch', 'delayed_reply',
        'ack_not_durable', 'reconciliation_required'].includes(m.reason)); break;
  }
}

/** Untrusted UTF-8 wire boundary. Refusals contain no reflected input. */
export function decode(bytes: Uint8Array): Message {
  if (!(bytes instanceof Uint8Array)) fail('malformed');
  if (bytes.byteLength > MAX_BYTES) fail('oversized');
  let value: unknown;
  try {
    const text = new TextDecoder('utf-8', { fatal: true, ignoreBOM: true }).decode(bytes);
    // Preserve exact integer bounds before JSON.parse can round fractional values.
    for (const token of text.matchAll(/"(?:\\.|[^"\\])*"|(-?\d[\d.eE+-]*)/g)) {
      if (token[1] !== undefined && !/^(0|[1-9][0-9]*)$/.test(token[1])) fail('malformed');
    }
    value = parseJSON(text);
  } catch { return fail('malformed'); }
  validate(value); return value;
}
// Revalidate even typed arguments; no caller-provided object becomes authority.
function message(v: unknown): asserts v is Message { validate(v); }
function current(m: Message, c: Identity): Refusal {
  if (m.identity.generation !== c.generation) return 'stale_generation';
  return canonical(m.identity) === canonical(c) ? 'ok' : 'stale_attempt';
}
export function checkCurrent(m: Message, c: Identity): Refusal {
  message(m); identity(c); return current(m, c);
}
/** Exact negotiated execution version; not proof of TLS, peer identity or authority. */
export function checkSession(m: Message, c: Identity, selected: string): Refusal {
  message(m); identity(c); require(typeof selected === 'string');
  const refusal = current(m, c); if (refusal !== 'ok') return refusal;
  return selected === FENCED_VERSION && m.version === selected ? 'ok' : 'unknown_version';
}
/** Compare against a durable owner's retained message; this function retains nothing. */
export function checkReplay(m: Message, previous: Message, c: Identity): Refusal {
  message(m); message(previous); identity(c);
  const refusal = current(m, c); if (refusal !== 'ok') return refusal;
  if (m.message_id === previous.message_id) return canonical(m) === canonical(previous) ? 'duplicate' : 'identity_conflict';
  if (m.kind === 'assign' && previous.kind === 'assign' && m.assignment_id === previous.assignment_id) {
    const { message_id: _, ...a } = m; const { message_id: __, ...b } = previous;
    return canonical(a) === canonical(b) ? 'duplicate' : 'identity_conflict';
  }
  return 'ok';
}
export function checkTransition(m: Transition, c: Identity, state: AttemptState, revision: number): Refusal {
  message(m); identity(c); require(m.kind === 'transition' && states.includes(state)); integer(revision, 1);
  const refusal = current(m, c); if (refusal !== 'ok') return refusal;
  if (m.expected_revision !== revision || revision === Number.MAX_SAFE_INTEGER) return 'revision_conflict';
  return m.from === state && edges[state].includes(m.to) ? 'ok' : 'invalid_transition';
}
/** Pure timing calculation, not lease issuance or authorization. */
export function checkLease(request: LeaseRequest, reply: LeaseReply, c: Identity, timing: Timing): { reason: Refusal; stop_by_ms?: number } {
  message(request); message(reply); identity(c);
  require(request.kind === 'lease_request' && reply.kind === 'lease_reply');
  fields(timing, ['received_ms', 'drift_ms', 'termination_ms', 'runner_boot', 'daemon_boot', 'prior_stop_by_ms', 'nonce_active']);
  id(timing.runner_boot); id(timing.daemon_boot);
  integer(timing.received_ms); integer(timing.drift_ms, 1); integer(timing.termination_ms, 1);
  if (timing.prior_stop_by_ms !== null) integer(timing.prior_stop_by_ms);
  require(typeof timing.nonce_active === 'boolean');
  for (const m of [request, reply]) { const reason = current(m, c); if (reason !== 'ok') return { reason }; }
  if (request.version !== reply.version) return { reason: 'unknown_version' };
  if (request.runner_boot !== timing.runner_boot || reply.runner_boot !== timing.runner_boot ||
    request.daemon_boot !== timing.daemon_boot || reply.daemon_boot !== timing.daemon_boot) return { reason: 'boot_mismatch' };
  if (!timing.nonce_active || request.nonce !== reply.nonce) return { reason: 'nonce_mismatch' };
  require(timing.drift_ms + timing.termination_ms < reply.validity_ms);
  const end = request.sent_ms + reply.validity_ms; integer(end);
  const stop = end - timing.drift_ms - timing.termination_ms;
  if (timing.received_ms < request.sent_ms || timing.received_ms >= stop ||
    timing.prior_stop_by_ms !== null && (request.sent_ms >= timing.prior_stop_by_ms || timing.received_ms >= timing.prior_stop_by_ms)) return { reason: 'delayed_reply' };
  return { reason: 'ok', stop_by_ms: stop };
}
/** Receipt claims must come from the future trusted custody owner, never the worker. */
export function checkAck(result: Result, ack: ResultAck, c: Identity, receipt: Receipt): Refusal {
  message(result); message(ack); identity(c); require(result.kind === 'result' && ack.kind === 'result_ack');
  fields(receipt, ['identity', 'manifest', 'receipt_id', 'artifacts', 'metadata']);
  identity(receipt.identity); manifest(receipt.manifest); id(receipt.receipt_id);
  require(['unknown', 'verified_durable'].includes(receipt.artifacts));
  require(['event_only', 'manifest_and_result_committed'].includes(receipt.metadata));
  for (const m of [result, ack]) { const refusal = current(m, c); if (refusal !== 'ok') return refusal; }
  if (result.version !== ack.version) return 'unknown_version';
  if (canonical(result.manifest) !== canonical(ack.manifest)) return 'identity_conflict';
  if (canonical(receipt.identity) !== canonical(c) || canonical(receipt.manifest) !== canonical(result.manifest) ||
    receipt.receipt_id !== ack.receipt_id || receipt.artifacts !== 'verified_durable' || receipt.metadata !== 'manifest_and_result_committed') return 'ack_not_durable';
  return 'ok';
}
/** Conservative observation projection; never returns execution/retry permission. */
export function observe(state: Observation, event: RecoveryEvent): Observation {
  fields(state, ['desired', 'confirmed_process', 'remote_work', 'quarantined']);
  require(['run', 'stop'].includes(state.desired) && ['not_started', 'running', 'terminated', 'unknown'].includes(state.confirmed_process));
  require(['quiescent', 'unknown'].includes(state.remote_work) && typeof state.quarantined === 'boolean');
  require(['partition', 'daemon_restart', 'runner_restart', 'lease_expired', 'stop_requested', 'process_terminated', 'remote_quiescent'].includes(event));
  const next = { ...state };
  if (event === 'process_terminated') next.confirmed_process = 'terminated';
  else if (event === 'remote_quiescent') next.remote_work = 'quiescent';
  else {
    next.desired = 'stop';
    if (event !== 'stop_requested') { next.confirmed_process = 'unknown'; next.remote_work = 'unknown'; next.quarantined = true; }
  }
  return next;
}
