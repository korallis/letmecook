import { keys, parseJSON } from '../../experiments/inference-boundary/json.ts';
import { decode, MAX_BYTES, MAX_ITEMS, MISSING_CAPABILITIES, VERSION, type Snapshot } from '../../schemas/readapi/types.ts';

// Closed set of daemon error codes (internal/httpapi/api.go). Anything else is malformed.
export const ERROR_CODES = ['boundary_refused', 'origin_refused', 'proxy_refused', 'read_only', 'invalid_request',
  'not_found', 'invalid_query', 'invalid_id', 'invalid_bound', 'busy', 'store_unavailable', 'invalid_snapshot'] as const;
export type ErrorCode = (typeof ERROR_CODES)[number];

export type ReadState =
  | { kind: 'loading' }
  | { kind: 'ready'; snapshot: Snapshot }
  | { kind: 'empty'; snapshot: Snapshot }
  | { kind: 'server-error'; code: ErrorCode; status: number }
  | { kind: 'malformed'; status: number }
  | { kind: 'unavailable' }
  | { kind: 'unknown' };

export const SNAPSHOT_PATH = `/api/v1/snapshot?limit=${MAX_ITEMS}`;
export const TIMEOUT_MS = 4000;

function errorCode(bytes: Uint8Array): ErrorCode | null {
  try {
    if (bytes.byteLength > 4096) return null;
    const v = parseJSON(new TextDecoder('utf-8', { fatal: true }).decode(bytes));
    keys(v, ['version', 'error', 'mode', 'missing_capabilities'], ['version', 'error', 'mode', 'missing_capabilities']);
    const o = v as Record<string, unknown>;
    if (o.version !== VERSION || o.mode !== 'fixture-only' || JSON.stringify(o.missing_capabilities) !== JSON.stringify(MISSING_CAPABILITIES)) return null;
    const code = ERROR_CODES.find(c => c === o.error);
    return code ?? null;
  } catch {
    return null;
  }
}

/** One bounded same-origin GET. Never throws; every failure is a named state. */
export async function readSnapshot(fetchImpl: typeof fetch = fetch): Promise<ReadState> {
  let res: Response;
  let bytes: Uint8Array;
  try {
    res = await fetchImpl(SNAPSHOT_PATH, { method: 'GET', credentials: 'omit', cache: 'no-store', redirect: 'error', signal: AbortSignal.timeout(TIMEOUT_MS) });
    const length = Number(res.headers.get('content-length') ?? 0);
    if (length > MAX_BYTES) return { kind: 'malformed', status: res.status };
    bytes = new Uint8Array(await res.arrayBuffer());
  } catch (error) {
    return { kind: error instanceof DOMException && error.name === 'TimeoutError' ? 'unknown' : 'unavailable' };
  }
  if (res.status !== 200) {
    const code = errorCode(bytes);
    return code ? { kind: 'server-error', code, status: res.status } : { kind: 'malformed', status: res.status };
  }
  try {
    const snapshot = decode(bytes, 'snapshot');
    return { kind: snapshot.tasks.length === 0 && snapshot.events.length === 0 ? 'empty' : 'ready', snapshot };
  } catch {
    return { kind: 'malformed', status: res.status };
  }
}

export const STATUS_WORDS: Record<ReadState['kind'], string> = {
  loading: 'Loading: snapshot request in flight',
  ready: 'Read: bounded fixture snapshot decoded',
  empty: 'Empty: snapshot decoded with no tasks or events',
  'server-error': 'Server error: daemon refused the read',
  malformed: 'Malformed: response refused by schema check',
  unavailable: 'Unavailable: request failed before a response',
  unknown: 'Unknown: request timed out; daemon state not observed',
};
