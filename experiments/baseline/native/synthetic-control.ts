// Loaded only by this experiment's synthetic launcher, never a live deployment.
import assert from 'node:assert/strict';
import { digest, validateNativeProfile } from '../../router-authority-extension/overlay/native-profile.mjs';
import { events } from '../../native-evaluation/fixtures.mjs';
import { settingsDigest } from '../../harness/native/client.ts';
import { validateRegistration } from '../registration.ts';
import { validateDescriptor, executionProjection, PINS, WRITE_PATHS } from '../case01.ts';

export const baselineTrace: { physicalRequests: any[]; registrationDigest: string | null } = { physicalRequests: [], registrationDigest: null };
export function tracePhysical(request_id: string, ordinal: number, scope_id: string, serialized: string) {
  assert(baselineTrace.physicalRequests.length < 32);
  baselineTrace.physicalRequests.push({ request_id, ordinal, scope_id, body: JSON.parse(serialized) });
}
export function baselineManifest(input: unknown, profiles: any[]) {
  const descriptor = validateDescriptor(input); assert.equal(profiles.length, 1);
  const profile = validateNativeProfile(profiles[0]);
  assert.equal(profile.evidence, 'synthetic'); assert.equal(profile.schema, 1); assert.equal(profile.scope.phase, 'baseline');
  assert.equal(profile.scope.caseRef, 'case01'); assert.equal(profile.scope.elapsedMs, 900000); assert.equal(profile.scope.maxInferenceAttempts, 32);
  assert.deepEqual(profile.toolPaths, WRITE_PATHS);
  assert.equal(profile.harness?.settings, settingsDigest('allow'));
  assert.deepEqual(profile.local, {requestBytes:65536,responseBytes:1048576,concurrency:1,requestCount:32,totalMs:120000,firstOutputMs:90000,idleMs:45000,attemptMs:900000});
  return descriptor;
}
export function baselineStart(registration: unknown, descriptor: any, packetDigest: string, policy: any) {
  assert.equal(baselineTrace.registrationDigest, null, 'baseline_already_started');
  const value = validateRegistration(registration);
  assert.equal(digest(executionProjection(value)), descriptor.projectionDigest, 'baseline_registration_changed');
  assert.equal(value.execution.packetDigest, packetDigest); assert.equal(value.execution.nativeProfileDigest, digest(policy.native));
  assert.equal(value.fixture.treeDigest, descriptor.baseTreeDigest);
  assert.deepEqual(value.paths.write, descriptor.writePaths);
  baselineTrace.registrationDigest = digest(value);
  return { schema: 1, registration: value, registrationDigest: baselineTrace.registrationDigest, packetDigest, profileDigest: digest(policy.native) };
}
export function caseEvents(body: any) {
  const scenario = process.env.GAFFER_BASELINE_CASE;
  assert(['two-requests','three-requests'].includes(scenario ?? ''), 'unknown_baseline_scenario');
  const count = body.input.filter((x: any) => x.type === 'function_call_output').length;
  const rounds = scenario === 'two-requests' ? 1 : 2;
  if (count >= rounds) return events(false);
  const selected = rounds === 1 ? [0, 1] : [count];
  const patch = '*** Begin Patch\n' + selected.map(i => '*** Update File: ' + WRITE_PATHS[i] + '\n@@\n-  - uses: ' + (i ? 'actions/setup-node@v4' : 'actions/checkout@v4') + '\n+  - uses: ' + PINS[i] + '\n').join('') + '*** End Patch';
  const replacements: Record<string, string> = { call_synthetic: 'call_case01_' + count, fc_synthetic: 'fc_case01_' + count, rs_synthetic: 'rs_case01_' + count, resp_synthetic_tool: 'resp_case01_' + count };
  const output = JSON.parse(JSON.stringify(events(true), (key, value) => {
    if (typeof value === 'string' && replacements[value]) return replacements[value];
    if (key === 'arguments' || key === 'delta') return typeof value === 'string' && value.includes('patchText') ? JSON.stringify({ patchText: patch }) : value;
    return value;
  }));
  return output;
}
