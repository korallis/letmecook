import { createHash } from 'node:crypto';
import { canonical, keys } from './json.ts';
import { validateRouterPolicy, type RouterPolicy } from './router-policy.ts';

export const CHAT_PATH = '/v1/chat/completions';
export const TOOL = {
  type: 'function', function: {
    name: 'read_file', description: 'Read one authorized fixture path',
    parameters: { type: 'object', properties: { path: { type: 'string', enum: ['fixture.txt'] } }, required: ['path'], additionalProperties: false },
  },
} as const;
export interface Limits {
  requestBytes: number; responseBytes: number; outputTokens: number; concurrency: number;
  requestCount: number; totalMs: number; firstOutputMs: number; idleMs: number; attemptMs: number;
}
export interface FixturePolicy {
  schema: 1; routerId: string; routeId: string; revision: string; epoch: number;
  routerModel: string; profile: 'chat-text-tools-v1';
  // This closed graph is deliberately a synthetic fixture, not a live-deployment attestation.
  graph: { evidence: 'synthetic'; provider: 'fixture_provider'; model: 'fixture_model'; billing: 'subscription'; fusion: false; capabilityAdapters: false; remoteResources: false; compressionHelpers: false };
  limits: Limits;
}
export type Policy = FixturePolicy | RouterPolicy;
export interface Binding {
  attemptId: string; grantId: string; taskId: string; leaseId: string; fence: number;
  role: 'worker' | 'planner' | 'reviewer'; routerId: string; routeId: string;
  revision: string; epoch: number; expiresAt: number; leaseExpiresAt: number;
}
export type Outcome = 'completed' | 'failed_before_output' | 'partial_failure' | 'cancelled_unknown';
export type Reason = 'unauthorized' | 'policy_denied' | 'unsupported_request' | 'request_limit' | 'response_limit' |
  'concurrency_limit' | 'attempt_limit' | 'deadline' | 'cancelled' | 'upstream_failure' | 'invalid_stream' | 'boundary_closed' | 'receipt_unverified' | 'replacement_read_only';
export class Denial extends Error {
  readonly reason: Reason;
  readonly status: number;
  constructor(reason: Reason, status = 403) { super(reason); this.reason = reason; this.status = status; }
}
export const ref = (value: unknown): value is string => typeof value === 'string' && /^[a-z][a-z0-9_-]{0,63}$/.test(value);
export const fingerprint = (value: Policy) => createHash('sha256').update(canonical(value)).digest('hex');
export function validatePolicy(policy: Policy): Policy {
  if (policy?.schema === 2) return validateRouterPolicy(policy);
  keys(policy, ['schema', 'routerId', 'routeId', 'revision', 'epoch', 'routerModel', 'profile', 'graph', 'limits'], ['schema', 'routerId', 'routeId', 'revision', 'epoch', 'routerModel', 'profile', 'graph', 'limits']);
  if (policy.schema !== 1 || ![policy.routerId, policy.routeId, policy.revision].every(ref) || !Number.isSafeInteger(policy.epoch) || policy.epoch < 1 || !/^[a-zA-Z0-9_/-]{1,128}$/.test(policy.routerModel) || policy.profile !== 'chat-text-tools-v1') throw new Denial('policy_denied');
  const graph = { evidence: 'synthetic', provider: 'fixture_provider', model: 'fixture_model', billing: 'subscription', fusion: false, capabilityAdapters: false, remoteResources: false, compressionHelpers: false };
  if (canonical(policy.graph) !== canonical(graph)) throw new Denial('policy_denied');
  const maximum: Limits = { requestBytes: 1048576, responseBytes: 8388608, outputTokens: 4096, concurrency: 4, requestCount: 32, totalMs: 120000, firstOutputMs: 30000, idleMs: 15000, attemptMs: 600000 };
  keys(policy.limits, Object.keys(maximum), Object.keys(maximum));
  for (const key of Object.keys(maximum) as (keyof Limits)[]) {
    if (!Number.isSafeInteger(policy.limits[key]) || policy.limits[key] < 1 || policy.limits[key] > maximum[key]) throw new Denial('policy_denied');
  }
  return structuredClone(policy);
}
export function validateBinding(binding: Binding) {
  keys(binding, ['attemptId', 'grantId', 'taskId', 'leaseId', 'fence', 'role', 'routerId', 'routeId', 'revision', 'epoch', 'expiresAt', 'leaseExpiresAt'], ['attemptId', 'grantId', 'taskId', 'leaseId', 'fence', 'role', 'routerId', 'routeId', 'revision', 'epoch', 'expiresAt', 'leaseExpiresAt']);
  if (![binding.attemptId, binding.grantId, binding.taskId, binding.leaseId, binding.routerId, binding.routeId, binding.revision].every(ref) || !['worker', 'planner', 'reviewer'].includes(binding.role) || ![binding.fence, binding.epoch, binding.expiresAt, binding.leaseExpiresAt].every(n => Number.isSafeInteger(n) && n > 0)) throw new Denial('policy_denied');
}
export function fixturePolicy(overrides: Partial<Limits> = {}): FixturePolicy {
  return { schema: 1, routerId: 'fixture_router', routeId: 'coding', revision: 'policy_1', epoch: 1, routerModel: 'gaffer-coding', profile: 'chat-text-tools-v1',
    graph: { evidence: 'synthetic', provider: 'fixture_provider', model: 'fixture_model', billing: 'subscription', fusion: false, capabilityAdapters: false, remoteResources: false, compressionHelpers: false },
    limits: { requestBytes: 65536, responseBytes: 65536, outputTokens: 256, concurrency: 1, requestCount: 8, totalMs: 2000, firstOutputMs: 1000, idleMs: 500, attemptMs: 10000, ...overrides } };
}
