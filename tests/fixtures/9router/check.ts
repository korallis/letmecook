import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import Ajv from 'ajv';
import addFormats from 'ajv-formats';

// This checks a declarative specification corpus. It deliberately does not
// implement the status adapter, forwarding boundary or stream state machine.
const directory = dirname(fileURLToPath(import.meta.url));
const readJSON = async (name: string) => JSON.parse(await readFile(join(directory, name), 'utf8'));
const [schema, statuses, examples, failures, profiles, deployment, limitsSchema, nativeExample] = await Promise.all([
  readJSON('status.schema.json'), readJSON('status-cases.json'),
  readJSON('examples.json'), readJSON('failure-cases.json'),
  readJSON('input-profiles.json'), readJSON('../../../docs/contracts/9router-deployment-2026-09-14.json'),
  readJSON('limits-profile.schema.json'), readJSON('native-limits-example.json'),
]);
const ajv = new Ajv({ allErrors: true, strict: true, strictRequired: false, allowUnionTypes: true });
addFormats(ajv);
const validate = ajv.compile(schema);
const validateLimitsProfile = ajv.compile(limitsSchema);
const ids = new Set<string>();
const profileIds = new Set(profiles.profiles.map((profile: any) => profile.id));
let mutationCount = 0;

function rejectMutation(original: unknown, change: (value: any) => void, label: string) {
  const mutated = structuredClone(original);
  change(mutated);
  assert.equal(validate(mutated), false, `schema accepted ${label}`);
  mutationCount++;
}

for (const fixture of statuses.cases) {
  assert.equal(fixture.evidence, 'synthetic');
  assert(!ids.has(fixture.id), `duplicate fixture ${fixture.id}`);
  ids.add(fixture.id);
  const expected = fixture.expected;
  assert(profileIds.has(fixture.context.adapter_profile), `${fixture.id}: missing input profile`);
  assert(validate(expected), `${fixture.id}: ${ajv.errorsText(validate.errors)}`);
  const encoded = JSON.stringify(expected);
  assert(!/SENTINEL|raw-connection|fixture-provider|@|providerSpecificData|apiKey|accessToken|refreshToken|reasonDetail/.test(encoded), `${fixture.id}: unsafe expected output`);
  assert.deepEqual(expected.connections.map((connection: any) => connection.connection_ref).sort(),
    fixture.context.connections.map((mapping: any) => mapping.connection_ref).sort(), `${fixture.id}: missing configured connection`);
  const now = Date.parse(fixture.context.now);
  assert.equal(Date.parse(expected.projected_at), now);
  for (const observation of [expected.route, ...expected.connections, expected.capabilities]) {
    if (observation.observed_at !== null) {
      assert(Date.parse(observation.observed_at) <= now, `${fixture.id}: future observation`);
      if (observation.validity_until !== null) {
        assert(Date.parse(observation.validity_until) > Date.parse(observation.observed_at));
      }
    }
  }
  if (expected.route.readiness === 'ready') {
    assert.equal(fixture.context.deployment_accepted, true);
    assert.equal(fixture.context.policy_epoch_active, true);
    assert.equal(fixture.context.probe.result, 'completed');
    assert.equal(fixture.context.probe.policy_revision, expected.policy_revision);
    assert.equal(expected.capabilities.state, 'verified');
    assert(Date.parse(expected.route.validity_until) > now);
  }
  rejectMutation(expected, value => { value.apiKey = 'UPSTREAM_SECRET_SENTINEL'; }, `${fixture.id}: extra root key`);
  rejectMutation(expected, value => { value.route.raw = { token: 'UPSTREAM_SECRET_SENTINEL' }; }, `${fixture.id}: extra route object`);
  rejectMutation(expected, value => { value.connections[0].providerSpecificData = { copilotToken: 'UPSTREAM_SECRET_SENTINEL' }; }, `${fixture.id}: nested credentials`);
  rejectMutation(expected, value => { value.connections[0].state = 'super_ready'; }, `${fixture.id}: unknown enum`);
  rejectMutation(expected, value => { value.connections[0].retry_after = 'tomorrow'; }, `${fixture.id}: invalid timestamp`);
  rejectMutation(expected, value => { value.capabilities.raw = { key: 'UPSTREAM_SECRET_SENTINEL' }; }, `${fixture.id}: extra capability object`);
  rejectMutation(expected, value => { value.route.readiness = 'ready'; value.capabilities.state = 'unknown'; }, `${fixture.id}: ready without proof`);
}

const selectedProfile = profiles.profiles.find((profile: any) => profile.selected_deployment);
assert.equal(deployment.evidence_kind, 'read_only_deployment_inspection');
assert.equal(deployment.host_selection, 'optional_operator_configuration');
assert.equal(deployment.package.version, selectedProfile.package_version);
assert.equal(deployment.source.commit, selectedProfile.source_commit);
assert.equal(deployment.package.integrity, selectedProfile.package_integrity);
assert.equal(deployment.package.changed_files, 0);
assert.equal(deployment.package.missing_files, 0);
assert.equal(deployment.runtime_conformance.authenticated_inference, 'not_run');
assert(!/matilda|100\.75\.|\/Users\/|\/home\/|\/data\//i.test(JSON.stringify(deployment)), 'public deployment record contains operator host/address/path');

assert.equal(examples.evidence, 'synthetic');
assert.equal(examples.deployment.deployed_commit, null);
assert.equal(examples.deployment.deployed_artifact_digest, null);
assert.equal(examples.policy.ready_for_deployment, false);
assert.equal(examples.policy.limits.boundary_retries, 0);
assert.equal(examples.policy.limits_profile.kind, 'strict-provider-output-v1');
assert.equal(examples.policy.limits_profile.providerOutput.maxTokens, examples.policy.limits.output_tokens);
// These assertions validate illustrative documents only. They cannot resolve
// authority references, inspect a reachable graph or admit runtime requests.
for (const example of [examples, nativeExample]) {
  assert.equal(example.evidence, 'synthetic');
  assert.equal(example.policy.ready_for_deployment, false);
  assert.equal(example.policy.policy_write_mode, 'freeze_and_drain');
  assert(validateLimitsProfile(example.policy.limits_profile), ajv.errorsText(validateLimitsProfile.errors));
  for (const key of ['router_id', 'route_id', 'router_build_ref', 'graph_fingerprint', 'policy_revision',
    'epoch', 'protocol_harness_settings_ref', 'capability_evidence_ref', 'authorization_policy_ref', 'limits_profile']) {
    assert.notEqual(example.policy[key], undefined, `missing policy identity ${key}`);
    assert.deepEqual(example.attempt_binding[key], example.policy[key], `mismatched grant identity ${key}`);
  }
  assert.match(example.policy.graph_fingerprint, /^[a-f0-9]{64}$/);
  for (const key of ['request_bytes', 'response_bytes', 'in_flight', 'request_count', 'inference_subattempt_count',
    'total_ms', 'first_output_ms', 'semantic_idle_ms', 'attempt_ms']) {
    assert(Number.isSafeInteger(example.policy.limits[key]) && example.policy.limits[key] > 0, `invalid local limit ${key}`);
  }
  for (const key of ['router_retries_per_request', 'harness_retries_per_attempt', 'boundary_retries']) {
    assert(Number.isSafeInteger(example.policy.limits[key]) && example.policy.limits[key] >= 0, `invalid retry limit ${key}`);
  }
  assert.equal(example.policy.limits.boundary_retries, 0);
  assert(example.policy.limits.first_output_ms <= example.policy.limits.total_ms);
  assert(example.policy.limits.semantic_idle_ms <= example.policy.limits.total_ms);
  assert(example.policy.limits.total_ms <= example.policy.limits.attempt_ms);
}
assert.equal(nativeExample.policy.limits_profile.kind, 'native-subscription-local-v1');
assert.equal(nativeExample.policy.limits_profile.authorizationPolicyRef, nativeExample.policy.authorization_policy_ref);
assert.equal(nativeExample.policy.limits_profile.subscriptionEnvelopeRef, nativeExample.policy.subscription_envelope.ref);
assert.deepEqual(nativeExample.policy.task_requirements, { hard_provider_output_bound: false, hard_provider_monetary_cap: false });
assert(!Object.hasOwn(nativeExample.policy.limits, 'output_tokens'));
assert(nativeExample.policy.subscription_envelope.all_reachable_connections_and_fallbacks.length > 0);
for (const edge of nativeExample.policy.subscription_envelope.all_reachable_connections_and_fallbacks) {
  assert.equal(edge.billing, 'subscription');
  for (const key of ['connection_ref', 'model_ref', 'classification_ref', 'compatibility_evidence_ref']) {
    assert.match(edge[key], /^[A-Za-z][A-Za-z0-9_-]{0,127}$/);
  }
}
assert.equal(nativeExample.request.method, 'POST');
assert.equal(nativeExample.request.path, '/v1/responses');
assert.equal(nativeExample.request.body.model, nativeExample.policy.router_model);
assert.equal(nativeExample.request.body.stream, true);
for (const key of ['max_tokens', 'max_completion_tokens', 'max_output_tokens']) assert(!Object.hasOwn(nativeExample.request.body, key));

let limitsMutationCount = 0;
function rejectLimitsMutation(original: unknown, change: (value: any) => void, label: string) {
  const mutated = structuredClone(original);
  change(mutated);
  assert.equal(validateLimitsProfile(mutated), false, `limits schema accepted ${label}`);
  limitsMutationCount++;
}
const strictProfile = examples.policy.limits_profile;
const nativeProfile = nativeExample.policy.limits_profile;
for (const cap of [null, 0, -1, 1.5, '4096']) {
  rejectLimitsMutation(strictProfile, value => { value.providerOutput.maxTokens = cap; }, `invalid strict cap ${cap}`);
}
rejectLimitsMutation(strictProfile, value => { delete value.providerOutput.maxTokens; }, 'missing strict cap');
rejectLimitsMutation(strictProfile, value => { value.kind = 'native-subscription-local-v1'; }, 'implicit downgrade');
rejectLimitsMutation(strictProfile, value => { delete value.kind; }, 'missing discriminator in a new logical profile');
for (const key of ['authorizationPolicyRef', 'subscriptionEnvelopeRef']) {
  rejectLimitsMutation(nativeProfile, value => { delete value[key]; }, `missing native ${key}`);
  rejectLimitsMutation(nativeProfile, value => { value[key] = ''; }, `empty native ${key}`);
}
for (const key of ['providerOutput', 'providerMonetaryCap']) {
  rejectLimitsMutation(nativeProfile, value => { value[key].requirement = 'hard'; }, `native hard ${key}`);
  rejectLimitsMutation(nativeProfile, value => { value[key].capability = 'verified'; }, `native claims ${key}`);
  rejectLimitsMutation(nativeProfile, value => { delete value[key]; }, `missing unavailable ${key}`);
}
rejectLimitsMutation(nativeProfile, value => { value.providerOutput.maxTokens = 4096; }, 'native disguised cap');
rejectLimitsMutation(nativeProfile, value => { value.kind = 'unlimited'; }, 'unknown limits profile');

const requestPaths = new Set(['/v1/chat/completions', '/v1/messages', '/v1/responses']);
for (const request of examples.requests) {
  assert.equal(request.method, 'POST');
  assert(requestPaths.has(request.path));
  assert.equal(request.body.model, examples.policy.router_model);
  assert.equal(request.body.stream, true);
  const cap = request.body.max_completion_tokens ?? request.body.max_tokens ?? request.body.max_output_tokens;
  assert(Number.isInteger(cap) && cap > 0 && cap <= examples.policy.limits.output_tokens);
}

assert.equal(failures.evidence, 'synthetic_specification');
const layers = new Set(['boundary', 'deployed_router', 'protocol', 'harness', 'policy', 'containment', 'status']);
let streamCount = 0;
for (const fixture of failures.cases) {
  assert(!ids.has(fixture.id), `duplicate fixture ${fixture.id}`);
  ids.add(fixture.id);
  assert(layers.has(fixture.layer));
  assert.equal(typeof fixture.input, 'string');
  assert(fixture.expect.length > 0 && fixture.expect.every((item: unknown) => typeof item === 'string'));
  if (fixture.stream) {
    const path = resolve(directory, fixture.stream);
    assert(path.startsWith(resolve(directory, 'streams') + '/'));
    const body = await readFile(path, 'utf8');
    assert(body.startsWith('data: ') || body.startsWith('event: '));
    for (const line of body.split('\n').filter(line => line.startsWith('data: ') && line !== 'data: [DONE]')) {
      JSON.parse(line.slice(6));
    }
    streamCount++;
  }
}
console.log(`Contract corpus verified: ${statuses.cases.length} projections, ${mutationCount} rejected status-schema mutations, 2 limits-profile examples, ${limitsMutationCount} rejected limits-schema mutations, ${examples.requests.length + 1} request examples, ${failures.cases.length} fault specifications, ${streamCount} SSE fixtures. No runtime admission or live conformance was run.`);
