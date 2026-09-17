import { parseJSON, keys, object } from '../../experiments/inference-boundary/json.ts';
import { decode as decodeMessage, type Identity, type AttemptState, type TaskState, type Observation, type Assignment, type Transition } from '../execution/protocol.ts';

export const VERSION = 'read-provisional-v1' as const;
export const MAX_ITEMS = 50;
export const MAX_BYTES = 1048576;
export const MISSING_CAPABILITIES = ['execution', 'inference', 'artifact_custody', 'result_ack', 'acceptance', 'publication', 'merge', 'state_import', 'sessions'] as const;
export type Metadata = {
  version: typeof VERSION; mode: 'fixture-only' | 'store-only'; missing_capabilities: [...typeof MISSING_CAPABILITIES];
  generation: string; daemon_boot: string; schema_version: 1 | 2 | 3 | 4 | 5 | 6 | 7;
};
export type Attempt = { identity: Identity; state: AttemptState; revision: number; observation: Observation };
export type Task = { task_id: string; state: TaskState; attempt: Attempt };
export type Event = { sequence: number; revision: number; message: Assignment | Transition };
export type Snapshot = Metadata & { tasks: Task[]; events: Event[] };
export type Status = Metadata & { task_count: number; event_count: number };

const require = (v: unknown): void => { if (!v) throw new Error('invalid_read_response'); };
const fields = (v: unknown, names: string[]): void => keys(v, names, names);
const id = (v: unknown): void => require(typeof v === 'string' && v.length === 36 && /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(v));
const integer = (v: unknown, min = 1): void => require(typeof v === 'number' && Number.isSafeInteger(v) && v >= min);
const metadata = ['version', 'mode', 'missing_capabilities', 'generation', 'daemon_boot', 'schema_version'];

/** Closed, bounded runtime schema for same-origin #95 reads; no fetch or effects. */
export function decode(bytes: Uint8Array, kind: 'status'): Status;
export function decode(bytes: Uint8Array, kind: 'snapshot'): Snapshot;
export function decode(bytes: Uint8Array, kind: 'status' | 'snapshot'): Status | Snapshot {
  require(bytes instanceof Uint8Array && bytes.byteLength <= MAX_BYTES);
  require(kind === 'status' || kind === 'snapshot');
  const text = new TextDecoder('utf-8', { fatal: true, ignoreBOM: true }).decode(bytes);
  for (const token of text.matchAll(/"(?:\\.|[^"\\])*"|(-?\d[\d.eE+-]*)/g)) {
    if (token[1] !== undefined) require(/^(0|[1-9][0-9]*)$/.test(token[1]));
  }
  const v = parseJSON(text); object(v);
  fields(v, [...metadata, ...(kind === 'status' ? ['task_count', 'event_count'] : ['tasks', 'events'])]);
  require(v.version === VERSION && (v.mode === 'fixture-only' && v.schema_version === 1 || v.mode === 'store-only' && (v.schema_version === 2 || v.schema_version === 3 || v.schema_version === 4 || v.schema_version === 5 || v.schema_version === 6 || v.schema_version === 7)));
  require(Array.isArray(v.missing_capabilities) && JSON.stringify(v.missing_capabilities) === JSON.stringify(MISSING_CAPABILITIES));
  id(v.generation); id(v.daemon_boot);
  if (kind === 'status') { integer(v.task_count, 0); integer(v.event_count, 0); return v as Status; }
  require(Array.isArray(v.tasks) && v.tasks.length <= MAX_ITEMS && Array.isArray(v.events) && v.events.length <= MAX_ITEMS);
  const seen = new Set<string>();
  for (const t of v.tasks) {
    fields(t, ['task_id', 'state', 'attempt']); id(t.task_id);
    require(!seen.has(t.task_id)); seen.add(t.task_id);
    require(['ready', 'reconciling', 'verifying', 'awaiting_review'].includes(t.state));
    const a = t.attempt; fields(a, ['identity', 'state', 'revision', 'observation']);
    fields(a.identity, ['generation', 'task_id', 'attempt_id', 'epoch']);
    require(a.identity.generation === v.generation && a.identity.task_id === t.task_id);
    id(a.identity.attempt_id); integer(a.identity.epoch); integer(a.revision);
    require(['assigned', 'starting', 'running', 'result_pending', 'stopping', 'unknown', 'succeeded', 'failed', 'cancelled', 'expired'].includes(a.state));
    fields(a.observation, ['desired', 'confirmed_process', 'remote_work', 'quarantined']);
    require(a.observation.desired === 'stop' && a.observation.confirmed_process === (v.mode === 'fixture-only' ? 'not_started' : 'unknown') && a.observation.remote_work === 'unknown' && a.observation.quarantined === true);
  }
  let previous = 0;
  for (const e of v.events) {
    fields(e, ['sequence', 'revision', 'message']); integer(e.sequence); integer(e.revision);
    require(e.sequence > previous); previous = e.sequence;
    const m = decodeMessage(new TextEncoder().encode(JSON.stringify(e.message)));
    require(m.identity.generation === v.generation && (m.kind === 'assign' || m.kind === 'transition'));
    require(v.mode === 'fixture-only' || m.version === 'execution-provisional-v2');
  }
  return v as Snapshot;
}
