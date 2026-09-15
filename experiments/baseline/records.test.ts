import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, readFile, writeFile, stat, readdir, rm, symlink, chmod } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createHash } from 'node:crypto';
import { fixture, acceptedFixture, ref } from './fixture.ts';
import { validateRegistration, registrationIdentity } from './registration.ts';
import { collectDataset, readInput, writeExport } from './collect.ts';
import { digest, type Row } from './validation.ts';

function changedRegistration(input: Row, edit: (r: Row) => void) { edit(input.registrations[0]); input.runs[0].registrationDigest = digest(input.registrations[0]); return input; }

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
  assert.deepEqual(row.trialEffort, { measuredSeconds: 30, estimatedSeconds: 12, unknownIntervals: 1, complete: false, totalSeconds: null, confidence: 'unknown' });
  assert.equal(row.usage.inputTokens, null); assert.equal(row.usage.incrementalCash, null); assert.equal(row.outputLabel, null);
  assert.equal(result.publicJSON.syntheticExcluded, 1); assert.equal(result.publicJSON.cohort.registered, 0); assert.equal(result.publicJSON.cohort.completionRate, null); assert.equal(result.publicJSON.cohort.minutesPerAcceptedOutcome, null);
  assert.match(result.csv, /,30,12,,unknown,/); assert(!result.csv.includes('synthetic-run'));
});

test('human-labelled useful failure-attribution can retain failing gates; acceptance is never inferred', () => {
  const result = collectDataset(acceptedFixture()); assert.equal(result.publicJSON.rows[0].acceptedWithinRegisteredBounds, true);
  assert.equal(result.publicJSON.rows[0].checks.failed, 2); assert.equal(result.publicJSON.rows[0].trialEffort.confidence, 'estimated');
  assert.equal(result.publicJSON.rows[0].followUpComplete, false); assert.equal(result.publicJSON.cohort.accepted, 0);
  const bad = fixture(); bad.runs[0].status = 'accepted'; assert.throws(() => collectDataset(bad), /operator_acceptance_required/);
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
  input.registrations.push(second.registrations[0]); input.runs.push(b);
  const rows = collectDataset(input).publicJSON.rows;
  assert(rows[0].sequenceViolations.includes('replacement-after-unknown-original'));
  assert(rows[1].sequenceViolations.includes('started-after-unknown-original')); assert(rows[1].sequenceViolations.includes('concurrent-case'));
  assert.equal(rows[1].outputLabel, 'accepted'); assert.equal(rows[1].acceptedWithinRegisteredBounds, false);
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

test('blocked unstarted case retains preparation overhead and no scope, attempts, acceptance or usage is invented', () => {
  const input = changedRegistration(fixture(), r => { r.eligibility.status = 'needs-clarification'; });
  const r = input.runs[0]; r.status = 'blocked'; r.startedAt = r.endedAt = r.elapsedMs = r.waitingMs = r.scope = null; r.attempts = []; r.checks = [];
  const row = collectDataset(input).publicJSON.rows[0]; assert.equal(row.started, false); assert.equal(row.physicalAttempts, null); assert.equal(row.trialEffort.measuredSeconds, 30); assert.equal(row.trialEffort.totalSeconds, null);
});
