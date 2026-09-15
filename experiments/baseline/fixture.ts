// Invented public test data. This never registers or executes an operator case.
import { BOUNDS } from './registration.ts';
import { digest, type Row } from './validation.ts';
export const ref = (name: string) => ({ ref: 'synthetic:' + name, sha256: digest(name) });
export function fixture(): Row {
  const registration = {
    schema: 1, kind: 'frozen-baseline-declaration', origin: 'synthetic', caseId: 'synthetic-ci', version: 1,
    frozenAt: '2026-09-15T00:00:00.000Z', previousDigest: null, authority: ref('intended-outcome-approval'), approvedCaseDigest: digest('two-action-pins'),
    eligibility: { status: 'feasible', source: ref('synthetic-eligibility') },
    fixture: { manifest: ref('fixture-manifest'), startingCommit: 'a'.repeat(40), treeDigest: digest('base-tree'), context: ref('source-context'), brief: ref('brief'), progressSeed: ref('progress-seed') },
    paths: { read: ['ci/first.yml', 'ci/second.yml', 'lock.json'], write: ['ci/first.yml', 'ci/second.yml'] },
    criteria: [{ id: 'pins', textDigest: digest('Immutable pins only') }, { id: 'attribution', textDigest: digest('Report inherited failure') }],
    toolchains: [{ id: 'node', version: '24.18.0-synthetic', binaryDigest: digest('node-binary'), environmentDigest: digest('isolated-environment') }],
    checks: [
      { id: 'pins', criterionIds: ['pins'], argv: ['node', 'check-pins.mjs'], cwd: '.', toolchainId: 'node', inputs: ref('pin-check-inputs'), required: true, revisions: 'candidate' },
      { id: 'audit', criterionIds: ['attribution'], argv: ['node', 'fixture-audit.mjs'], cwd: '.', toolchainId: 'node', inputs: ref('frozen-advisory-fixture'), required: true, revisions: 'both' },
    ],
    execution: { harness: ref('harness-build'), router: ref('router-build'), namedRoute: 'synthetic-codex-route', nativeProfileDigest: digest('profile'), packetDigest: digest('packet'), isolation: ref('isolation'), preflight: ref('preflight'), stopPlan: ref('stop-plan') },
    bounds: { ...BOUNDS }, operatorEffort: { treatment: 'cap-including-preparation', maximumActiveSeconds: 1800, source: ref('effort-treatment') },
  };
  const run = {
    schema: 1, kind: 'baseline-observation', origin: 'synthetic', runId: 'synthetic-run', registrationDigest: digest(registration), registrationCommit: 'b'.repeat(40), status: 'failed',
    startedAt: '2026-09-15T00:01:00.000Z', endedAt: '2026-09-15T00:02:00.000Z', elapsedMs: 60000, waitingMs: 30000,
    scope: { id: 'synthetic-scope', startedAt: '2026-09-15T00:01:00.000Z', deadlineAt: '2026-09-15T00:16:00.000Z', physicalAttempts: 2, operationsComplete: true, quiescence: 'confirmed', evidence: ref('scope-receipts') },
    attempts: [
      { id: 'attempt-one', startedAt: '2026-09-15T00:01:00.000Z', endedAt: '2026-09-15T00:01:20.000Z', status: 'failed', exitCode: 1, invocation: ref('invocation-1'), progressNotes: ref('progress-1'), artifact: null, observations: [ref('failed-attempt')], operations: [{ id: 'operation-one', source: 'original', outcome: 'failure', evidence: ref('receipt-1') }] },
      { id: 'attempt-two', startedAt: '2026-09-15T00:01:20.000Z', endedAt: '2026-09-15T00:02:00.000Z', status: 'completed', exitCode: 0, invocation: ref('invocation-2'), progressNotes: ref('progress-2'), artifact: ref('candidate'), observations: [ref('completed-attempt')], operations: [{ id: 'operation-two', source: 'router-fallback', outcome: 'success', evidence: ref('receipt-2') }] },
    ],
    checks: [
      { id: 'candidate-pins', checkId: 'pins', attemptId: 'attempt-two', revision: 'candidate', status: 'passed', exitCode: 0, evidence: ref('pin-check') },
      { id: 'base-audit', checkId: 'audit', attemptId: null, revision: 'base', status: 'failed', exitCode: 1, evidence: ref('base-audit') },
      { id: 'candidate-audit', checkId: 'audit', attemptId: 'attempt-two', revision: 'candidate', status: 'failed', exitCode: 1, evidence: ref('candidate-audit') },
    ],
    operatorCoverage: { trial: 'incomplete', 'follow-up': 'incomplete' },
    operatorIntervals: [
      { id: 'planning', operatorId: 'fixture-operator', category: 'planning', phase: 'trial', start: '2026-09-14T23:59:00.000Z', end: '2026-09-14T23:59:30.000Z', durationSeconds: 30, confidence: 'measured', source: ref('planning-timer') },
      { id: 'correction', operatorId: 'fixture-operator', category: 'correction', phase: 'trial', start: null, end: null, durationSeconds: 12, confidence: 'estimated', source: ref('correction-estimate') },
      { id: 'recovery', operatorId: 'fixture-operator', category: 'recovery', phase: 'trial', start: null, end: null, durationSeconds: null, confidence: 'unknown', source: ref('missing-recovery-timer') },
    ],
    usageCoverage: 'incomplete', usage: [{ attemptId: null, scope: 'attempt', confidence: 'unknown', source: ref('usage-unavailable'), inputTokens: null, outputTokens: null, cachedTokens: null, incrementalCash: null, currency: null }],
    ownershipCosts: [], rawObservations: [ref('retained-observation-manifest')],
    acceptance: { decision: null, operatorId: null, labelledAt: null, source: null, criteria: [], scopeBreach: null, artifact: null },
    followUp: { startedAt: null, dueAt: null, completedAt: null, evidence: null, defects: [] },
  };
  return { schema: 1, registrations: [registration], runs: [run] };
}
export function acceptedFixture(): Row {
  const input = fixture(), run = input.runs[0];
  run.status = 'accepted'; run.operatorIntervals.pop(); run.operatorCoverage.trial = 'complete';
  run.acceptance = { decision: 'accepted', operatorId: 'fixture-operator', labelledAt: '2026-09-15T00:03:00.000Z', source: ref('fixture-output-label'),
    criteria: input.registrations[0].criteria.map((c: Row) => ({ id: c.id, met: true, evidence: ref('criterion-' + c.id) })), scopeBreach: false, artifact: ref('candidate') };
  run.followUp = { startedAt: '2026-09-15T00:03:00.000Z', dueAt: '2026-09-22T00:03:00.000Z', completedAt: null, evidence: null, defects: [] };
  return input;
}
