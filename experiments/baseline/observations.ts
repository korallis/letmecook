// Validate supplied observations without executing checks or authenticating labels.
import assert from 'node:assert/strict';
import { exact, list, text, id, sha, number, integer, oneOf, time, reference, nullableReference, references, unique, digest, type Row } from './validation.ts';
import { validateRegistration } from './registration.ts';

export const CATEGORIES = ['capture', 'planning', 'approval', 'review', 'correction', 'recovery', 'setup'] as const;
export const STATUSES = ['accepted', 'rejected', 'failed', 'budget-exhausted', 'blocked', 'cancelled', 'unknown'] as const;

export function effort(run: Row, phase: 'trial' | 'follow-up' = 'trial') {
  const rows = run.operatorIntervals.filter((x: Row) => x.phase === phase);
  const measuredSeconds = rows.filter((x: Row) => x.confidence === 'measured').reduce((n: number, x: Row) => n + x.durationSeconds, 0);
  const estimatedSeconds = rows.filter((x: Row) => x.confidence === 'estimated').reduce((n: number, x: Row) => n + x.durationSeconds, 0);
  const complete = rows.length > 0 && run.operatorCoverage[phase] === 'complete' && !rows.some((x: Row) => x.confidence === 'unknown');
  return { measuredSeconds, estimatedSeconds, unknownIntervals: rows.filter((x: Row) => x.confidence === 'unknown').length,
    complete, totalSeconds: complete ? measuredSeconds + estimatedSeconds : null,
    confidence: !complete ? 'unknown' : rows.some((x: Row) => x.confidence === 'estimated') ? 'estimated' : 'measured' };
}

export function validateRun(input: unknown, frozen: unknown): Row {
  const registration = validateRegistration(frozen);
  exact(input, 'schema kind origin runId registrationDigest registrationCommit status startedAt endedAt elapsedMs waitingMs scope attempts checks operatorCoverage operatorIntervals usageCoverage usage ownershipCosts rawObservations acceptance followUp');
  assert.equal(input.schema, 1); assert.equal(input.kind, 'baseline-observation'); assert.equal(input.origin, registration.origin);
  id(input.runId); sha(input.registrationCommit, 40); assert.equal(input.registrationDigest, digest(registration), 'registration_digest_mismatch');
  oneOf(input.status, STATUSES); references(input.rawObservations, 1);
  const started = input.startedAt !== null;
  if (started) {
    assert.equal(registration.eligibility.status, 'feasible', 'unregistered_execution_eligibility');
    const start = time(input.startedAt), end = time(input.endedAt); assert(end >= start && start >= time(registration.frozenAt), 'run_time_order');
    if (input.elapsedMs !== null) number(input.elapsedMs); if (input.waitingMs !== null) { number(input.waitingMs); if (input.elapsedMs !== null) assert(input.waitingMs <= input.elapsedMs, 'waiting_exceeds_elapsed'); }
    exact(input.scope, 'id startedAt deadlineAt physicalAttempts operationsComplete quiescence evidence'); id(input.scope.id);
    assert.equal(input.scope.startedAt, input.startedAt); assert.equal(time(input.scope.deadlineAt) - start, 900000, 'scope_deadline_changed');
    if (input.scope.physicalAttempts !== null) integer(input.scope.physicalAttempts);
    assert.equal(typeof input.scope.operationsComplete, 'boolean'); oneOf(input.scope.quiescence, ['confirmed', 'unknown']); reference(input.scope.evidence);
  } else {
    assert.equal(input.status, 'blocked'); for (const k of ['endedAt', 'elapsedMs', 'waitingMs', 'scope']) assert.equal(input[k], null, 'unstarted_observation');
  }
  list(input.attempts, started ? 1 : 0, 128); unique(input.attempts); const operations: Row[] = [];
  for (const a of input.attempts) {
    exact(a, 'id startedAt endedAt status exitCode invocation progressNotes artifact observations operations');
    assert(started, 'unstarted_attempt'); assert(time(a.startedAt) >= time(input.startedAt) && time(a.endedAt) >= time(a.startedAt) && time(a.endedAt) <= time(input.endedAt), 'attempt_time_order');
    oneOf(a.status, ['completed', 'failed', 'cancelled', 'unknown', 'budget-exhausted']); if (a.exitCode !== null) integer(a.exitCode, 0, 255);
    if (a.status === 'completed') assert.equal(a.exitCode, 0, 'completed_attempt_exit');
    reference(a.invocation); reference(a.progressNotes); nullableReference(a.artifact); references(a.observations, 1);
    list(a.operations, 0, 4096);
    for (const o of a.operations) {
      exact(o, 'id source outcome evidence'); id(o.id); oneOf(o.source, ['original', 'router-fallback']); oneOf(o.outcome, ['success', 'failure', 'unknown']); reference(o.evidence); operations.push(o);
    }
  }
  unique(operations);
  const attemptsByTime = [...input.attempts].sort((a: Row, b: Row) => time(a.startedAt) - time(b.startedAt));
  assert.deepEqual(input.attempts.map((a: Row) => a.id), attemptsByTime.map((a: Row) => a.id), 'attempt_order');
  for (let i = 1; i < attemptsByTime.length; i++) assert(time(attemptsByTime[i].startedAt) >= time(attemptsByTime[i - 1].endedAt), 'overlapping_harness_attempts');
  if (started) {
    if (input.scope.operationsComplete) assert.equal(input.scope.physicalAttempts, operations.length, 'physical_count_mismatch');
    else if (input.scope.physicalAttempts !== null) assert(input.scope.physicalAttempts >= operations.length, 'physical_count_underreported');
    if (operations.some(o => o.outcome === 'unknown') || !input.scope.operationsComplete) assert.equal(input.scope.quiescence, 'unknown', 'unknown_original_cannot_be_quiet');
  }
  list(input.checks, 0, 4096); unique(input.checks);
  for (const c of input.checks) {
    exact(c, 'id checkId attemptId revision status exitCode evidence');
    const declared = registration.checks.find((x: Row) => x.id === c.checkId); assert(declared, 'undeclared_check');
    assert(c.attemptId === null || input.attempts.some((a: Row) => a.id === c.attemptId), 'unknown_check_attempt');
    oneOf(c.revision, declared.revisions === 'both' ? ['base', 'candidate'] : [declared.revisions]);
    oneOf(c.status, ['passed', 'failed', 'not-run', 'unknown']); reference(c.evidence);
    if (c.status === 'passed') assert.equal(c.exitCode, 0, 'passing_exit_required');
    else if (c.status === 'failed') integer(c.exitCode, 1, 255);
    else assert.equal(c.exitCode, null, 'unknown_check_exit');
  }
  exact(input.operatorCoverage, 'trial follow-up'); Object.values(input.operatorCoverage).forEach(x => oneOf(x, ['complete', 'incomplete']));
  list(input.operatorIntervals, 0, 10000); unique(input.operatorIntervals);
  for (const interval of input.operatorIntervals) {
    exact(interval, 'id operatorId category phase start end durationSeconds confidence source'); id(interval.operatorId);
    oneOf(interval.category, CATEGORIES); oneOf(interval.phase, ['trial', 'follow-up']); oneOf(interval.confidence, ['measured', 'estimated', 'unknown']); reference(interval.source);
    if (interval.confidence === 'unknown') { assert.equal(interval.start, null); assert.equal(interval.end, null); assert.equal(interval.durationSeconds, null); }
    else {
      number(interval.durationSeconds);
      if (interval.start !== null || interval.end !== null || interval.confidence === 'measured') {
        const seconds = (time(interval.end) - time(interval.start)) / 1000; assert(seconds >= 0, 'interval_time_order');
        if (interval.confidence === 'measured') assert.equal(interval.durationSeconds, seconds, 'measured_timer_mismatch');
      }
    }
  }
  oneOf(input.usageCoverage, ['complete', 'incomplete']); list(input.usage, 0, 10000);
  for (const u of input.usage) {
    exact(u, 'attemptId scope confidence source inputTokens outputTokens cachedTokens incrementalCash currency');
    assert(u.attemptId === null || input.attempts.some((a: Row) => a.id === u.attemptId), 'unknown_usage_attempt');
    oneOf(u.scope, ['attempt', 'planning', 'review', 'setup']); oneOf(u.confidence, ['observed', 'estimated', 'unknown']); reference(u.source);
    for (const k of ['inputTokens', 'outputTokens', 'cachedTokens', 'incrementalCash']) {
      if (u[k] !== null) { number(u[k]); if (k !== 'incrementalCash') integer(u[k]); }
      if (u.confidence === 'unknown') assert.equal(u[k], null, 'unknown_usage_is_null');
    }
    if (u.inputTokens !== null && u.cachedTokens !== null) assert(u.cachedTokens <= u.inputTokens, 'cached_exceeds_input');
    if (u.incrementalCash === null) assert.equal(u.currency, null); else oneOf(u.currency, ['GBP', 'USD', 'EUR']);
  }
  list(input.ownershipCosts, 0, 128);
  for (const c of input.ownershipCosts) {
    exact(c, 'kind amount currency confidence allocation source'); oneOf(c.kind, ['subscription', 'hardware-electricity']); oneOf(c.confidence, ['observed', 'estimated', 'unknown']); reference(c.source); reference(c.allocation);
    if (c.amount === null) assert.equal(c.currency, null); else { number(c.amount); oneOf(c.currency, ['GBP', 'USD', 'EUR']); }
    if (c.confidence === 'unknown') assert.equal(c.amount, null);
  }
  const a = input.acceptance; exact(a, 'decision operatorId labelledAt source criteria scopeBreach artifact');
  list(a.criteria, 0, 64); unique(a.criteria);
  for (const c of a.criteria) { exact(c, 'id met evidence'); assert(registration.criteria.some((r: Row) => r.id === c.id), 'unknown_label_criterion'); oneOf(c.met, [true, false, null]); nullableReference(c.evidence); if (c.met !== null) reference(c.evidence); }
  if (a.decision === null) {
    for (const k of ['operatorId', 'labelledAt', 'source', 'scopeBreach', 'artifact']) assert.equal(a[k], null, 'missing_human_label'); assert.deepEqual(a.criteria, []);
  } else {
    oneOf(a.decision, ['accepted', 'rejected']); id(a.operatorId); reference(a.source); assert(started && time(a.labelledAt) >= time(input.endedAt), 'label_before_result');
    assert.equal(typeof a.scopeBreach, 'boolean'); reference(a.artifact); assert.equal(a.criteria.length, registration.criteria.length, 'incomplete_criteria_label');
    if (a.decision === 'accepted') { assert.equal(a.scopeBreach, false); assert(a.criteria.every((c: Row) => c.met === true), 'accepted_criterion_unmet'); }
  }
  exact(input.followUp, 'startedAt dueAt completedAt evidence defects'); list(input.followUp.defects, 0, 10000); unique(input.followUp.defects);
  if (input.followUp.startedAt === null) {
    for (const k of ['dueAt', 'completedAt', 'evidence']) assert.equal(input.followUp[k], null); assert.deepEqual(input.followUp.defects, []);
  } else {
    assert.equal(a.decision, 'accepted'); assert(time(input.followUp.startedAt) >= time(a.labelledAt));
    assert.equal(time(input.followUp.dueAt) - time(input.followUp.startedAt), 7 * 86400000, 'seven_day_window_required');
    if (input.followUp.completedAt !== null) { assert(time(input.followUp.completedAt) >= time(input.followUp.dueAt), 'followup_not_finished'); reference(input.followUp.evidence); }
    else nullableReference(input.followUp.evidence);
  }
  for (const d of input.followUp.defects) { exact(d, 'id severity status source'); oneOf(d.severity, ['critical', 'major', 'minor']); oneOf(d.status, ['open', 'resolved', 'disputed']); reference(d.source); }
  if (input.status === 'accepted') {
    assert.equal(a.decision, 'accepted', 'operator_acceptance_required'); assert(started); assert.equal(input.scope.quiescence, 'confirmed'); assert.equal(input.scope.operationsComplete, true);
    assert(input.scope.physicalAttempts > 0 && input.scope.physicalAttempts <= 32 && input.elapsedMs !== null && input.elapsedMs <= 900000 && time(input.endedAt) - time(input.startedAt) <= 900000, 'accepted_outside_bounds');
    assert(input.attempts.at(-1)?.status === 'completed', 'accepted_without_completed_attempt');
    assert.deepEqual(input.attempts.at(-1)?.artifact, a.artifact, 'accepted_artifact_mismatch');
    for (const check of registration.checks.filter((c: Row) => c.required)) for (const revision of check.revisions === 'both' ? ['base', 'candidate'] : [check.revisions]) {
      assert(input.checks.some((c: Row) => c.checkId === check.id && c.revision === revision), 'required_check_observation_missing');
    }
    const e = effort(input); if (registration.operatorEffort.treatment === 'cap-including-preparation') assert(e.totalSeconds !== null && e.totalSeconds <= registration.operatorEffort.maximumActiveSeconds, 'accepted_effort_bound_unknown_or_exhausted');
  }
  if (input.status === 'rejected') assert.equal(a.decision, 'rejected', 'operator_rejection_required');
  return structuredClone(input);
}
