/** Checks the committed baseline design records; does not authenticate evidence or run a harness. */
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

type Row = Record<string, unknown>;
function record(value: unknown): asserts value is Row {
  assert(value !== null && typeof value === 'object' && !Array.isArray(value), 'expected record');
}
function rows(value: unknown): asserts value is Row[] {
  assert(Array.isArray(value), 'expected record array');
  value.forEach(record);
}
function text(value: unknown) {
  assert(typeof value === 'string' && value.trim().length > 0, 'required text missing');
}
function textList(value: unknown) {
  assert(Array.isArray(value) && value.length > 0, 'required text list missing');
  value.forEach(text);
}
function uniqueIds(values: Row[]) {
  values.forEach(value => text(value.id));
  assert.equal(new Set(values.map(value => value.id)).size, values.length, 'duplicate fixture identity');
}
function validate(intake: unknown, development: unknown, template: unknown) {
  for (const value of [intake, development, template]) { record(value); assert.equal(value.contractVersion, 1); }
  record(intake); record(development); record(template);
  record(intake.privateManifest);
  assert.match(String(intake.privateManifest.sha256), /^[a-f0-9]{64}$/, 'private manifest digest missing');
  rows(intake.cases); assert.equal(intake.cases.length, 3); uniqueIds(intake.cases);
  for (const candidate of intake.cases) {
    assert.equal(candidate.origin, 'operator');
    assert.equal(candidate.selectionProvenance, 'agent-proposed-under-user-delegation');
    assert.equal(candidate.partition, 'development');
    assert.equal(candidate.registrationStatus, 'not-registered', 'frozen registration needs a separate reviewed fixture version');
    assert.equal(candidate.operatorLabel, null, 'proposed cases must not acquire fabricated human labels');
    assert.equal(candidate.feasibility, 'needs-clarification');
    for (const field of ['capture', 'taskClass', 'intendedOutcome', 'scope', 'expectedResponse']) text(candidate[field]);
    textList(candidate.acceptanceCriteria); textList(candidate.requiredChecks);
    record(candidate.repository); assert.equal(candidate.repository.startingCommit, null);
    text(candidate.repository.startingCommitRef);
    record(candidate.dataPermissions); text(candidate.dataPermissions.modelExposure);
    textList(candidate.dataPermissions.prohibitedPublication);
  }
  assert.equal(development.origin, 'synthetic');
  assert.equal(development.partition, 'development');
  assert.equal(development.eligibleForMeasurement, false, 'synthetic examples cannot enter measured cohort');
  rows(development.examples); assert.equal(development.examples.length, 6); uniqueIds(development.examples);
  for (const example of development.examples) {
    assert.equal(example.operatorLabel, null);
    assert(['feasible', 'infeasible', 'needs-clarification'].includes(String(example.feasibility)));
    text(example.assumptions); text(example.expectedResponse); textList(example.acceptanceCriteria);
  }
  assert.equal(template.recordKind, 'template-not-an-observation');
  assert.equal(template.status, 'blocked'); textList(template.blockers);
  for (const field of ['runId', 'fixtureId', 'registeredAt', 'startedAt', 'endedAt', 'elapsedSeconds', 'waitingSeconds']) {
    assert.equal(template[field], null, 'unstarted template cannot contain run observations');
  }
  for (const field of ['operatorIntervals', 'attempts', 'checks', 'rawObservations', 'usage', 'defects', 'budgetOverruns']) {
    assert.deepEqual(template[field], [], 'template contains observations');
  }
  record(template.acceptance); assert.equal(template.acceptance.decision, null);
  assert.equal(template.acceptance.operatorId, null); assert.deepEqual(template.acceptance.criteria, []);
  record(template.followUp); assert.equal(template.followUp.completedAt, null);
}

const load = (name: string): unknown => JSON.parse(readFileSync(new URL(`${name}.json`, import.meta.url), 'utf8'));
const inputs = [load('operator-intake'), load('development-examples'), load('run-template')] as const;
validate(...inputs);
// Ensure the checker refuses the mistakes that would manufacture measurement evidence.
for (const [index, field, value] of [
  [1, 'eligibleForMeasurement', true], [1, 'partition', 'held-out'],
  [2, 'status', 'accepted'], [2, 'elapsedSeconds', 0], [2, 'rawObservations', [{ id: 'invented' }]],
] as const) {
  const mutated = structuredClone(inputs);
  record(mutated[index]); mutated[index][field] = value;
  assert.throws(() => validate(...mutated), 'invalid evidence claim was accepted');
}
console.log('Baseline design records verified: 3 unregistered candidates, 6 excluded synthetic examples, unstarted template; 5 invalid claims rejected. No live evidence or human labels validated.');
