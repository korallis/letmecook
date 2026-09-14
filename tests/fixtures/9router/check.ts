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
const [schema, statuses, examples, failures] = await Promise.all([
  readJSON('status.schema.json'), readJSON('status-cases.json'),
  readJSON('examples.json'), readJSON('failure-cases.json'),
]);
const ajv = new Ajv({ allErrors: true, strict: true, strictRequired: false, allowUnionTypes: true });
addFormats(ajv);
const validate = ajv.compile(schema);
const ids = new Set<string>();
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

assert.equal(examples.evidence, 'synthetic');
assert.equal(examples.deployment.deployed_commit, null);
assert.equal(examples.deployment.deployed_artifact_digest, null);
assert.equal(examples.policy.ready_for_deployment, false);
assert.equal(examples.policy.limits.boundary_retries, 0);
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
console.log(`Contract corpus verified: ${statuses.cases.length} projections, ${mutationCount} rejected schema mutations, ${examples.requests.length} request examples, ${failures.cases.length} fault specifications, ${streamCount} SSE fixtures. No live conformance was run.`);
