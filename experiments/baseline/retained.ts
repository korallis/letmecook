// Read-only qualification of the public synthetic case's retained record graph.
import assert from 'node:assert/strict';
import { readEvidence, readSnapshot } from './artifacts/index.ts';
import { validateEvidenceRef } from './artifacts/store.ts';
import { changedPaths, validateSnapshotBytes } from './artifacts/manifest.ts';
import { validateRegistration } from './registration.ts';
import { executionProjection, promptFor, rulesFor, validateDescriptor } from './case01.ts';
import { fixture } from './fixture.ts';
import { digest } from './validation.ts';
import { wirePrompt, validateWorkerInput } from './native/input.ts';
import { verifyBaselineTranscript } from './native/transcript.ts';
import { collectDataset } from './collect.ts';
import type { EvidenceRef, EvidenceStore } from './execution-contract.ts';

export async function validateRetainedRun(store: EvidenceStore, report: any, phase: 'before-ack' | 'ack' | 'replay' = 'ack') {
  report = structuredClone(report);
  assert.equal(report.schema, 1); assert(['two-requests', 'three-requests'].includes(report.scenario)); assert.equal(report.live, false);
  const records = new Map<string, any>();
  async function get(ref: EvidenceRef): Promise<any> {
    validateEvidenceRef(ref);
    if (!records.has(ref.ref)) {
      assert(records.size < 256, 'retained_record_limit');
      records.set(ref.ref, await readEvidence(store, ref));
    }
    return records.get(ref.ref);
  }
  const registration = validateRegistration(await get(report.registration)), declaration = fixture().registrations[0];
  assert.equal(registration.origin, 'synthetic'); assert.equal(registration.caseId, 'synthetic-case01');
  for (const [actual, expected] of [[registration.authority, declaration.authority], [registration.eligibility.source, declaration.eligibility.source], [registration.operatorEffort.source, declaration.operatorEffort.source]]) assert.deepEqual(actual, expected);
  const visited = new Set<string>();
  async function walk(value: any, depth = 0): Promise<void> {
    assert(depth <= 64, 'retained_graph_depth');
    if (!value || typeof value !== 'object') return;
    if (Object.hasOwn(value, 'ref')) {
      const record = await get(value);
      if (!visited.has(value.ref)) { visited.add(value.ref); await walk(record, depth + 1); }
      return;
    }
    // Only these three fields in an actual synthetic declaration are unresolved.
    // A synthetic reference in any required evidence position is still rejected.
    if (value.kind === 'frozen-baseline-declaration') {
      assert.deepEqual(value, registration);
      value = { ...value, authority: null, eligibility: { ...value.eligibility, source: null }, operatorEffort: { ...value.operatorEffort, source: null } };
    }
    for (const item of Object.values(value)) await walk(item, depth + 1);
  }
  await walk(report);
  const packet = await get(report.packet), invocation = await get(report.invocation), observation = await get(report.observation);
  const raw = await get(report.routerEvidence), input = await get(report.verificationInput), transcript = await get(report.transcript);
  const ack = phase === 'before-ack' ? null : await get(report.acknowledgement), bundle = await get(report.candidate);
  assert.equal(packet.packetDigest, digest(packet.packet)); assert.equal(registration.execution.packetDigest, packet.packetDigest);
  assert.equal(registration.execution.nativeProfileDigest, digest(packet.selectedPolicy.native));
  const descriptor = validateDescriptor(packet.packet.baselineCase);
  assert.equal(descriptor.projectionDigest, digest(executionProjection(registration)));
  assert.equal(descriptor.baseTreeDigest, registration.fixture.treeDigest);
  assert.equal(registration.fixture.startingCommit, report.sourceCommit);
  for (const ref of [registration.execution.harness, registration.execution.router, registration.execution.preflight]) {
    const sources = await get(ref); assert.equal(sources.sourceCommit, report.sourceCommit); assert.deepEqual(sources.files, report.sources);
  }
  assert(Date.parse(registration.frozenAt) <= report.scopeStarted.started);
  assert.equal(report.scopeStarted.id, input.scope.id); assert.equal(report.scopeStarted.started, input.scope.started); assert.equal(report.scopeStarted.deadline, input.scope.deadline);
  assert.equal(invocation.token, '<scoped-grant>');
  validateWorkerInput({ ...invocation, token: '0'.repeat(64) }, invocation.deadline - 600000);
  assert.equal(invocation.registrationDigest, digest(registration)); assert.equal(invocation.packetDigest, packet.packetDigest);
  assert.equal(invocation.profileDigest, registration.execution.nativeProfileDigest);
  assert.equal(invocation.context, (await get(registration.fixture.context)).context);
  assert.equal(invocation.prompt, promptFor(invocation.context));
  assert.equal(invocation.contextDigest, descriptor.contextDigest); assert.equal(invocation.promptDigest, descriptor.promptDigest);
  assert.equal(invocation.baseTreeDigest, registration.fixture.treeDigest);
  assert.equal(observation.invocationDigest, digest(invocation));
  for (const key of ['registrationDigest', 'packetDigest', 'bindingDigest', 'settingsDigest']) assert.equal(observation[key], invocation[key]);
  assert.equal(observation.invalid, false); assert.equal(observation.reason, null); assert.deepEqual(observation.rejected, []);
  assert.equal(input.expected.prompt, wirePrompt(invocation.prompt)); assert.deepEqual(input.policy, packet.selectedPolicy);
  assert.equal(input.expected.bindingDigest, invocation.bindingDigest); assert.equal(input.packetDigest, packet.packetDigest);
  for (const key of ['requests', 'events', 'exitCode', 'signal', 'localProcessExited']) assert.deepEqual(input[key], observation[key]);
  for (const [key, field] of Object.entries({ decisions: 'decisions', receipts: 'receipts', pendingReservations: 'reservations', durableDecisionTimes: 'decisionTimes', scope: 'scope' })) assert.deepEqual(input[key], raw.evidence[field]);
  for (const key of ['physicalRequests', 'scopeOperations']) assert.deepEqual(input[key], raw.physical[key]);
  assert.equal(raw.physical.registrationDigest, digest(registration));
  assert.deepEqual(verifyBaselineTranscript(input), transcript);
  assert.equal(transcript.physicalAttempts, report.scenario === 'two-requests' ? 2 : 3);
  assert.equal(bundle.kind, 'baseline-repository-bundle'); assert.equal(bundle.caseId, registration.caseId); assert.equal(bundle.registrationDigest, digest(registration));
  const base = await readSnapshot(store, bundle.base), candidate = await readSnapshot(store, bundle.candidate);
  assert.deepEqual(bundle.base.manifest, registration.fixture.manifest); assert.equal(base.manifest.treeDigest, invocation.baseTreeDigest); assert.deepEqual(base.bytes.files, invocation.files);
  assert.deepEqual(bundle.changedPaths, changedPaths(base.manifest, candidate.manifest, rulesFor(registration)));
  assert.deepEqual([...bundle.changedPaths].sort(), [...registration.paths.write].sort());
  const required = registration.checks.flatMap((c: any) => (c.revisions === 'both' ? ['base', 'candidate'] : [c.revisions]).map((revision: string) => revision + '-' + c.id)).sort();
  assert.deepEqual(report.checks.map(({ revision, check }: any) => revision + '-' + check.checkId).sort(), required);
  for (const { revision, check } of report.checks) {
    const retained = await get(check.observation), declared = registration.checks.find((c: any) => c.id === check.checkId);
    assert.equal(retained.kind, 'baseline-check-observation'); assert.equal(retained.registrationDigest, digest(registration)); assert.equal(retained.revision, revision);
    for (const key of ['checkId', 'snapshotDigest', 'status', 'exitCode', 'cleanup']) assert.equal(retained[key], check[key]);
    assert.equal(retained.snapshotDigest, (revision === 'base' ? base : candidate).manifest.treeDigest);
    assert.equal(retained.cleanup, true); assert.equal(retained.reason, null); assert.equal(retained.execution.cleanup, true); assert.equal(retained.execution.reason, null); assert.equal(retained.execution.exitCode, check.exitCode);
    assert.equal(check.status, check.exitCode === 0 ? 'passed' : 'failed');
    const runtime = await get(retained.runtime.evidence), toolchain = registration.toolchains.find((t: any) => t.id === declared.toolchainId);
    assert.equal(runtime.kind, 'baseline-check-runtime'); assert.equal(runtime.execution.cleanup, true); assert.equal(runtime.execution.reason, null); assert.equal(runtime.execution.exitCode, 0);
    assert.equal(runtime.measured.version, toolchain.version); assert.equal(runtime.measured.binaryDigest, toolchain.binaryDigest); assert.equal(digest(runtime.measured.environment), toolchain.environmentDigest);
    const inputs = await get(declared.inputs), file = base.bytes.files.find(f => '/repo/' + f.path === declared.argv[1]); assert(file);
    assert.deepEqual(inputs.executable, { path: file.path, sha256: file.sha256 }); assert.equal(inputs.imageDigest, runtime.imageDigest);
    // The capture retains the executable's complete bytes in the base snapshot.
    validateSnapshotBytes({ schema: 1, kind: 'baseline-snapshot-bytes', files: [...inputs.files].sort((a, b) => a.path.localeCompare(b.path)) });
  }
  if (ack) {
  assert.equal(ack.kind, 'synthetic-baseline-acknowledgement');
  for (const key of ['registration', 'packet', 'candidate', 'transcript']) assert.deepEqual(ack[key], report[key]);
  assert.equal(ack.registrationCommit, report.registrationCommit); assert.equal(ack.bundleDigest, digest(bundle));
  assert.equal(ack.bindingDigest, invocation.bindingDigest); assert.equal(ack.scopeDigest, digest(input.scope));
  assert.deepEqual(ack.checks, report.checks.map((x: any) => x.check.observation));
  assert.equal(ack.independentlyAccepted, false); assert.equal(ack.published, false);
  }
  if (phase === 'replay') {
    if (report.result === 'passed') {
      assert.equal(report.cleanup, true); assert.equal(report.repositoryDestroyed, true);
      assert(report.cleanupResults.every((result: string) => result === 'fulfilled'));
      assert(!report.error && !report.persistenceFailed, 'failed_finalization');
    }
    const dataset = await get(report.dataset); assert.deepEqual(dataset.registrations, [registration]); assert.equal(dataset.runs.length, 1);
    const record = dataset.runs[0]; assert.equal(record.runId, report.run); assert.equal(record.registrationDigest, digest(registration)); assert.equal(record.registrationCommit, report.registrationCommit);
    assert.equal(record.scope.id, input.scope.id); assert.equal(record.scope.physicalAttempts, transcript.physicalAttempts); assert.deepEqual(record.scope.evidence, report.routerEvidence);
    assert.equal(record.startedAt, new Date(report.scopeStarted.started).toISOString()); assert.equal(record.scope.startedAt, record.startedAt); assert.equal(record.scope.deadlineAt, new Date(report.scopeStarted.deadline).toISOString());
    assert.equal(record.attempts.length, 1); const attempt = record.attempts[0]; assert.equal(attempt.id, input.binding.attemptId);
    assert.deepEqual(attempt.invocation, report.invocation); assert.deepEqual(attempt.artifact, report.candidate); assert.deepEqual(attempt.progressNotes, registration.fixture.progressSeed);
    assert.equal(attempt.startedAt, new Date(observation.startedAt).toISOString()); assert.equal(attempt.endedAt, new Date(observation.endedAt).toISOString()); assert.equal(attempt.exitCode, observation.exitCode);
    assert.deepEqual(attempt.observations, [report.observation, report.routerEvidence]);
    assert.deepEqual(attempt.operations, raw.evidence.receipts.flatMap((r: any) => r.operations.map((op: any) => ({ id: 'operation_' + r.id + '_' + op.ordinal, source: op.ordinal === 1 ? 'original' : 'router-fallback', outcome: op.terminal === 'provider_completed' ? 'success' : op.terminal === 'unknown' ? 'unknown' : 'failure', evidence: report.routerEvidence }))));
    assert.deepEqual(record.checks.map((c: any) => ({ ...c, candidateArtifact: c.candidateArtifact ?? null })), report.checks.map(({ revision, check }: any) => ({ id: revision + '-' + check.checkId, checkId: check.checkId, attemptId: revision === 'base' ? null : input.binding.attemptId, revision, status: check.status, exitCode: check.exitCode, candidateArtifact: revision === 'base' ? null : report.candidate, evidence: check.observation })));
    assert.deepEqual(record.rawObservations, [report.acknowledgement, report.routerEvidence, report.observation]);
    assert.deepEqual(collectDataset(dataset).publicJSON, report.export);
  }
  for (const [ref, record] of records) assert.deepEqual(await readEvidence(store, { ref, sha256: ref.slice(-69, -5) }), record);
}
