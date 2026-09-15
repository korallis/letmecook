import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, readFile, writeFile, stat, readdir, rm, symlink, chmod, mkdir } from 'node:fs/promises';
import { renameSync, symlinkSync, chmodSync, unlinkSync, writeFileSync, readFileSync, mkdirSync, linkSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import type { FileHandle } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createHash } from 'node:crypto';
import { fixture, acceptedFixture, ref } from './fixture.ts';
import { validateRegistration, registrationIdentity } from './registration.ts';
import { collectDataset, readInput, readBounded, writeExport } from './collect.ts';
import { digest, type Row } from './validation.ts';

function changedRegistration(input: Row, edit: (r: Row) => void) { edit(input.registrations[0]); input.runs[0].registrationDigest = digest(input.registrations[0]); return input; }
function shiftRun(run: Row, milliseconds: number) {
  const shift = (record: Row, fields: string[]) => fields.forEach(key => { if (record[key] !== null) record[key] = new Date(Date.parse(record[key]) + milliseconds).toISOString(); });
  shift(run, ['startedAt', 'endedAt']); shift(run.scope, ['startedAt', 'deadlineAt']); run.attempts.forEach((a: Row) => shift(a, ['startedAt', 'endedAt']));
  run.operatorIntervals.forEach((i: Row) => shift(i, ['start', 'end'])); shift(run.acceptance, ['labelledAt']); shift(run.followUp, ['startedAt', 'dueAt', 'completedAt']);
}
function withEffort(input: Row, measuredSeconds: number | null, estimates: number[]) {
  const run = input.runs[0], [measured, estimated] = run.operatorIntervals;
  run.operatorCoverage.trial = 'complete';
  run.operatorIntervals = estimates.map((durationSeconds, i) => ({ ...estimated, id: 'estimate-' + i, durationSeconds }));
  if (measuredSeconds !== null) run.operatorIntervals.unshift({ ...measured, durationSeconds: measuredSeconds,
    end: new Date(Date.parse(measured.start) + measuredSeconds * 1000).toISOString() });
  return input;
}

test('closed frozen registration binds identity, source, scope, toolchain and exact approved bounds without granting authority', () => {
  const r = fixture().registrations[0], valid = registrationIdentity(r);
  assert.equal(valid.registrationDigest, digest(r)); assert.equal(valid.executionAuthorized, false); assert.equal(valid.evidenceAuthenticated, false);
  const mutations: ((r: Row) => void)[] = [
    r => { r.extra = true; }, r => { r.fixture.startingCommit = 'main'; }, r => { r.fixture.context.sha256 = null; },
    r => { r.authority = null; }, r => { r.bounds.maxInferenceAttempts = 33; }, r => { r.bounds.elapsedMs = 900001; },
    r => { r.bounds.maxRefreshOperations = 1; }, r => { r.bounds.paidFallback = true; }, r => { r.bounds.model = 'other'; },
    r => { r.bounds.effort = 'low'; }, r => { r.bounds.concurrentCases = 2; }, r => { r.bounds.providerMonetaryCap = 0; },
    r => { r.bounds.unknownOriginalBlocksReplacement = false; }, r => { r.operatorEffort.source = null; },
    r => { r.operatorEffort.maximumActiveSeconds = 1801; }, r => { r.operatorEffort.treatment = 'record-only'; },
    r => { r.execution.packetDigest = ''; }, r => { r.execution.stopPlan = null; }, r => { r.paths.write = ['../escape']; },
    r => { r.paths.read = ['.git/config']; }, r => { r.checks[0].toolchainId = 'missing'; }, r => { r.checks[0].argv = []; },
    r => { r.checks[0].criterionIds = ['missing']; }, r => { r.checks = []; }, r => { r.criteria[1].id = 'pins'; },
    r => { r.version = 2; }, r => { r.origin = 'operator'; r.caseId = 'operator-01'; },
  ];
  for (const mutate of mutations) { const x = structuredClone(r); mutate(x); assert.throws(() => validateRegistration(x)); }
  const input = fixture(); input.registrations[0].fixture.context = ref('changed-context'); assert.throws(() => collectDataset(input), /unregistered_run/);
});

test('operator declarations bind the preserved public approval and criteria, without retrieving private evidence', async () => {
  const intake = JSON.parse(await readFile(new URL('../../tests/fixtures/tasks/operator-intake.json', import.meta.url), 'utf8'));
  const r = fixture().registrations[0], approved = intake.cases[0]; r.origin = 'operator'; r.caseId = approved.id;
  r.authority = { ref: intake.authority.reference, sha256: intake.authority.sha256 };
  r.fixture.manifest = { ref: intake.privateManifest.reference, sha256: intake.privateManifest.sha256 }; r.approvedCaseDigest = digest(approved);
  r.criteria = approved.acceptanceCriteria.map((x: string, i: number) => ({ id: 'criterion-' + i, textDigest: digest(x) }));
  r.checks[0].criterionIds = r.criteria.map((c: Row) => c.id); r.checks[1].criterionIds = [r.criteria[0].id];
  assert.equal(registrationIdentity(r).executionAuthorized, false);
  for (const mutate of [(x: Row) => { x.authority = ref('substituted-authority'); }, (x: Row) => { x.fixture.manifest = ref('other-base'); }, (x: Row) => { x.approvedCaseDigest = digest('expanded-scope'); }, (x: Row) => { x.criteria[0].textDigest = digest('different-outcome'); }]) {
    const x = structuredClone(r); mutate(x); assert.throws(() => validateRegistration(x));
  }
});

test('failed requests, fallback, failed checks and measured/estimated/unknown effort survive JSON and CSV collection', () => {
  const input = fixture(), result = collectDataset(input), row = result.publicJSON.rows[0];
  assert.deepEqual(result.privateJSON.runs, input.runs); assert.deepEqual(result.privateJSON.registrations, input.registrations);
  assert.equal(row.harnessAttempts, 2); assert.equal(row.physicalAttempts, 2); assert.equal(row.physicalFailureCount, 1); assert.equal(row.checks.failed, 2);
  assert.deepEqual(row.trialEffort, { measuredSeconds: 30, estimatedSeconds: 12, unknownIntervals: 1, complete: false, totalSeconds: null, confidence: 'unknown', exactSeconds: { measured: '30', estimated: '12', total: null } });
  assert.equal(row.usage.inputTokens, null); assert.equal(row.usage.incrementalCash, null); assert.equal(row.outputLabel, null);
  assert.equal(result.publicJSON.syntheticExcluded, 1); assert.equal(result.publicJSON.cohort.registered, 0); assert.equal(result.publicJSON.cohort.completionRate, null); assert.equal(result.publicJSON.cohort.minutesPerAcceptedOutcome, null);
  assert.match(result.csv, /,30,0,12,,unknown,/); assert(!result.csv.includes('synthetic-run'));
});

test('human-labelled useful failure-attribution can retain failing gates; acceptance is never inferred', () => {
  const result = collectDataset(acceptedFixture()); assert.equal(result.publicJSON.rows[0].acceptedWithinRegisteredBounds, true);
  assert.equal(result.publicJSON.rows[0].checks.failed, 2); assert.equal(result.publicJSON.rows[0].trialEffort.confidence, 'estimated');
  assert.equal(result.publicJSON.rows[0].followUpComplete, false); assert.equal(result.publicJSON.cohort.accepted, 0);
  const bad = fixture(); bad.runs[0].status = 'accepted'; assert.throws(() => collectDataset(bad), /operator_acceptance_required/);
});

test('required candidate observations bind the final accepted attempt and artifact, while stale history is retained', () => {
  for (const attemptId of ['attempt-one', null]) {
    const input = acceptedFixture(); input.runs[0].checks.filter((c: Row) => c.revision === 'candidate').forEach((c: Row) => { c.attemptId = attemptId; });
    assert.throws(() => collectDataset(input), /wrong_candidate/);
    input.runs[0].status = 'failed'; const result = collectDataset(input);
    assert.deepEqual(result.privateJSON.runs, input.runs); assert.equal(result.publicJSON.rows[0].acceptedWithinRegisteredBounds, false);
  }
  for (const artifact of [null, ref('earlier-candidate')]) {
    const input = acceptedFixture(); input.runs[0].checks[0].candidateArtifact = artifact;
    assert.throws(() => collectDataset(input), /wrong_candidate/);
  }
  const input = acceptedFixture(), old = { ...input.runs[0].checks[0], id: 'old-pins-check', attemptId: 'attempt-one', candidateArtifact: ref('older-candidate') };
  input.runs[0].checks.unshift(old); const result = collectDataset(input);
  assert.deepEqual(result.privateJSON.runs, input.runs); assert.equal(result.publicJSON.rows[0].acceptedWithinRegisteredBounds, true);
});

test('final candidate-bound failed, not-run and unknown checks remain valid sourced observations', () => {
  for (const status of ['failed', 'not-run', 'unknown']) {
    const input = acceptedFixture(), check = input.runs[0].checks[0]; check.status = status; check.exitCode = status === 'failed' ? 1 : null;
    const result = collectDataset(input); assert.deepEqual(result.privateJSON.runs, input.runs);
    assert.equal(result.publicJSON.rows[0].acceptedWithinRegisteredBounds, true);
    assert(result.publicJSON.rows[0].checks[status] > 0);
  }
});

test('rejects fabricated acceptance, evidence loss, unknown-zero conversion and changed physical counts', () => {
  const mutations: ((r: Row) => void)[] = [
    r => { r.registrationCommit = null; }, r => { r.scope.physicalAttempts = 1; }, r => { r.scope.operationsComplete = false; },
    r => { r.attempts[1].operations[0].id = r.attempts[0].operations[0].id; }, r => { r.attempts[0].observations = []; },
    r => { r.acceptance.source = null; }, r => { r.acceptance.operatorId = null; }, r => { r.acceptance.criteria[0].met = false; },
    r => { r.acceptance.scopeBreach = true; }, r => { r.acceptance.labelledAt = '2026-09-15T00:01:00.000Z'; },
    r => { r.checks[0].status = 'passed'; r.checks[0].exitCode = 1; }, r => { r.checks = []; },
    r => { r.operatorCoverage.trial = 'incomplete'; }, r => { r.operatorIntervals[0].durationSeconds = 0; },
    r => { r.operatorIntervals = []; }, r => { r.attempts.reverse(); },
    r => { r.attempts[1].exitCode = 1; }, r => { r.attempts[1].artifact = null; },
    r => { r.attempts.forEach((a: Row) => { a.operations = []; }); r.scope.physicalAttempts = 0; },
    r => { r.usage[0].inputTokens = 0; }, r => { r.usage[0].incrementalCash = 0; }, r => { r.rawObservations = []; },
    r => { r.followUp.completedAt = '2026-09-16T00:03:00.000Z'; }, r => { r.elapsedMs = 900001; },
  ];
  for (const mutate of mutations) { const x = acceptedFixture(); mutate(x.runs[0]); assert.throws(() => collectDataset(x)); }
});

test('unknown scope observations, exhausted bounds and late output labels remain failed records', () => {
  const input = acceptedFixture(), r = input.runs[0]; r.status = 'budget-exhausted'; r.elapsedMs = 900100;
  r.scope.physicalAttempts = 33; r.scope.operationsComplete = false; r.scope.quiescence = 'unknown';
  const row = collectDataset(input).publicJSON.rows[0]; assert.equal(row.outputLabel, 'accepted'); assert.equal(row.acceptedWithinRegisteredBounds, false);
  assert.equal(row.overruns.physicalAttempts, 1); assert.equal(row.overruns.elapsedMs, 100); assert.equal(row.unknownOriginal, true);
  const unknown = fixture(); unknown.runs[0].scope.physicalAttempts = null; unknown.runs[0].scope.operationsComplete = false; unknown.runs[0].scope.quiescence = 'unknown'; unknown.runs[0].elapsedMs = null;
  const u = collectDataset(unknown).publicJSON.rows[0]; assert.equal(u.physicalAttempts, null); assert.equal(u.overruns.elapsedMs, null);
});

test('known wall elapsed and overrun remain visible beside short or unknown independent elapsed observations', () => {
  for (const elapsedMs of [60000, null]) {
    const input = fixture(); input.runs[0].endedAt = '2026-09-15T00:17:00.000Z'; input.runs[0].elapsedMs = elapsedMs;
    const result = collectDataset(input), row = result.publicJSON.rows[0];
    assert.deepEqual(result.privateJSON.runs, input.runs); assert.equal(row.elapsedMs, elapsedMs);
    assert.equal(row.wallElapsedMs, 960000); assert.equal(row.overruns.wallElapsedMs, 60000);
    assert.equal(row.overruns.elapsedMs, elapsedMs === null ? null : 0);
    const [headers, values] = result.csv.trimEnd().split('\n').map(line => line.split(','));
    assert.equal(values[headers.indexOf('elapsed_ms')], elapsedMs === null ? '' : '60000');
    assert.equal(values[headers.indexOf('wall_elapsed_ms')], '960000'); assert.equal(values[headers.indexOf('wall_overrun_ms')], '60000');
  }
});

test('supplied physical operations and observed counts retain proven overruns without inventing a complete total', () => {
  for (const count of [0, 1, 31, 32, 33, 64]) {
    const input = fixture(), run = input.runs[0]; run.attempts[0].operations = [];
    run.attempts[1].operations = Array.from({ length: count }, (_, i) => ({ id: 'observed-' + i, source: i % 2 ? 'router-fallback' : 'original', outcome: i % 3 ? 'success' : 'failure', evidence: ref('receipt-' + i) }));
    run.scope.physicalAttempts = null; run.scope.operationsComplete = false; run.scope.quiescence = 'unknown';
    const result = collectDataset(input), row = result.publicJSON.rows[0];
    assert.deepEqual(result.privateJSON.runs, input.runs); assert.equal(row.physicalAttempts, null); assert.equal(row.overruns.physicalAttempts, null);
    assert.equal(row.knownLowerBounds.physicalAttempts, count); assert.equal(row.provenOverruns.physicalAttempts, Math.max(0, count - 32));
    const [header, data] = result.csv.trimEnd().split('\n').map(line => line.split(','));
    assert.equal(data[header.indexOf('physical_attempts')], ''); assert.equal(data[header.indexOf('known_physical_attempt_lower_bound')], String(count));
    assert.equal(data[header.indexOf('proven_physical_overrun')], String(Math.max(0, count - 32)));
  }
  const counted = fixture(); counted.runs[0].scope.physicalAttempts = 40; counted.runs[0].scope.operationsComplete = false; counted.runs[0].scope.quiescence = 'unknown';
  const row = collectDataset(counted).publicJSON.rows[0]; assert.equal(row.knownLowerBounds.physicalAttempts, 40); assert.equal(row.provenOverruns.physicalAttempts, 8);
});

test('measured lower bounds prove effort overruns despite unknown intervals, while estimates remain separate', () => {
  for (const measured of [1800, 1801]) for (const estimated of [12, 1801]) {
    const input = fixture(), run = input.runs[0]; const interval = run.operatorIntervals[0]; interval.start = '2026-09-14T23:00:00.000Z';
    interval.end = new Date(Date.parse(interval.start) + measured * 1000).toISOString(); interval.durationSeconds = measured; run.operatorIntervals[1].durationSeconds = estimated;
    const result = collectDataset(input), row = result.publicJSON.rows[0];
    assert.deepEqual(result.privateJSON.runs, input.runs); assert.equal(row.trialEffort.totalSeconds, null); assert.equal(row.overruns.operatorSeconds, null);
    assert.equal(row.knownLowerBounds.measuredOperatorSeconds, measured); assert.equal(row.trialEffort.estimatedSeconds, estimated);
    assert.equal(row.provenOverruns.measuredOperatorSeconds, Math.max(0, measured - 1800));
    const [header, data] = result.csv.trimEnd().split('\n').map(line => line.split(','));
    assert.equal(data[header.indexOf('total_trial_seconds')], ''); assert.equal(data[header.indexOf('proven_measured_operator_overrun_seconds')], String(Math.max(0, measured - 1800)));
  }
  const uncapped = changedRegistration(fixture(), r => { r.operatorEffort.treatment = 'record-only'; r.operatorEffort.maximumActiveSeconds = null; });
  assert.equal(collectDataset(uncapped).publicJSON.rows[0].provenOverruns.measuredOperatorSeconds, null);
  const fractional = changedRegistration(fixture(), r => { r.operatorEffort.maximumActiveSeconds = 1; }), run = fractional.runs[0], measured = run.operatorIntervals[0];
  run.operatorIntervals = [...Array.from({ length: 10 }, (_, i) => ({ ...measured, id: 'fraction-' + i, durationSeconds: 0.1,
    start: new Date(Date.parse(measured.start) + i * 100).toISOString(), end: new Date(Date.parse(measured.start) + (i + 1) * 100).toISOString() })), ...run.operatorIntervals.slice(1)];
  const row = collectDataset(fractional).publicJSON.rows[0]; assert.equal(row.knownLowerBounds.measuredOperatorSeconds, 1); assert.equal(row.provenOverruns.measuredOperatorSeconds, 0); assert.equal(row.trialEffort.totalSeconds, null);
});

test('decimal mixed and estimated effort qualifies below and at the cap and preserves real overruns above it', () => {
  for (const measured of [1, null]) for (const [last, expected, excess] of measured === null
    ? [[180.899, '1799.999', '0'], [180.9, '1800', '0'], [180.901, '1800.001', '0.001']] as const
    : [[179.899, '1799.999', '0'], [179.9, '1800', '0'], [179.901, '1800.001', '0.001']] as const) {
    const input = withEffort(acceptedFixture(), measured, [...Array(9).fill(179.9), last]);
    if (excess === '0') assert.equal(collectDataset(input).publicJSON.rows[0].acceptedWithinRegisteredBounds, true);
    else assert.throws(() => collectDataset(input), /accepted_effort_bound/);
    input.runs[0].status = 'failed';
    for (const reverse of [false, true]) {
      if (reverse) input.runs[0].operatorIntervals.reverse();
      const result = collectDataset(input), row = result.publicJSON.rows[0]; assert.deepEqual(result.privateJSON.runs, input.runs);
      assert.equal(row.trialEffort.totalSeconds, Number(expected)); assert.equal(row.trialEffort.exactSeconds.total, expected);
      assert.equal(row.trialEffort.confidence, 'estimated'); assert.equal(row.knownLowerBounds.measuredOperatorSeconds, measured ?? 0);
      assert.equal(row.overruns.operatorSeconds, Number(excess)); assert.equal(row.overruns.operatorSecondsExact, excess);
      assert.equal(row.provenOverruns.measuredOperatorSeconds, 0);
      const [header, data] = result.csv.trimEnd().split('\n').map(line => line.split(','));
      assert.equal(data[header.indexOf('total_trial_seconds_exact')], expected); assert.equal(data[header.indexOf('operator_overrun_seconds_exact')], excess);
    }
  }
});

test('exponent estimates and precision beyond the rounded total retain exact cap decisions without a tolerance', () => {
  for (const [estimates, exact, excess] of [
    [[0.9999999, 9e-8], '0.99999999', '0'], [[0.9999999, 1e-7], '1', '0'], [[0.9999999, 1.1e-7], '1.00000001', '0.00000001'],
    [[0.7, 0.30000000000000004], '1.00000000000000004', '0.00000000000000004'],
    [[1, Number.MIN_VALUE], '1.' + '0'.repeat(323) + '5', '0.' + '0'.repeat(323) + '5'],
  ] as const) {
    const input = withEffort(changedRegistration(acceptedFixture(), r => { r.operatorEffort.maximumActiveSeconds = 1; }), null, [...estimates]);
    if (excess === '0') assert.equal(collectDataset(input).publicJSON.rows[0].acceptedWithinRegisteredBounds, true);
    else assert.throws(() => collectDataset(input), /accepted_effort_bound/);
    input.runs[0].status = 'failed'; const row = collectDataset(input).publicJSON.rows[0];
    assert.equal(row.trialEffort.exactSeconds.estimated, exact); assert.equal(row.trialEffort.exactSeconds.total, exact);
    assert.equal(row.overruns.operatorSecondsExact, excess); assert.equal(row.overruns.operatorSeconds, Number(excess));
    assert.equal(row.knownLowerBounds.measuredOperatorSeconds, 0); assert.equal(row.trialEffort.confidence, 'estimated');
  }
  const large = withEffort(fixture(), null, [1e15, 0.01]);
  const row = collectDataset(large).publicJSON.rows[0]; assert.equal(row.trialEffort.exactSeconds.estimated, '1000000000000000.01');
  assert.equal(row.trialEffort.estimatedSeconds, 1e15); assert.equal(row.overruns.operatorSecondsExact, '999999999998200.01');
});

test('decimal sums retain unknown totals, separate estimates, and exact category observations', () => {
  const input = withEffort(fixture(), 1, Array(10).fill(179.9));
  input.runs[0].operatorIntervals.forEach((i: Row) => { i.category = 'review'; });
  input.runs[0].operatorIntervals.push(fixture().runs[0].operatorIntervals[2]);
  const result = collectDataset(input), row = result.publicJSON.rows[0];
  assert.deepEqual(result.privateJSON.runs, input.runs); assert.equal(row.trialEffort.exactSeconds.estimated, '1799');
  assert.equal(row.trialEffort.exactSeconds.total, null); assert.equal(row.trialEffort.totalSeconds, null); assert.equal(row.trialEffort.confidence, 'unknown');
  assert.equal(row.overruns.operatorSeconds, null); assert.equal(row.overruns.operatorSecondsExact, null);
  assert.equal(row.knownLowerBounds.measuredOperatorSeconds, 1); assert.equal(row.provenOverruns.measuredOperatorSeconds, 0);
  assert.equal(row.knownSecondsByCategory.review, 1800); assert.equal(row.knownSecondsByCategoryExact.review, '1800');
});

test('cohort sums use original decimal observations across cases and phases without summing rounded projections', async () => {
  const intake = JSON.parse(await readFile(new URL('../../tests/fixtures/tasks/operator-intake.json', import.meta.url), 'utf8'));
  const cases = intake.cases.map((approved: Row, index: number) => {
    // Invented observations exercising the public operator-cohort projection in
    // memory only. This test does not register, persist, or run an operator case.
    const input = withEffort(fixture(), null, index === 0 ? [0.1, 1e-18] : [index === 1 ? 0.2 : 0.3]);
    const r = input.registrations[0], run = input.runs[0]; r.origin = run.origin = 'operator'; r.caseId = approved.id;
    r.authority = { ref: intake.authority.reference, sha256: intake.authority.sha256 }; r.fixture.manifest = { ref: intake.privateManifest.reference, sha256: intake.privateManifest.sha256 };
    r.approvedCaseDigest = digest(approved); r.criteria = approved.acceptanceCriteria.map((x: string, i: number) => ({ id: 'criterion-' + i, textDigest: digest(x) }));
    r.checks[0].criterionIds = r.criteria.map((c: Row) => c.id); r.checks[1].criterionIds = [r.criteria[0].id]; run.registrationDigest = digest(r);
    run.runId = 'decimal-case-' + index; run.scope.id = 'decimal-scope-' + index; shiftRun(run, index * 120000);
    run.operatorCoverage['follow-up'] = 'complete'; run.operatorIntervals.push({ ...run.operatorIntervals[0], id: 'follow-up', phase: 'follow-up', durationSeconds: [0.1, 0.2, 0.3][index] });
    return input;
  });
  const input = { schema: 1, registrations: cases.flatMap((c: Row) => c.registrations), runs: cases.flatMap((c: Row) => c.runs) };
  for (const reverse of [false, true]) {
    if (reverse) input.runs.reverse(); const result = collectDataset(input), cohort = result.publicJSON.cohort;
    assert.deepEqual(result.privateJSON.runs, input.runs); assert.equal(cohort.accepted, 0); assert.equal(cohort.trialSeconds, 0.6); assert.equal(cohort.allInSeconds, 1.2);
    assert.deepEqual(cohort.exactSeconds, { trial: '0.600000000000000001', allIn: '1.200000000000000001' });
  }
});

test('collector CLI exports the exact-cap decimal review repro with no invented overrun', async () => {
  const root = await mkdtemp(join(tmpdir(), 'gaffer-baseline-decimal-cli-'));
  try {
    const input = withEffort(acceptedFixture(), 1, Array(10).fill(179.9)), file = join(root, 'input.json'), output = join(root, 'export');
    await writeFile(file, JSON.stringify(input), { mode: 0o600 });
    const response = execFileSync(process.execPath, [new URL('./collect.ts', import.meta.url).pathname, file, output], { timeout: 5000, encoding: 'utf8' });
    assert.deepEqual(JSON.parse(response), { collected: true, operatorCases: 0, syntheticExcluded: 1, executionAuthorized: false });
    const summary = JSON.parse(await readFile(join(output, 'public-summary.json'), 'utf8'));
    assert.equal(summary.rows[0].trialEffort.totalSeconds, 1800); assert.equal(summary.rows[0].overruns.operatorSeconds, 0);
    assert.equal(summary.rows[0].acceptedWithinRegisteredBounds, true); assert.equal(summary.rows[0].trialEffort.confidence, 'estimated');
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('operator overlap, duplicate case records and reuse cannot manufacture lower effort or extra successes', () => {
  const input = fixture(); input.runs[0].operatorIntervals.push({ ...input.runs[0].operatorIntervals[0], id: 'overlap' }); assert.throws(() => collectDataset(input), /overlapping_operator/);
  const twice = fixture(); twice.runs.push(structuredClone(twice.runs[0])); assert.throws(() => collectDataset(twice), /duplicate_run/);
  const missing = fixture(); missing.runs = []; assert.throws(() => collectDataset(missing));
  const infeasible = changedRegistration(fixture(), r => { r.eligibility.status = 'infeasible'; }); assert.throws(() => collectDataset(infeasible), /eligibility/);
});

test('records sequence incidents without discarding attempted replacements after unknown originals', () => {
  const input = fixture(), a = input.runs[0]; a.scope.quiescence = 'unknown'; a.attempts[0].operations[0].outcome = 'unknown';
  const second = acceptedFixture(), b = second.runs[0]; second.registrations[0].caseId = 'synthetic-next'; b.registrationDigest = digest(second.registrations[0]); b.runId = 'next-run'; b.scope.id = 'next-scope';
  b.operatorIntervals = []; b.operatorCoverage.trial = 'incomplete'; b.status = 'failed';
  shiftRun(b, 1000);
  input.registrations.push(second.registrations[0]); input.runs.push(b);
  const rows = collectDataset(input).publicJSON.rows;
  assert(rows[0].sequenceViolations.includes('replacement-after-unknown-original'));
  assert(rows[1].sequenceViolations.includes('started-after-unknown-original')); assert(rows[1].sequenceViolations.includes('concurrent-case'));
  assert.equal(rows[1].outputLabel, 'accepted'); assert.equal(rows[1].acceptedWithinRegisteredBounds, false);
});

test('all overlapping cases lose qualification independently of input order, including equal, staggered and contained intervals', () => {
  for (const [offset, duration, overlaps] of [[0, 60000, true], [30000, 60000, true], [10000, 40000, true], [60000, 60000, false], [120000, 60000, false]] as const) {
    const accepted = acceptedFixture(), other = fixture(); changedRegistration(other, r => { r.caseId = 'synthetic-other'; });
    const b = other.runs[0]; b.runId = 'other-run'; b.scope.id = 'other-scope'; b.operatorIntervals.forEach((i: Row) => { i.operatorId = 'other-operator'; });
    shiftRun(b, offset); b.endedAt = new Date(Date.parse(b.startedAt) + duration).toISOString(); b.attempts[1].endedAt = b.endedAt; b.elapsedMs = duration;
    const input = { schema: 1, registrations: [...accepted.registrations, ...other.registrations], runs: [...accepted.runs, ...other.runs] };
    const byRun = () => { const result = collectDataset(input); assert.deepEqual(result.privateJSON.runs, input.runs); return Object.fromEntries(input.runs.map((r, i) => [r.runId, { incidents: result.publicJSON.rows[i].sequenceViolations, qualified: result.publicJSON.rows[i].acceptedWithinRegisteredBounds }])); };
    const forward = byRun(); input.runs.reverse(); input.registrations.reverse(); assert.deepEqual(byRun(), forward);
    assert.equal(forward['synthetic-run'].incidents.includes('concurrent-case'), overlaps); assert.equal(forward['other-run'].incidents.includes('concurrent-case'), overlaps);
    assert.equal(forward['synthetic-run'].qualified, !overlaps);
  }
  const cases = [acceptedFixture(), fixture(), fixture()];
  cases.forEach((input, index) => { changedRegistration(input, r => { r.caseId = 'synthetic-chain-' + index; }); const run = input.runs[0]; run.runId = 'chain-' + index; run.scope.id = 'scope-' + index; run.operatorIntervals.forEach((i: Row) => { i.operatorId = 'operator-' + index; }); shiftRun(run, index * 30000); });
  for (const order of [[0, 1, 2], [0, 2, 1], [1, 0, 2], [1, 2, 0], [2, 0, 1], [2, 1, 0]]) {
    const rows = collectDataset({ schema: 1, registrations: cases.flatMap(x => x.registrations), runs: order.map(i => cases[i].runs[0]) }).publicJSON.rows;
    assert(rows.every(r => r.sequenceViolations.includes('concurrent-case') && !r.acceptedWithinRegisteredBounds));
  }
});

test('unknown then another physical send in the same final attempt is flagged and retained unchanged', () => {
  for (const source of ['original', 'router-fallback']) {
    const input = fixture(), run = input.runs[0];
    run.attempts[1].operations.unshift({ id: 'unknown-in-same-attempt', source: 'original', outcome: 'unknown', evidence: ref('unknown-original') });
    run.attempts[1].operations[1].source = source;
    run.scope.physicalAttempts = 3; run.scope.quiescence = 'unknown';
    const result = collectDataset(input), row = result.publicJSON.rows[0];
    assert.deepEqual(result.privateJSON.runs, input.runs);
    assert.equal(row.unknownOriginal, true); assert.equal(row.physicalAttempts, 3);
    assert.deepEqual(row.sequenceViolations, ['replacement-after-unknown-original']);
    assert.equal(row.acceptedWithinRegisteredBounds, false);
  }
});

test('known failure then router fallback in the same final attempt is not an unknown-work replacement', () => {
  const input = fixture(), run = input.runs[0];
  run.attempts[1].operations.unshift({ id: 'failed-in-same-attempt', source: 'original', outcome: 'failure', evidence: ref('failed-original') });
  run.scope.physicalAttempts = 3;
  const result = collectDataset(input), row = result.publicJSON.rows[0];
  assert.deepEqual(result.privateJSON.runs, input.runs); assert.equal(row.unknownOriginal, false);
  assert.equal(row.physicalFailureCount, 2); assert.deepEqual(row.sequenceViolations, []);
});

test('terminal unknown with no later send or attempt stays unknown without inventing a replacement', () => {
  const input = fixture(), run = input.runs[0];
  run.attempts[1].operations.push({ id: 'terminal-unknown', source: 'original', outcome: 'unknown', evidence: ref('terminal-unknown') });
  run.scope.physicalAttempts = 3; run.scope.quiescence = 'unknown';
  const result = collectDataset(input), row = result.publicJSON.rows[0];
  assert.deepEqual(result.privateJSON.runs, input.runs); assert.equal(row.unknownOriginal, true);
  assert.equal(row.physicalAttempts, 3); assert.deepEqual(row.sequenceViolations, []);
});

test('public projection never copies private free text, paths, IDs or references, including CSV formulas', () => {
  const input = fixture(), sentinel = '=HYPERLINK("https://private.example.invalid/secret","source")';
  changedRegistration(input, r => {
    r.fixture.context.ref = sentinel; r.execution.namedRoute = sentinel; r.checks[0].argv = ['sh', '-c', sentinel]; r.toolchains[0].version = sentinel;
  });
  input.runs[0].runId = 'secret-looking-token'; input.runs[0].rawObservations[0].ref = sentinel; input.runs[0].operatorIntervals[0].source.ref = sentinel;
  const result = collectDataset(input), publicText = JSON.stringify(result.publicJSON) + result.csv;
  assert(!publicText.includes('private.example')); assert(!publicText.includes('secret-looking-token')); assert(!publicText.includes('HYPERLINK')); assert(!publicText.includes(input.registrations[0].fixture.startingCommit));
  assert(JSON.stringify(result.privateJSON).includes('HYPERLINK'));
});

test('record-only effort is explicit; unknown costs/usage remain unknown and currencies never mix', () => {
  const input = changedRegistration(acceptedFixture(), r => { r.operatorEffort.treatment = 'record-only'; r.operatorEffort.maximumActiveSeconds = null; });
  const r = input.runs[0]; r.operatorCoverage.trial = 'incomplete'; r.operatorIntervals = [];
  r.usageCoverage = 'complete'; r.usage = [];
  const row = collectDataset(input).publicJSON.rows[0]; assert.equal(row.acceptedWithinRegisteredBounds, true); assert.equal(row.trialEffort.totalSeconds, null); assert.equal(row.usage.inputTokens, null);
  r.usage = ['USD', 'GBP'].map(currency => ({ attemptId: null, scope: 'attempt', confidence: 'observed', source: ref(currency), inputTokens: 2, outputTokens: 1, cachedTokens: 0, incrementalCash: 1, currency }));
  const mixed = collectDataset(input).publicJSON.rows[0].usage; assert.equal(mixed.inputTokens, 4); assert.equal(mixed.incrementalCash, null); assert.equal(mixed.currency, null);
});

test('private exports are exclusive, synced and retain inputs; failure never acknowledges a complete bundle', async () => {
  const root = await mkdtemp(join(tmpdir(), 'gaffer-baseline-records-'));
  try {
    const output = join(root, 'export'); await writeExport(fixture(), output);
    const files = await readdir(output); assert.deepEqual(files.sort(), ['complete.json', 'private-records.json', 'public-summary.csv', 'public-summary.json']);
    const complete = JSON.parse(await readFile(join(output, 'complete.json'), 'utf8'));
    for (const file of files) {
      assert.equal((await stat(join(output, file))).mode & 0o077, 0);
      if (file !== 'complete.json') assert.equal(complete.files[file], createHash('sha256').update(await readFile(join(output, file))).digest('hex'));
    }
    await assert.rejects(writeExport(fixture(), output), /EEXIST/);
    const failed = join(root, 'failed'); await assert.rejects(writeExport(fixture(), failed, name => { if (name === 'public-summary.csv') throw Error('injected_storage_failure'); }), /injected/);
    await assert.rejects(stat(join(failed, 'complete.json')), /ENOENT/); assert((await readdir(failed)).includes('private-records.json'));
    const invalid = fixture(); invalid.runs[0].status = 'accepted'; const untouched = join(root, 'invalid'); await assert.rejects(writeExport(invalid, untouched)); await assert.rejects(stat(untouched), /ENOENT/);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('input refuses exposed files, symlinks, duplicate JSON keys and invalid UTF8 without executing supplied commands', async () => {
  const root = await mkdtemp(join(tmpdir(), 'gaffer-baseline-input-'));
  try {
    const file = join(root, 'input.json'); await writeFile(file, JSON.stringify(fixture()), { mode: 0o600 }); assert.deepEqual(await readInput(file), fixture());
    const link = join(root, 'linked.json'); await symlink(file, link); await assert.rejects(readInput(link));
    await chmod(file, 0o644); await assert.rejects(readInput(file)); await chmod(file, 0o600);
    await writeFile(file, '{"schema":1,"schema":2}'); await assert.rejects(readInput(file), /invalid_json/);
    await writeFile(file, Buffer.from([0xff])); await assert.rejects(readInput(file));
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('FIFO input is refused promptly before reading or blocking for a writer', async () => {
  const root = await mkdtemp(join(tmpdir(), 'gaffer-baseline-fifo-'));
  try {
    const fifo = join(root, 'input.fifo'); execFileSync('mkfifo', ['-m', '600', fifo], { timeout: 2000 });
    const code = `const {readInput}=await import(${JSON.stringify(new URL('./collect.ts', import.meta.url).href)}); try { await readInput(process.argv[1]); process.exitCode=2; } catch { console.log('refused'); }`;
    assert.equal(execFileSync(process.execPath, ['--input-type=module', '-e', code, fifo], { timeout: 2000, encoding: 'utf8' }).trim(), 'refused');
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('bounded FD reads stop at the limit plus one even when the input keeps growing after stat', async () => {
  let consumed = 0; const requested: number[] = [];
  const growing = { async read(buffer: Buffer, offset: number, length: number) { requested.push(length); buffer.fill(32, offset, offset + length); consumed += length; return { bytesRead: length, buffer }; } } as unknown as Pick<FileHandle, 'read'>;
  await assert.rejects(readBounded(growing), /private_bounded_input_required/);
  assert.equal(consumed, 16 * 1048576 + 1); assert(requested.every(n => n <= 65536));
});

test('directory substitution or mode changes refuse export acknowledgement and retain the original partial files', async () => {
  for (const stage of ['private-records.json', 'public-summary.json', 'complete.json']) {
    const root = await mkdtemp(join(tmpdir(), 'gaffer-baseline-swap-'));
    try {
      const output = join(root, 'output'), original = join(root, 'original-output'), target = join(root, 'target'); await mkdir(target, { mode: 0o700 });
      await assert.rejects(writeExport(fixture(), output, name => {
        if (name === stage) { renameSync(output, original); symlinkSync(target, output); }
      }), /export_directory_identity_changed/);
      assert.deepEqual(await readdir(target), []); await assert.rejects(stat(join(original, 'complete.json')), /ENOENT/);
      assert.equal((await readdir(original)).includes('private-records.json'), stage !== 'private-records.json');
    } finally { await rm(root, { recursive: true, force: true }); }
  }
  const root = await mkdtemp(join(tmpdir(), 'gaffer-baseline-mode-'));
  try {
    const output = join(root, 'output'); await assert.rejects(writeExport(fixture(), output, () => { chmodSync(output, 0o755); }), /export_directory_identity_changed/);
    assert.deepEqual(await readdir(output), []);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('every retained data file is revalidated before completion across deletion, substitution, content and private-property changes', async () => {
  for (const filename of ['private-records.json', 'public-summary.json', 'public-summary.csv']) {
    for (const change of ['delete', 'identical-replacement', 'in-place-content', 'symlink', 'directory', 'fifo', 'exposed-mode', 'private-mode-change', 'hardlink']) {
      const root = await mkdtemp(join(tmpdir(), 'gaffer-baseline-file-change-'));
      try {
        const output = join(root, 'output'); let original: Buffer | undefined;
        await assert.rejects(writeExport(fixture(), output, name => {
          if (name !== 'complete.json') return;
          const file = join(output, filename); original = readFileSync(file);
          if (change === 'delete') unlinkSync(file);
          if (change === 'identical-replacement') { renameSync(file, join(root, 'retained-original')); writeFileSync(file, original, { mode: 0o600 }); }
          if (change === 'in-place-content') writeFileSync(file, Buffer.alloc(original.length, 120));
          if (change === 'symlink') { renameSync(file, join(root, 'retained-original')); symlinkSync(join(root, 'retained-original'), file); }
          if (change === 'directory') { renameSync(file, join(root, 'retained-original')); mkdirSync(file, { mode: 0o700 }); }
          if (change === 'fifo') { renameSync(file, join(root, 'retained-original')); execFileSync('mkfifo', ['-m', '600', file], { timeout: 2000 }); }
          if (change === 'exposed-mode') chmodSync(file, 0o644);
          if (change === 'private-mode-change') chmodSync(file, 0o400);
          if (change === 'hardlink') linkSync(file, join(root, 'linked-original'));
        }), /export_file_|ENOENT/);
        assert(original && original.length > 0); await assert.rejects(stat(join(output, 'complete.json')), /ENOENT/);
        for (const unchanged of ['private-records.json', 'public-summary.json', 'public-summary.csv'].filter(name => name !== filename)) assert((await stat(join(output, unchanged))).isFile());
      } finally { await rm(root, { recursive: true, force: true }); }
    }
  }
});

test('final acknowledgement revalidates all files including the completion manifest after directory syncs', async () => {
  for (const filename of ['private-records.json', 'public-summary.json', 'public-summary.csv', 'complete.json']) {
    const root = await mkdtemp(join(tmpdir(), 'gaffer-baseline-final-content-'));
    const handle = await (await import('node:fs/promises')).open(join(root, 'prototype'), 'a', 0o600), prototype = Object.getPrototypeOf(handle); await handle.close();
    const originalSync = prototype.sync; let syncs = 0;
    const output = join(root, 'output');
    prototype.sync = async function (this: FileHandle, ...args: unknown[]) {
      const result = await originalSync.apply(this, args);
      if (++syncs === 7) { const file = join(output, filename); writeFileSync(file, Buffer.alloc(readFileSync(file).length, 120)); }
      return result;
    };
    try {
      await assert.rejects(writeExport(fixture(), output), /export_file_content_changed/);
      assert.equal(syncs, 7); assert.deepEqual((await readdir(output)).sort(), ['complete.json', 'private-records.json', 'public-summary.csv', 'public-summary.json']);
    } finally { prototype.sync = originalSync; await rm(root, { recursive: true, force: true }); }
  }
});

test('blocked unstarted case retains preparation overhead and no scope, attempts, acceptance or usage is invented', () => {
  const input = changedRegistration(fixture(), r => { r.eligibility.status = 'needs-clarification'; });
  const r = input.runs[0]; r.status = 'blocked'; r.startedAt = r.endedAt = r.elapsedMs = r.waitingMs = r.scope = null; r.attempts = []; r.checks = [];
  const row = collectDataset(input).publicJSON.rows[0]; assert.equal(row.started, false); assert.equal(row.physicalAttempts, null); assert.equal(row.trialEffort.measuredSeconds, 30); assert.equal(row.trialEffort.totalSeconds, null);
});
