// Trusted supervisor admission. This module is never staged into the worker.
import { validateRouterPolicy, type RouterPolicy } from '../../inference-boundary/router-policy.ts';
import { validateBinding, type Binding } from '../../inference-boundary/types.ts';
import { assertNativeBinding, type NativeRouterPolicy } from '../../inference-boundary/native-policy.ts';
import { canonical } from '../../inference-boundary/json.ts';
import { PROFILE, digest, settingsDigest } from './client.ts';
import { validateRunRequest, type RunRequest } from './run.ts';

export function prepareRun(policy: RouterPolicy, binding: Binding, input: Omit<RunRequest, 'schema' | 'profile' | 'bindingDigest' | 'settingsDigest'>, now = Date.now()): RunRequest {
  const p = validateRouterPolicy(policy); validateBinding(binding);
  if (p.schema !== 3 || p.native.protocol !== PROFILE || p.routerModel !== 'gpt-6-astra' || canonical(p.native.toolPaths) !== canonical(['greeting.txt'])) throw Error('unsupported_native_worker_policy');
  assertNativeBinding(binding, p as NativeRouterPolicy);
  if (binding.role !== 'worker' || binding.routerId !== p.routerId || binding.routeId !== p.routeId || binding.revision !== p.revision || binding.epoch !== p.epoch || Math.min(binding.expiresAt, binding.leaseExpiresAt) < now + input.limits.wallMs) throw Error('unsupported_native_worker_binding');
  if (input.limits.wallMs > p.limits.attemptMs || input.limits.outputBytes > p.limits.responseBytes || p.native.harness.settings !== settingsDigest(input.approval)) throw Error('native_settings_or_limits_mismatch');
  const request: RunRequest = { ...structuredClone(input), schema: 1, profile: PROFILE, bindingDigest: digest(binding), settingsDigest: p.native.harness.settings };
  validateRunRequest(request); return request;
}
