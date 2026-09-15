/** Checks the committed baseline design records; does not authenticate evidence or run a harness. */
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
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
function keys(value: Row, expected: string[]) {
  assert.deepEqual(Object.keys(value).sort(), [...expected].sort(), 'unexpected or missing record fields');
}
function digest(value: unknown) {
  assert(typeof value === 'string'); assert.match(value, /^[a-f0-9]{64}$/, 'evidence digest missing');
}
const observationTimeMeaning = 'time the assistant observed the explicit approval; not an asserted message timestamp';
function validateCases(intake: Row, approved: boolean) {
  record(intake.privateManifest); digest(intake.privateManifest.sha256);
  text(intake.privateManifest.reference);
  rows(intake.cases); assert.equal(intake.cases.length, 3); uniqueIds(intake.cases);
  assert.deepEqual(intake.cases.map(candidate => candidate.id), ['operator-01', 'operator-02', 'operator-03']);
  for (const candidate of intake.cases) {
    keys(candidate, ['id', 'taskClass', 'intendedOutcome', 'scope', 'acceptanceCriteria', 'requiredChecks', 'feasibilityReason',
      'expectedResponse', 'origin', 'selectionProvenance', 'partition', 'capture', 'repository', 'dataPermissions',
      'feasibility', 'operatorLabel', 'registrationStatus']);
    assert.equal(candidate.origin, 'operator');
    assert.equal(candidate.selectionProvenance, 'agent-proposed-under-user-delegation');
    assert.equal(candidate.partition, 'development');
    assert.equal(candidate.registrationStatus, 'not-registered', 'frozen registration needs a separate reviewed fixture version');
    if (!approved) assert.equal(candidate.operatorLabel, null, 'historical proposals must retain null human labels');
    assert.equal(candidate.feasibility, 'needs-clarification');
    for (const field of ['capture', 'taskClass', 'intendedOutcome', 'scope', 'expectedResponse', 'feasibilityReason']) text(candidate[field]);
    textList(candidate.acceptanceCriteria); textList(candidate.requiredChecks);
    record(candidate.repository); assert.equal(candidate.repository.startingCommit, null);
    text(candidate.repository.startingCommitRef);
    record(candidate.dataPermissions);
    if (!approved) text(candidate.dataPermissions.modelExposure);
    textList(candidate.dataPermissions.prohibitedPublication);
  }
}
function validate(intake: unknown, development: unknown, template: unknown, historyBytes: string) {
  const historical: unknown = JSON.parse(historyBytes);
  for (const value of [intake, historical, development, template]) { record(value); assert.equal(value.contractVersion, 1); }
  record(intake); record(historical); record(development); record(template);
  keys(intake, ['contractVersion', 'revision', 'status', 'recordedAt', 'recordedAtMeaning', 'source', 'privateManifest',
    'previousRevision', 'authority', 'liveTrialAuthorization', 'missingInputs', 'cases']);
  assert.equal(intake.revision, 2);
  assert.equal(intake.status, 'intended-outcomes-approved-awaiting-registration');
  text(intake.source); textList(intake.missingInputs);
  record(intake.previousRevision);
  keys(intake.previousRevision, ['path', 'sha256', 'status']);
  assert.equal(intake.previousRevision.path, 'history/operator-intake-20260914-proposed.json');
  assert.equal(intake.previousRevision.status, 'historical-proposal-before-explicit-approval');
  assert.equal(intake.previousRevision.sha256, createHash('sha256').update(historyBytes).digest('hex'), 'historical proposal changed');
  assert.equal(historical.status, 'candidates-prepared-awaiting-registration');
  validateCases(historical, false); validateCases(intake, true);
  assert.deepEqual(intake.privateManifest, historical.privateManifest, 'private fixture mapping must remain opaque and unchanged');
  record(intake.authority);
  const authority = intake.authority;
  keys(authority, ['reference', 'sha256', 'sourceKind', 'operatorId', 'recordedAt', 'recordedAtMeaning', 'messageTimestamp',
    'intendedOutcomesApproved', 'completedOutputsAccepted', 'operatorMinutesObserved', 'access']);
  assert.equal(authority.reference, 'operator-owned:baseline-approvals-20260914-v1'); digest(authority.sha256);
  assert.equal(authority.sourceKind, 'explicit-user-message');
  assert.equal(authority.operatorId, 'session-user');
  assert.equal(authority.recordedAt, '2026-09-14T18:58:37Z');
  assert.equal(authority.recordedAtMeaning, observationTimeMeaning);
  assert.equal(authority.messageTimestamp, null, 'observation time is not a known message timestamp');
  assert.equal(intake.recordedAt, authority.recordedAt); assert.equal(intake.recordedAtMeaning, observationTimeMeaning);
  assert.equal(authority.intendedOutcomesApproved, true);
  assert.equal(authority.completedOutputsAccepted, false, 'intended-outcome approval is not completed-output acceptance');
  assert.equal(authority.operatorMinutesObserved, false, 'approval does not manufacture operator minutes'); text(authority.access);
  record(intake.liveTrialAuthorization);
  assert.deepEqual(intake.liveTrialAuthorization, {
    status: 'approved-pending-controls-review', authorityRef: authority.reference, executionStatus: 'blocked',
    controlsReviewStatus: 'pending', approvedProfile: 'native-subscription-local-v1', strictDefaultRetained: true,
    deployment: 'isolated local copy of existing 9Router setup', model: 'Astra', effort: 'xhigh',
    providers: 'existing Codex subscriptions only', paidApiFallback: false,
    initialPublicChecks: { maximumInferenceAttempts: 10, maximumElapsedMs: 600000, scope: 'aggregate' },
    perBaselineCase: { maximumInferenceAttempts: 32, maximumElapsedMs: 900000, scope: 'per-case' },
    retriesCount: true, concurrentCases: 1, providerOutputCap: 'unavailable', providerMonetaryCap: 'unavailable',
    unknownRemoteWorkBlocksReplacement: true,
    preconditions: [
      'Approved native profile and deployment controls pass review.',
      'Pinned harness/router route and execution/isolation profile pass preflight.',
      'Register exact private fixture revisions, runnable prerequisites, local limit enforcement and stop mechanism before dispatch.',
    ],
  }, 'conditional trial declaration differs from recorded approval; no runtime eligibility follows from this record');
  rows(intake.cases); rows(historical.cases);
  for (const [index, candidate] of intake.cases.entries()) {
    const previous: Row = historical.cases[index];
    for (const field of ['capture', 'taskClass', 'intendedOutcome', 'scope', 'expectedResponse', 'acceptanceCriteria', 'requiredChecks', 'repository']) {
      assert.deepEqual(candidate[field], previous[field], 'approval must refer to the prepared brief without silently changing its scope');
    }
    assert.deepEqual(candidate.operatorLabel, {
      kind: 'intended-outcome-approval', decision: 'approved', operatorId: authority.operatorId,
      sourceRef: `${authority.reference}#${candidate.id}`, recordedAt: authority.recordedAt,
      recordedAtMeaning: observationTimeMeaning, messageTimestamp: null, approvedOutcome: candidate.intendedOutcome,
    }, 'case approval requires a matching outcome and private authority reference; it is not final acceptance');
    record(candidate.dataPermissions); record(previous.dataPermissions);
    assert.deepEqual(candidate.dataPermissions, {
      ...previous.dataPermissions,
      modelExposure: {
        status: 'approved-pending-controls-review', authorityRef: authority.reference,
        allowedData: ['necessary source', 'necessary diffs'], excludedData: ['secrets', 'production/user data'],
        route: '9Router', providers: 'existing Codex subscriptions only', paidApiFallback: false,
      },
    }, 'model exposure approval must retain route, excluded-data and publication limits');
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
  assert.deepEqual(template.acceptance, {
    decision: null, operatorId: null, labelledAt: null, criteria: [], scopeBreach: null, artifactRef: null, reason: null,
  }, 'template contains acceptance observations');
  assert.deepEqual(template.followUp, {
    startedAt: null, dueAt: null, completedAt: null, evidenceRef: null,
  }, 'template contains follow-up observations');
}

const load = (name: string): unknown => JSON.parse(readFileSync(new URL(`${name}.json`, import.meta.url), 'utf8'));
const inputs = [load('operator-intake'), load('development-examples'), load('run-template'),
  readFileSync(new URL('history/operator-intake-20260914-proposed.json', import.meta.url), 'utf8')] as const;
validate(...inputs);
let rejected = 0;
// Exercise declarations as data, including conditional authority and unchanged case scope.
const invalidApprovalClaims: { name: string; path: (string | number)[]; value: unknown }[] = [
  { name: 'premature intake registration', path: ['status'], value: 'registered' },
  { name: 'missing blockers', path: ['missingInputs'], value: [] },
  { name: 'extra result observations', path: ['rawObservations'], value: [{ id: 'invented' }] },
  { name: 'broken history digest', path: ['previousRevision', 'sha256'], value: '0'.repeat(64) },
  { name: 'missing authority digest', path: ['authority', 'sha256'], value: null },
  { name: 'missing private authority reference', path: ['authority', 'reference'], value: '' },
  { name: 'agent substituted for human source', path: ['authority', 'sourceKind'], value: 'agent-proposal' },
  { name: 'completed output acceptance', path: ['authority', 'completedOutputsAccepted'], value: true },
  { name: 'invented operator minutes', path: ['authority', 'operatorMinutesObserved'], value: true },
  { name: 'invented message timestamp', path: ['authority', 'messageTimestamp'], value: '2026-09-14T18:58:37Z' },
  { name: 'incorrect timestamp meaning', path: ['recordedAtMeaning'], value: 'user message timestamp' },
  { name: 'premature execution readiness', path: ['liveTrialAuthorization', 'executionStatus'], value: 'ready' },
  { name: 'invented controls review', path: ['liveTrialAuthorization', 'controlsReviewStatus'], value: 'passed' },
  { name: 'strict default weakened', path: ['liveTrialAuthorization', 'strictDefaultRetained'], value: false },
  { name: 'omitted preflight', path: ['liveTrialAuthorization', 'preconditions'], value: [] },
  { name: 'paid fallback expansion', path: ['liveTrialAuthorization', 'paidApiFallback'], value: true },
  { name: 'concurrent cases expansion', path: ['liveTrialAuthorization', 'concurrentCases'], value: 2 },
  { name: 'retries excluded from limits', path: ['liveTrialAuthorization', 'retriesCount'], value: false },
  { name: 'initial attempt limit expansion', path: ['liveTrialAuthorization', 'initialPublicChecks', 'maximumInferenceAttempts'], value: 11 },
  { name: 'initial time limit expansion', path: ['liveTrialAuthorization', 'initialPublicChecks', 'maximumElapsedMs'], value: 600001 },
  { name: 'initial limits applied per case', path: ['liveTrialAuthorization', 'initialPublicChecks', 'scope'], value: 'per-case' },
  { name: 'baseline attempt limit expansion', path: ['liveTrialAuthorization', 'perBaselineCase', 'maximumInferenceAttempts'], value: 33 },
  { name: 'baseline time limit expansion', path: ['liveTrialAuthorization', 'perBaselineCase', 'maximumElapsedMs'], value: 900001 },
  { name: 'fabricated provider output cap', path: ['liveTrialAuthorization', 'providerOutputCap'], value: 4096 },
  { name: 'fabricated provider monetary cap', path: ['liveTrialAuthorization', 'providerMonetaryCap'], value: 0 },
  { name: 'replacement despite unknown remote work', path: ['liveTrialAuthorization', 'unknownRemoteWorkBlocksReplacement'], value: false },
  { name: 'missing approved label', path: ['cases', 0, 'operatorLabel'], value: null },
  { name: 'mismatched case authority', path: ['cases', 0, 'operatorLabel', 'sourceRef'], value: 'operator-owned:baseline-approvals-20260914-v1#operator-02' },
  { name: 'mismatched approved outcome', path: ['cases', 0, 'operatorLabel', 'approvedOutcome'], value: 'Broader dependency update' },
  { name: 'invented label message time', path: ['cases', 0, 'operatorLabel', 'messageTimestamp'], value: '2026-09-14T18:58:37Z' },
  { name: 'approval treated as artifact acceptance', path: ['cases', 0, 'operatorLabel', 'kind'], value: 'completed-output-acceptance' },
  { name: 'new unapproved acceptance criterion', path: ['cases', 0, 'acceptanceCriteria'], value: ['All updates accepted'] },
  { name: 'native prerequisite removed', path: ['cases', 1, 'requiredChecks'], value: ['Linux-only tests'] },
  { name: 'major migration scope expansion', path: ['cases', 2, 'scope'], value: 'Implement all major tooling migrations' },
  { name: 'premature case registration', path: ['cases', 0, 'registrationStatus'], value: 'registered' },
  { name: 'premature execution eligibility', path: ['cases', 0, 'feasibility'], value: 'feasible' },
  { name: 'private starting SHA disclosed', path: ['cases', 0, 'repository', 'startingCommit'], value: '1'.repeat(40) },
  { name: 'source exposure scope expansion', path: ['cases', 0, 'dataPermissions', 'modelExposure', 'allowedData'], value: ['entire private repository'] },
  { name: 'excluded data removed', path: ['cases', 0, 'dataPermissions', 'modelExposure', 'excludedData'], value: [] },
  { name: 'direct provider route', path: ['cases', 0, 'dataPermissions', 'modelExposure', 'route'], value: 'provider-direct' },
  { name: 'provider account expansion', path: ['cases', 0, 'dataPermissions', 'modelExposure', 'providers'], value: 'any provider' },
  { name: 'publication boundary removed', path: ['cases', 0, 'dataPermissions', 'prohibitedPublication'], value: [] },
  { name: 'new external effects', path: ['cases', 0, 'dataPermissions', 'externalEffects'], value: 'Merge and deploy' },
];
for (const { name, path, value } of invalidApprovalClaims) {
  const mutated = structuredClone(inputs);
  let target: unknown = mutated[0];
  for (const key of path.slice(0, -1)) {
    if (Array.isArray(target)) target = target[Number(key)];
    else { record(target); target = target[key]; }
  }
  record(target); target[path.at(-1)!] = value;
  assert.throws(() => validate(...mutated), name); rejected++;
}
const alteredHistory = JSON.parse(inputs[3]);
alteredHistory.cases[0].operatorLabel = { decision: 'approved' };
assert.throws(() => validate(inputs[0], inputs[1], inputs[2], JSON.stringify(alteredHistory)), 'historical label rewritten'); rejected++;
// Ensure the checker refuses the mistakes that would manufacture measurement evidence.
for (const [index, field, value] of [
  [1, 'eligibleForMeasurement', true], [1, 'partition', 'held-out'],
  [2, 'status', 'accepted'], [2, 'elapsedSeconds', 0], [2, 'rawObservations', [{ id: 'invented' }]],
] as const) {
  const mutated = structuredClone(inputs);
  record(mutated[index]); mutated[index][field] = value;
  assert.throws(() => validate(...mutated), 'invalid evidence claim was accepted');
  rejected++;
}
for (const [group, fields] of Object.entries({
  acceptance: ['labelledAt', 'scopeBreach', 'artifactRef', 'reason'],
  followUp: ['startedAt', 'dueAt', 'completedAt', 'evidenceRef'],
})) {
  for (const field of fields) {
    const mutated = structuredClone(inputs);
    record(mutated[2]); const observation = mutated[2][group]; record(observation);
    observation[field] = field === 'scopeBreach' ? true : 'invented-observation';
    assert.throws(() => validate(...mutated), 'nested template observation was accepted');
    rejected++;
  }
}
console.log(`Baseline declarations verified: 3 intended-outcome approvals pending registration, preserved historical proposal, 6 excluded synthetic examples, unstarted template; ${rejected} invalid claims rejected. No live evidence collected or private authority authenticated.`);
