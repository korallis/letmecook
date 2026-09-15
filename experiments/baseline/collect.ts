// Offline record ingestion only. No network, process execution, grants or scopes.
import assert from 'node:assert/strict';
import { constants } from 'node:fs';
import { open, mkdir, lstat } from 'node:fs/promises';
import { dirname, resolve, join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { createHash } from 'node:crypto';
import { parseJSON } from '../inference-boundary/json.ts';
import { exact, list, digest, canonical, time, type Row } from './validation.ts';
import { validateRegistration } from './registration.ts';
import { validateRun, effort, CATEGORIES } from './observations.ts';

function usage(run: Row) {
  const complete = run.usageCoverage === 'complete' && run.usage.length > 0;
  const total = (key: string) => complete && run.usage.every((x: Row) => x[key] !== null && x.confidence !== 'unknown')
    ? run.usage.reduce((sum: number, x: Row) => sum + x[key], 0) : null;
  // Cash is never inferred from subscription-only routing; mixed currencies are not added.
  const currencies = [...new Set(run.usage.filter((u: Row) => u.currency !== null).map((u: Row) => u.currency))];
  return { inputTokens: total('inputTokens'), outputTokens: total('outputTokens'), cachedTokens: total('cachedTokens'),
    incrementalCash: currencies.length === 1 ? total('incrementalCash') : null,
    currency: currencies.length === 1 ? currencies[0] : null,
    confidence: !complete || run.usage.some((u: Row) => u.confidence === 'unknown') ? 'unknown' : run.usage.some((u: Row) => u.confidence === 'estimated') ? 'estimated' : 'observed' };
}

export function collectDataset(input: unknown) {
  exact(input, 'schema registrations runs'); assert.equal(input.schema, 1); list(input.registrations, 1, 100); list(input.runs, 1, 100);
  const registrations = input.registrations.map(validateRegistration), byDigest = new Map(registrations.map(r => [digest(r), r]));
  assert.equal(byDigest.size, registrations.length, 'duplicate_registration');
  const caseKeys = registrations.map(r => r.origin + ':' + r.caseId); assert.equal(new Set(caseKeys).size, caseKeys.length, 'one_record_per_case');
  const runs = input.runs.map((r: unknown) => {
    assert(r && typeof r === 'object' && !Array.isArray(r)); const registration = byDigest.get((r as Row).registrationDigest); assert(registration, 'unregistered_run'); return validateRun(r, registration);
  });
  assert.equal(new Set(runs.map(r => r.runId)).size, runs.length, 'duplicate_run');
  assert.equal(new Set(runs.map(r => r.registrationDigest)).size, runs.length, 'attempts_belong_in_one_case_record');
  assert.equal(runs.length, registrations.length, 'retain_blocked_registered_cases');
  const started = runs.filter(r => r.startedAt !== null).sort((a, b) => time(a.startedAt) - time(b.startedAt));
  assert.equal(new Set(started.map(r => r.scope.id)).size, started.length, 'scope_reused');
  // Cross-run overlap checks include preparation/follow-up and every operator.
  const intervals = runs.flatMap(r => r.operatorIntervals).filter(i => i.start !== null).sort((a, b) => time(a.start) - time(b.start));
  const lastByOperator = new Map<string, Row>();
  for (const interval of intervals) {
    const last = lastByOperator.get(interval.operatorId); if (last) assert(time(interval.start) >= time(last.end), 'overlapping_operator_intervals'); lastByOperator.set(interval.operatorId, interval);
  }
  const violations = new Map<string, string[]>();
  for (let i = 0; i < started.length; i++) {
    const r = started[i], reasons: string[] = [];
    if (started.slice(0, i).some(previous => previous.origin === r.origin && previous.scope.quiescence === 'unknown')) reasons.push('started-after-unknown-original');
    if (started.slice(0, i).some(previous => previous.origin === r.origin && time(previous.endedAt) > time(r.startedAt))) reasons.push('concurrent-case');
    if (r.attempts.some((a: Row, n: number) => n < r.attempts.length - 1 && a.operations.some((o: Row) => o.outcome === 'unknown'))) reasons.push('replacement-after-unknown-original');
    violations.set(r.runId, reasons);
  }
  const rows = runs.map((run, index) => {
    const registration = byDigest.get(run.registrationDigest)!, trial = effort(run), followUp = effort(run, 'follow-up');
    const incident = violations.get(run.runId) ?? [];
    const operatorCap = registration.operatorEffort.maximumActiveSeconds;
    const overruns = { physicalAttempts: run.scope?.physicalAttempts === null || !run.scope ? null : Math.max(0, run.scope.physicalAttempts - 32),
      elapsedMs: run.elapsedMs === null ? null : Math.max(0, run.elapsedMs - 900000),
      operatorSeconds: operatorCap === null || trial.totalSeconds === null ? null : Math.max(0, trial.totalSeconds - operatorCap) };
    const followUpComplete = run.followUp.completedAt !== null;
    // Allowlisted projection: no copied caller IDs, free text, commands, paths, refs or hashes.
    return { record: `case-${String(index + 1).padStart(3, '0')}`, origin: run.origin,
      case: registration.origin === 'operator' ? registration.caseId : 'synthetic', eligibility: registration.eligibility.status, status: run.status,
      started: run.startedAt !== null, outputLabel: run.acceptance.decision,
      acceptedWithinRegisteredBounds: run.status === 'accepted' && incident.length === 0,
      harnessAttempts: run.attempts.length, failedHarnessAttempts: run.attempts.filter((a: Row) => a.status !== 'completed').length,
      physicalAttempts: run.scope?.physicalAttempts ?? null, physicalFailureCount: run.attempts.flatMap((a: Row) => a.operations).filter((o: Row) => o.outcome === 'failure').length,
      unknownOriginal: run.scope ? run.scope.quiescence === 'unknown' : null,
      elapsedMs: run.elapsedMs, waitingMs: run.waitingMs, trialEffort: trial, followUpEffort: followUp,
      knownSecondsByCategory: Object.fromEntries(CATEGORIES.map(category => [category, run.operatorIntervals.filter((i: Row) => i.category === category && i.confidence !== 'unknown').reduce((n: number, i: Row) => n + i.durationSeconds, 0)])),
      checks: Object.fromEntries(['passed', 'failed', 'not-run', 'unknown'].map(status => [status, run.checks.filter((c: Row) => c.status === status).length])),
      usage: usage(run), ownershipCostObservationCount: run.ownershipCosts.length, overruns, sequenceViolations: incident,
      followUpComplete, defects: Object.fromEntries(['critical', 'major', 'minor'].map(severity => [severity, run.followUp.defects.filter((d: Row) => d.severity === severity).length])),
      disputedDefects: run.followUp.defects.filter((d: Row) => d.status === 'disputed').length };
  });
  const cohort = rows.filter(r => r.origin === 'operator'), accepted = cohort.filter(r => r.acceptedWithinRegisteredBounds).length;
  const sum = (phase: 'trialEffort' | 'followUpEffort') => cohort.length && cohort.every(r => r[phase].complete) ? cohort.reduce((n, r) => n + r[phase].totalSeconds!, 0) : null;
  const trialSeconds = sum('trialEffort'), followUpSeconds = sum('followUpEffort');
  const allInSeconds = trialSeconds !== null && followUpSeconds !== null && cohort.every(r => r.outputLabel !== 'accepted' || r.followUpComplete) ? trialSeconds + followUpSeconds : null;
  const publicJSON = { schema: 1, kind: 'baseline-public-measurement', evidenceAuthenticated: false, executionAuthorized: false,
    inputSetCompleteness: 'caller-declared', syntheticExcluded: rows.length - cohort.length,
    cohort: { registered: cohort.length, registeredFeasible: cohort.filter(r => r.eligibility === 'feasible').length,
      infeasible: cohort.filter(r => r.eligibility === 'infeasible').length, needsClarification: cohort.filter(r => r.eligibility === 'needs-clarification').length,
      started: cohort.filter(r => r.started).length, accepted,
      outcomes: Object.fromEntries(['accepted', 'rejected', 'failed', 'budget-exhausted', 'blocked', 'cancelled', 'unknown'].map(status => [status, cohort.filter(r => r.status === status).length])),
      completionRate: cohort.some(r => r.started) ? accepted / cohort.filter(r => r.started).length : null,
      trialSeconds, allInSeconds, minutesPerAcceptedOutcome: accepted && allInSeconds !== null ? allInSeconds / 60 / accepted : null,
      completedQualityComparison: false }, rows };
  const columns = ['record', 'origin', 'case', 'status', 'started', 'output_label', 'accepted_within_bounds', 'harness_attempts', 'physical_attempts', 'elapsed_ms', 'known_measured_seconds', 'estimated_seconds', 'total_trial_seconds', 'effort_confidence', 'follow_up_complete'];
  const cells = rows.map(r => [r.record, r.origin, r.case, r.status, r.started, r.outputLabel, r.acceptedWithinRegisteredBounds, r.harnessAttempts, r.physicalAttempts,
    r.elapsedMs, r.trialEffort.measuredSeconds, r.trialEffort.estimatedSeconds, r.trialEffort.totalSeconds, r.trialEffort.confidence, r.followUpComplete]);
  const csv = [columns, ...cells].map(row => row.map(value => value === null ? '' : String(value)).join(',')).join('\n') + '\n';
  return { privateJSON: { schema: 1, kind: 'retained-baseline-inputs', evidenceAuthenticated: false, registrations, runs }, publicJSON, csv };
}

async function privateDirectory(directory: string) {
  const s = await lstat(directory); assert(s.isDirectory() && !s.isSymbolicLink() && s.uid === process.getuid?.() && (s.mode & 0o077) === 0, 'private_owned_directory_required');
}
export async function readInput(filename: string) {
  await privateDirectory(dirname(filename));
  const file = await open(filename, constants.O_RDONLY | constants.O_NOFOLLOW);
  try {
    const s = await file.stat(); assert(s.isFile() && s.uid === process.getuid?.() && (s.mode & 0o077) === 0 && s.size <= 16 * 1048576, 'private_bounded_input_required');
    const bytes = await file.readFile(); assert(bytes.length <= 16 * 1048576, 'private_bounded_input_required');
    // Reject ambiguous keys/invalid UTF8, then normalize parser-only prototypes.
    return JSON.parse(JSON.stringify(parseJSON(new TextDecoder('utf-8', { fatal: true }).decode(bytes)))) as unknown;
  } finally { await file.close(); }
}
export async function writeExport(input: unknown, output: string, beforeWrite: (name: string) => void = () => {}) {
  const result = collectDataset(input); await privateDirectory(dirname(output));
  await mkdir(output, { mode: 0o700 }); // Exclusive directory; never overwrite/reuse an earlier result.
  const files = { 'private-records.json': canonical(result.privateJSON) + '\n', 'public-summary.json': JSON.stringify(result.publicJSON, null, 2) + '\n', 'public-summary.csv': result.csv };
  const write = async (name: string, contents: string) => {
    beforeWrite(name); const f = await open(join(output, name), constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY | constants.O_NOFOLLOW, 0o600);
    try { await f.writeFile(contents); await f.sync(); } finally { await f.close(); }
  };
  for (const [name, contents] of Object.entries(files)) await write(name, contents);
  const directory = await open(output, 'r');
  try {
    await directory.sync();
    await write('complete.json', canonical({ schema: 1, files: Object.fromEntries(Object.entries(files).map(([name, contents]) => [name, createHash('sha256').update(contents).digest('hex')])) }) + '\n');
    await directory.sync();
  } finally { await directory.close(); }
  const parent = await open(dirname(output), 'r'); try { await parent.sync(); } finally { await parent.close(); }
  return result.publicJSON;
}
if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  try {
    assert.equal(process.argv.length, 4, 'usage_collect_input_output');
    const summary = await writeExport(await readInput(resolve(process.argv[2])), resolve(process.argv[3]));
    console.log(JSON.stringify({ collected: true, operatorCases: summary.cohort.registered, syntheticExcluded: summary.syntheticExcluded, executionAuthorized: false }));
  } catch { console.error('Baseline collection failed; no successful export acknowledged. Retain any partial directory for inspection.'); process.exitCode = 1; }
}
