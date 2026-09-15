// Invented public task. Pins and advisory data deliberately have no upstream claim.
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { fixture } from './fixture.ts';
import { validateRegistration } from './registration.ts';
import { exact, digest } from './validation.ts';
import { retainEvidence, makeManifest, readEvidence } from './artifacts/index.ts';
import { PINS_CHECK, AUDIT_CHECK, snapshotFile, snapshot } from './checks/fixture.ts';
import { IMAGE, NODE, BINARY_DIGEST, ENVIRONMENT_DIGEST } from './checks/index.ts';
import type { EvidenceStore, EvidenceRef, SnapshotRef, CheckJob, CaseRules } from './execution-contract.ts';

export const CASE = 'synthetic-case01';
export const WRITE_PATHS = ['ci/first.yml', 'ci/second.yml'];
export const PINS = ['actions/checkout@' + '1'.repeat(40), 'actions/setup-node@' + '2'.repeat(40)];
export const BRIEF = 'Replace the two action version tags with the supplied immutable synthetic pins. Change only ci/first.yml and ci/second.yml. Preserve every other byte and mode. Report that the frozen synthetic audit has an inherited finding.';
export const contextFor = (files: { path: string; contentBase64: string }[]) => JSON.stringify({
  task: BRIEF, pins: Object.fromEntries(WRITE_PATHS.map((p, i) => [p, PINS[i]])),
  files: files.filter(f => WRITE_PATHS.includes(f.path)).map(f => ({ path: f.path, text: Buffer.from(f.contentBase64, 'base64').toString('utf8') })),
  inheritedFinding: { id: 'invented-inherited-finding', dependency: 'invented-package', version: '0.0.1' },
});
export const promptFor = (context: string) => BRIEF + '\n\nFrozen public context:\n' + context;

// Break the registration/packet hash cycle with exactly two deferred identities.
export function executionProjection(input: unknown) {
  const registration = validateRegistration(input);
  assert.equal(registration.origin, 'synthetic'); assert.equal(registration.caseId, CASE);
  const { nativeProfileDigest, packetDigest, ...execution } = registration.execution;
  return { ...registration, execution };
}

export async function prepareCase(store: EvidenceStore, sourceCommit: string, sourceManifest: Record<string, string>) {
  const files = [
    ...WRITE_PATHS.map((path, i) => snapshotFile(path, 'steps:\n  - uses: ' + (i ? 'actions/setup-node@v4' : 'actions/checkout@v4') + '\n')),
    snapshotFile('lock.json', '{"dependency":"invented-package","version":"0.0.1"}\n'),
    snapshotFile('toolchain.txt', 'synthetic frozen toolchain\n', 0o755),
    snapshotFile('checks/pins.mjs', PINS_CHECK), snapshotFile('checks/audit.mjs', AUDIT_CHECK),
  ].sort((a, b) => a.path < b.path ? -1 : 1);
  const base = await snapshot(store, files), context = contextFor(files), registration = fixture().registrations[0];
  registration.caseId = CASE; registration.frozenAt = new Date().toISOString();
  registration.fixture = { manifest: base.manifest, startingCommit: sourceCommit, treeDigest: base.treeDigest,
    context: await retainEvidence(store, 'case-context', { schema: 1, context }),
    brief: await retainEvidence(store, 'case-brief', { schema: 1, brief: BRIEF }),
    progressSeed: await retainEvidence(store, 'progress-seed', { schema: 1, notes: [], inheritedFinding: 'invented-inherited-finding' }) };
  registration.paths = { read: makeManifest(files).files.map(f => f.path), write: WRITE_PATHS };
  registration.toolchains = [{ id: 'node', version: '24.21.0', binaryDigest: BINARY_DIGEST, environmentDigest: ENVIRONMENT_DIGEST }];
  const inputs = [snapshotFile('pins.json', JSON.stringify({ files: Object.fromEntries(WRITE_PATHS.map((p, i) => [p, PINS[i]])) })),
    snapshotFile('advisory.json', '{"id":"invented-inherited-finding","dependency":"invented-package","version":"0.0.1"}')];
  const executables: Record<string, EvidenceRef> = {};
  registration.checks = [];
  for (const id of ['pins', 'audit']) {
    const file = files.find(f => f.path === 'checks/' + id + '.mjs')!;
    executables[id] = await retainEvidence(store, 'check-executable', { schema: 1, kind: 'baseline-check-executable', file });
    const input = await retainEvidence(store, 'check-inputs', { schema: 1, kind: 'baseline-check-inputs', files: inputs, executable: { path: file.path, sha256: file.sha256 }, imageDigest: IMAGE });
    registration.checks.push({ id, criterionIds: [id === 'pins' ? 'pins' : 'attribution'], argv: [NODE, '/repo/' + file.path], cwd: '.', toolchainId: 'node', inputs: input, required: true, revisions: 'both' });
  }
  const sources = await retainEvidence(store, 'case-sources', { schema: 1, sourceCommit, files: sourceManifest });
  registration.execution.harness = sources; registration.execution.router = sources;
  registration.execution.namedRoute = 'gpt-6-astra';
  registration.execution.isolation = await retainEvidence(store, 'case-isolation', { schema: 1, image: IMAGE, workerMemoryMiB: 768, checkMemoryMiB: 128, network: 'none' });
  registration.execution.preflight = sources;
  registration.execution.stopPlan = await retainEvidence(store, 'case-stop', { schema: 1, independentContainerStop: true, preserveUnknownState: true, elapsedMs: 900000 });
  registration.execution.packetDigest = registration.execution.nativeProfileDigest = '0'.repeat(64);
  const descriptor = { schema: 1, kind: 'baseline-case-v1', origin: 'synthetic', caseId: CASE,
    projectionDigest: digest(executionProjection(registration)), baseTreeDigest: base.treeDigest,
    contextDigest: createHash('sha256').update(context).digest('hex'), promptDigest: createHash('sha256').update(promptFor(context)).digest('hex'),
    writePaths: WRITE_PATHS, phases: { baseChecksMs: 60000, workerMs: 600000, captureMs: 60000, candidateChecksMs: 60000, exportMs: 30000, stopMs: 30000, guardMs: 60000 } };
  return { registration, descriptor, base, files, context, executables };
}

export function validateDescriptor(value: any) {
  exact(value, 'schema kind origin caseId projectionDigest baseTreeDigest contextDigest promptDigest writePaths phases');
  assert.equal(value.schema, 1); assert.equal(value.kind, 'baseline-case-v1'); assert.equal(value.origin, 'synthetic'); assert.equal(value.caseId, CASE);
  for (const key of ['projectionDigest', 'baseTreeDigest', 'contextDigest', 'promptDigest']) assert.match(value[key], /^[a-f0-9]{64}$/);
  assert.deepEqual(value.writePaths, WRITE_PATHS);
  assert.deepEqual(value.phases, { baseChecksMs: 60000, workerMs: 600000, captureMs: 60000, candidateChecksMs: 60000, exportMs: 30000, stopMs: 30000, guardMs: 60000 });
  return value;
}

export function rulesFor(registration: any): CaseRules {
  validateRegistration(registration);
  return { schema: 1, caseId: registration.caseId, registrationDigest: digest(registration), readPaths: registration.paths.read, writePaths: registration.paths.write };
}
export async function checkJob(store: EvidenceStore, registrationRef: EvidenceRef, snapshot: SnapshotRef, executables: Record<string, EvidenceRef>, checkId: string, revision: 'base' | 'candidate', deadline: number): Promise<CheckJob> {
  const registration = validateRegistration(await readEvidence(store, registrationRef)), check = registration.checks.find((c: any) => c.id === checkId); assert(check);
  return { schema: 1, registration: registrationRef, registrationDigest: digest(registration), snapshot, executable: executables[checkId],
    checkId, revision, argv: check.argv, cwd: check.cwd, inputs: check.inputs, imageDigest: IMAGE, environmentDigest: ENVIRONMENT_DIGEST, deadline, outputBytes: 16384 };
}
