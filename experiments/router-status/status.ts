/** Backend-only M0 experiment; never import this module into a browser bundle. */
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { Ajv } from 'ajv';

export const INSPECTED_COMMIT = '69724d86d4486fa5d722e63ebb6c9700e71aebff';
export const DEPLOYED_COMMIT = '17c4cc76877bd1755030a8414f8d0083f48dcccf';
const PASSIVE_PATH = '/api/providers';
const states = ['eligible', 'exhausted', 'blocked', 'disabled', 'unknown'] as const;
type State = typeof states[number];
type Failure = 'unsupported_version' | 'schema_mismatch' | 'unavailable';
type ObjectValue = Record<string, unknown>;

export interface ConnectionBinding {
  /** Operator-owned opaque name, never taken from a router response. */
  ref: string;
  providerRef: string;
  /** Used only for matching, never returned. */
  upstreamId: string;
  upstreamProvider: string;
}
export interface RouteBinding {
  routeId: string;
  policyRevision: string;
  connectionRefs: string[];
}
export interface Config {
  routerId: string;
  origin: string;
  managementCredential: string;
  inspectedCommit: string;
  connections: ConnectionBinding[];
  routes: RouteBinding[];
  /** Only for a disposable local synthetic server. HTTPS is required otherwise. */
  syntheticLoopback?: boolean;
  timeoutMs?: number;
  maxBytes?: number;
  freshnessMs?: number;
}
export interface Observation {
  schema_version: 1;
  router_id: string;
  route_id: string;
  policy_revision: string;
  projected_at: string;
  route: {
    state: 'unknown';
    readiness: 'unknown';
    reason: 'no_observation' | 'policy_unverified' | 'malformed_observation' | 'router_unreachable';
    source: 'none';
    observed_at: null;
    validity_until: null;
  };
  connections: Array<{
    connection_ref: string;
    provider_ref: string;
    state: State;
    source: 'none' | 'router_snapshot';
    observed_at: string | null;
    validity_until: string | null;
    retry_after: string | null;
  }>;
  capabilities: { state: 'unknown'; source: 'none'; observed_at: null; validity_until: null };
}

const ajv = new Ajv({ allErrors: false, strict: true, strictRequired: false, allowUnionTypes: true });
const addFormats: (instance: Ajv) => void = createRequire(import.meta.url)('ajv-formats');
addFormats(ajv);
const validateObservation: (data: unknown) => boolean = ajv.compile(JSON.parse(readFileSync(new URL('../../tests/fixtures/9router/status.schema.json', import.meta.url), 'utf8')));

class ProjectionFailure extends Error {
  code: Failure;
  constructor(code: Failure) {
    super(code);
    this.code = code;
  }
}
function object(value: unknown): value is ObjectValue {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}
function badSchema(): never { throw new ProjectionFailure('schema_mismatch'); }
function timestamp(value: unknown): string | null {
  if (value === undefined || value === null) return null;
  // Reject coercion and normalise to a date, never pass an upstream string through.
  if (typeof value !== 'string' || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{1,3})?Z$/.test(value)) badSchema();
  const time = Date.parse(value);
  if (!Number.isFinite(time)) badSchema();
  const normalized = new Date(time).toISOString();
  if (normalized.slice(0, 19) !== value.slice(0, 19)) badSchema();
  return normalized;
}
function enumField(value: unknown, values: readonly string[]): string | null {
  if (value === undefined || value === null) return null;
  if (typeof value !== 'string' || !values.includes(value)) badSchema();
  return value;
}
function boundedInteger(value: number | undefined, fallback: number, max: number): number {
  if (value === undefined) return fallback;
  if (!Number.isSafeInteger(value) || value < 1 || value > max) throw new Error('invalid_configuration');
  return value;
}
function validateConfig(config: Config): Config {
  // Copy authority inputs so callers cannot widen URL/identity policy during a read.
  let copy: Config;
  try { copy = structuredClone(config); } catch { throw new Error('invalid_configuration'); }
  let url: URL;
  try { url = new URL(copy.origin); } catch { throw new Error('invalid_configuration'); }
  const loopback = ['127.0.0.1', '[::1]', 'localhost'].includes(url.hostname);
  if (url.username || url.password || url.pathname !== '/' || url.search || url.hash ||
      (url.protocol !== 'https:' && !(copy.syntheticLoopback && loopback && url.protocol === 'http:'))) {
    throw new Error('invalid_configuration');
  }
  copy.origin = url.origin;
  if (typeof copy.managementCredential !== 'string' || copy.managementCredential.length < 1 ||
      copy.managementCredential.length > 4096 || /[\r\n]/.test(copy.managementCredential)) throw new Error('invalid_configuration');
  const name = (value: unknown): value is string => typeof value === 'string' && /^[a-z][a-z0-9_-]{0,63}$/.test(value);
  if (!name(copy.routerId) || !Array.isArray(copy.connections) || copy.connections.length > 128 ||
      !Array.isArray(copy.routes) || copy.routes.length > 256) throw new Error('invalid_configuration');
  const refs = new Set<string>();
  const ids = new Set<string>();
  for (const entry of copy.connections) {
    if (!entry || !name(entry.ref) || !name(entry.providerRef) || typeof entry.upstreamProvider !== 'string' ||
        !entry.upstreamProvider || entry.upstreamProvider.length > 256 || typeof entry.upstreamId !== 'string' ||
        entry.upstreamId.length < 1 || entry.upstreamId.length > 256 || refs.has(entry.ref) || ids.has(entry.upstreamId)) {
      throw new Error('invalid_configuration');
    }
    refs.add(entry.ref); ids.add(entry.upstreamId);
  }
  const routes = new Set<string>();
  for (const route of copy.routes) {
    if (!route || !name(route.routeId) || !name(route.policyRevision) || routes.has(route.routeId) || !Array.isArray(route.connectionRefs) ||
        route.connectionRefs.length > 128 || new Set(route.connectionRefs).size !== route.connectionRefs.length ||
        route.connectionRefs.some(ref => !refs.has(ref))) throw new Error('invalid_configuration');
    routes.add(route.routeId);
  }
  return copy;
}

async function boundedJSON(response: Response, maxBytes: number): Promise<unknown> {
  if (response.headers.get('content-type')?.split(';')[0]?.trim() !== 'application/json') badSchema();
  if (!response.body) badSchema();
  const reader = response.body.getReader();
  const chunks: Uint8Array[] = [];
  let size = 0;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      size += value.byteLength;
      if (size > maxBytes) badSchema();
      chunks.push(value);
    }
    return JSON.parse(Buffer.concat(chunks).toString('utf8')) as unknown;
  } catch (error) {
    // Neither parser messages nor body excerpts become public diagnostics.
    if (error instanceof ProjectionFailure) throw error;
    throw new ProjectionFailure('schema_mismatch');
  } finally {
    await reader.cancel().catch(() => undefined);
    reader.releaseLock();
  }
}

function connectionsById(payload: unknown): Map<string, ObjectValue> {
  if (!object(payload) || !Array.isArray(payload.connections) || payload.connections.length > 1024) badSchema();
  const map = new Map<string, ObjectValue>();
  for (const entry of payload.connections) {
    if (!object(entry) || typeof entry.id !== 'string' || entry.id.length > 256 || !entry.id || map.has(entry.id)) badSchema();
    map.set(entry.id, entry);
  }
  return map;
}

function projectConnection(entry: ObjectValue | undefined, binding: ConnectionBinding, now: number, freshnessMs: number): Observation['connections'][number] {
  const empty: Observation['connections'][number] = {
    connection_ref: binding.ref, provider_ref: binding.providerRef, state: 'unknown', source: 'none',
    observed_at: null, validity_until: null, retry_after: null,
  };
  if (!entry) return empty;
  if (entry.provider !== binding.upstreamProvider) badSchema();
  if (entry.isActive !== undefined && entry.isActive !== null && typeof entry.isActive !== 'boolean') badSchema();
  const routing = enumField(entry.routingStatus, states);
  const auth = enumField(entry.authState, ['ok', 'expired', 'invalid', 'revoked', 'unknown']);
  const health = enumField(entry.healthStatus, ['healthy', 'degraded', 'error', 'failed', 'unhealthy', 'down', 'unknown']);
  const quota = enumField(entry.quotaState, ['ok', 'exhausted', 'blocked', 'unknown']);
  const lastCheckedAt = timestamp(entry.lastCheckedAt);
  const age = lastCheckedAt === null ? null : now - Date.parse(lastCheckedAt);
  // Reading status does not refresh the original router observation's lifetime.
  if (age === null || age < 0 || lastCheckedAt === null) return empty;
  const nextRetryAt = timestamp(entry.nextRetryAt);
  const resetAt = timestamp(entry.resetAt);
  const retryTimes = [nextRetryAt, resetAt].filter((time): time is string => time !== null && Date.parse(time) > now);
  let state: State = 'unknown';
  // Passive eligibility is never readiness. Stale observations cannot admit work.
  if (age < freshnessMs) {
    if (entry.isActive === false) state = 'disabled';
    else if (auth !== null && ['expired', 'invalid', 'revoked'].includes(auth)) state = 'blocked';
    else if (health !== null && ['error', 'failed', 'unhealthy', 'down'].includes(health)) state = 'blocked';
    else if (quota === 'exhausted' || quota === 'blocked') state = 'exhausted';
    else if (routing === 'blocked' || routing === 'disabled' || routing === 'exhausted') state = routing;
    else if (entry.isActive === true && auth === 'ok' && health === 'healthy' && quota === 'ok' && routing === 'eligible') state = 'eligible';
  }
  return { ...empty, state, source: 'router_snapshot', observed_at: lastCheckedAt,
    validity_until: new Date(Date.parse(lastCheckedAt) + freshnessMs).toISOString(),
    retry_after: age < freshnessMs && retryTimes.length ? retryTimes.sort().at(-1)! : null };
}

function projectLegacyConnection(entry: ObjectValue | undefined, binding: ConnectionBinding, now: number, freshnessMs: number): Observation['connections'][number] {
  const empty = projectConnection(undefined, binding, now, freshnessMs);
  if (!entry) return empty;
  if (entry.provider !== binding.upstreamProvider || (entry.isActive !== undefined && typeof entry.isActive !== 'boolean')) badSchema();
  // 0.5.75 does not have the reference fork's canonical status model. An active
  // configuration and legacy testStatus are not live route/capability evidence.
  if (entry.isActive !== false) return empty;
  // This is the authenticated retrieval time of an explicit disabled config fact,
  // never a timestamp assigned to an old health/capability observation.
  return { ...empty, state: 'disabled', source: 'router_snapshot', observed_at: new Date(now).toISOString(),
    validity_until: new Date(now + freshnessMs).toISOString() };
}

/** Fixed GET snapshot operation. There is deliberately no management refresh operation. */
export function createStatusAdapter(config: Config, options: { fetch?: typeof fetch; now?: () => number; diagnostic?: (code: Failure) => void } = {}) {
  const fixed = validateConfig(config);
  const timeoutMs = boundedInteger(fixed.timeoutMs, 2_000, 30_000);
  const maxBytes = boundedInteger(fixed.maxBytes, 262_144, 1_048_576);
  const freshnessMs = boundedInteger(fixed.freshnessMs, 30_000, 30_000);
  const transport = options.fetch ?? fetch;
  const now = options.now ?? Date.now;
  return {
    async snapshot(routeId: string): Promise<Observation> {
      const route = fixed.routes.find(route => route.routeId === routeId);
      if (!route) throw new Error('unconfigured_route');
      const bindings = fixed.connections.filter(entry => route.connectionRefs.includes(entry.ref));
      const sampledAt = now();
      const result: Observation = {
        schema_version: 1, router_id: fixed.routerId, route_id: route.routeId, policy_revision: route.policyRevision,
        projected_at: new Date(sampledAt).toISOString(),
        route: { state: 'unknown', readiness: 'unknown', reason: 'no_observation', source: 'none', observed_at: null, validity_until: null },
        connections: bindings.map(entry => projectConnection(undefined, entry, sampledAt, freshnessMs)),
        capabilities: { state: 'unknown', source: 'none', observed_at: null, validity_until: null },
      };
      const controller = new AbortController();
      const timeout = setTimeout(() => controller.abort(), timeoutMs);
      try {
        if (![INSPECTED_COMMIT, DEPLOYED_COMMIT].includes(fixed.inspectedCommit)) throw new ProjectionFailure('unsupported_version');
        const response = await transport(new URL(PASSIVE_PATH, fixed.origin), {
          method: 'GET', redirect: 'manual', signal: controller.signal,
          headers: { Authorization: `Bearer ${fixed.managementCredential}`, Accept: 'application/json' },
        });
        if (response.status !== 200) {
          await response.body?.cancel().catch(() => undefined);
          throw new ProjectionFailure('unavailable');
        }
        const payload = await boundedJSON(response, maxBytes);
        const entries = connectionsById(payload);
        const projectedAt = now();
        result.projected_at = new Date(projectedAt).toISOString();
        const project = fixed.inspectedCommit === DEPLOYED_COMMIT ? projectLegacyConnection : projectConnection;
        result.connections = bindings.map(binding => project(entries.get(binding.upstreamId), binding, projectedAt, freshnessMs));
      } catch (error) {
        const code = controller.signal.aborted ? 'unavailable' : error instanceof ProjectionFailure ? error.code : 'unavailable';
        result.route.reason = code === 'unsupported_version' ? 'policy_unverified' : code === 'schema_mismatch' ? 'malformed_observation' : 'router_unreachable';
        // Logging is restricted to a fixed enum. Never forward an error, body or header.
        try { options.diagnostic?.(code); } catch { /* A broken logger must not leak its exception. */ }
      } finally { clearTimeout(timeout); }
      if (!validateObservation(result)) {
        result.route.reason = 'malformed_observation';
        result.connections = bindings.map(entry => projectConnection(undefined, entry, sampledAt, freshnessMs));
        if (!validateObservation(result)) throw new Error('invalid_projection');
      }
      return result;
    },
  };
}
