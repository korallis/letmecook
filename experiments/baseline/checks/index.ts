import assert from 'node:assert/strict';
import { mkdtemp, mkdir, writeFile, chmod, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, dirname } from 'node:path';
import { createHash } from 'node:crypto';
import { retainEvidence, readEvidence, readSnapshot, makeManifest } from '../artifacts/index.ts';
import type { EvidenceStore, EvidenceRef, CheckJob, CheckEvidence, SnapshotFile } from '../execution-contract.ts';
import { validateRegistration } from '../registration.ts';
import { exact, digest, id, path, sha, integer, list, reference } from '../validation.ts';
import { IMAGE, NODE, ENVIRONMENT, runContainer, type ContainerResult } from './container.ts';

export { IMAGE, NODE } from './container.ts';
export const ENVIRONMENT_DIGEST = digest(ENVIRONMENT);
export const BINARY_DIGEST = '0f8949d1028f6d61506b2d5bc57e7e6fe893d7b1997509b7847294fc9c616584';
const PROBE = `const fs=require('node:fs'),crypto=require('node:crypto');const h=crypto.createHash('sha256');fs.createReadStream(process.execPath).on('error',()=>process.exit(2)).on('data',d=>h.update(d)).on('end',()=>console.log(JSON.stringify({version:process.versions.node,binaryDigest:h.digest('hex'),environment:{arch:process.arch,platform:process.platform,execPath:process.execPath,env:process.env}})));`;
export class RuntimeFailure extends Error {
  evidence: EvidenceRef; cleanup: boolean;
  constructor(evidence: EvidenceRef, cleanup: boolean) { super('runtime-probe-failed'); this.evidence = evidence; this.cleanup = cleanup; }
}

function bound(deadline: number) { assert(Number.isSafeInteger(deadline) && deadline > Date.now() && deadline <= Date.now() + 60000, 'invalid-check-deadline'); }
function active(deadline: number, signal: AbortSignal) { assert(!signal.aborted, 'aborted'); assert(Date.now() < deadline, 'deadline'); }
function file(value: unknown): asserts value is SnapshotFile {
  exact(value, 'path mode size sha256 contentBase64'); path(value.path); assert(value.mode === 0o644 || value.mode === 0o755, 'invalid-input-mode'); integer(value.size, 0, 65536); sha(value.sha256);
  assert(typeof value.contentBase64 === 'string' && value.contentBase64.length <= 87384, 'invalid-input-bytes');
  const bytes = Buffer.from(value.contentBase64, 'base64'); assert.equal(bytes.toString('base64'), value.contentBase64); assert.equal(bytes.length, value.size);
  assert.equal(createHash('sha256').update(bytes).digest('hex'), value.sha256, 'input-digest-mismatch');
}
async function stage(directory: string, files: SnapshotFile[], deadline: number, signal: AbortSignal) {
  active(deadline, signal);
  await mkdir(directory, { mode: 0o755 });
  for (const f of files) {
    active(deadline, signal);
    const destination = join(directory, f.path); await mkdir(dirname(destination), { recursive: true, mode: 0o755 });
    for (let parent = dirname(destination); parent !== directory; parent = dirname(parent)) await chmod(parent, 0o755);
    await writeFile(destination, Buffer.from(f.contentBase64, 'base64'), { mode: f.mode, flag: 'wx' }); await chmod(destination, f.mode);
  }
  await chmod(directory, 0o755);
}

export async function measureRuntime(store: EvidenceStore, signal: AbortSignal, deadline: number) {
  store = structuredClone(store); bound(deadline);
  const execution = await runContainer([NODE, '-e', PROBE], '/', [], deadline, 8192, signal);
  let measured: any = null;
  try {
    assert(execution.cleanup && execution.reason === null && execution.exitCode === 0, 'runtime-probe-failed');
    measured = JSON.parse(Buffer.from(execution.stdoutBase64, 'base64').toString()); exact(measured, 'version binaryDigest environment');
    assert.equal(measured.version, '24.21.0'); assert.equal(measured.binaryDigest, BINARY_DIGEST); assert.deepEqual(measured.environment, ENVIRONMENT);
  } catch { measured = null; }
  const evidence = await retainEvidence(store, 'check-runtime', { schema: 1, kind: 'baseline-check-runtime', imageDigest: IMAGE, execution, measured });
  if (!measured) throw new RuntimeFailure(evidence, execution.cleanup);
  return { version: measured.version as string, binaryDigest: measured.binaryDigest as string, environmentDigest: digest(measured.environment), imageDigest: IMAGE, evidence };
}

type Faults = { failCleanupVerification?: boolean };
async function execute(job: CheckJob, store: EvidenceStore, signal: AbortSignal, faults: Faults): Promise<CheckEvidence> {
  const startedAt = Date.now(); let staging: string | undefined, execution: ContainerResult | null = null;
  let status: CheckEvidence['status'] = 'not-run', exitCode: number | null = null, reason: string | null = null, cleanup = true, runtime: unknown = null;
  let jobDigest: string | null = null, snapshotDigest = '0'.repeat(64), checkId = 'invalid-check';
  let registrationDigest: string | null = null, revision: CheckJob['revision'] | null = null;
  let runtimeStarted = false;
  try {
    // Freeze caller-owned values before the first asynchronous evidence read.
    job = structuredClone(job); store = structuredClone(store);
    exact(job, 'schema registration registrationDigest checkId revision snapshot executable argv cwd inputs imageDigest environmentDigest deadline outputBytes');
    assert.equal(job.schema, 1); id(job.checkId); checkId = job.checkId; sha(job.registrationDigest); reference(job.registration); reference(job.inputs); reference(job.executable);
    exact(job.snapshot, 'manifest bytes treeDigest'); reference(job.snapshot.manifest); reference(job.snapshot.bytes); sha(job.snapshot.treeDigest); snapshotDigest = job.snapshot.treeDigest;
    registrationDigest = job.registrationDigest;
    assert(job.revision === 'base' || job.revision === 'candidate', 'invalid-check-revision'); revision = job.revision; list(job.argv, 2, 64); job.argv.forEach(value => assert(typeof value === 'string' && value.length <= 4096 && !value.includes('\0'), 'invalid-check-argument'));
    assert(job.cwd === '.' || (path(job.cwd), true)); integer(job.outputBytes, 1, 65536); bound(job.deadline); jobDigest = digest(job);
    assert.equal(job.imageDigest, IMAGE, 'unapproved-check-image'); assert.equal(job.environmentDigest, ENVIRONMENT_DIGEST, 'check-environment-mismatch');
    const registration = validateRegistration(await readEvidence(store, job.registration));
    assert.equal(registration.origin, 'synthetic', 'synthetic-checks-only'); assert.equal(digest(registration), job.registrationDigest, 'check-registration-mismatch');
    const check = registration.checks.find((c: any) => c.id === job.checkId); assert(check, 'undeclared-check');
    assert(check.revisions === 'both' || check.revisions === job.revision, 'undeclared-check-revision');
    assert.deepEqual(job.argv, check.argv, 'check-argv-mismatch'); assert.equal(job.cwd, check.cwd, 'check-cwd-mismatch'); assert.deepEqual(job.inputs, check.inputs, 'check-inputs-mismatch');
    const toolchain = registration.toolchains.find((t: any) => t.id === check.toolchainId);
    assert.equal(toolchain.binaryDigest, BINARY_DIGEST, 'check-binary-mismatch'); assert.equal(toolchain.environmentDigest, ENVIRONMENT_DIGEST, 'check-environment-mismatch'); assert.equal(toolchain.version, '24.21.0', 'check-version-mismatch');
    const executable = await readEvidence(store, job.executable); exact(executable, 'schema kind file'); assert.equal(executable.schema, 1); assert.equal(executable.kind, 'baseline-check-executable'); file(executable.file);
    const inputs = await readEvidence(store, job.inputs); exact(inputs, 'schema kind files executable imageDigest'); assert.equal(inputs.schema, 1); assert.equal(inputs.kind, 'baseline-check-inputs');
    list(inputs.files, 0, 64); inputs.files.forEach(file); makeManifest(inputs.files);
    exact(inputs.executable, 'path sha256'); assert.equal(inputs.executable.path, executable.file.path); assert.equal(inputs.executable.sha256, executable.file.sha256); assert.equal(inputs.imageDigest, IMAGE, 'check-input-image-mismatch');
    assert.equal(job.argv[0], NODE, 'check-node-required'); assert.equal(job.argv[1], '/repo/' + executable.file.path, 'check-executable-mismatch');
    const snapshot = await readSnapshot(store, job.snapshot);
    const base = await readEvidence(store, registration.fixture.manifest); exact(base, 'schema files treeDigest');
    assert.deepEqual(base, makeManifest(base.files), 'check-base-manifest-mismatch'); assert.equal(base.treeDigest, registration.fixture.treeDigest, 'check-base-tree-mismatch');
    assert.deepEqual(snapshot.manifest.files.map(f => [f.path, f.mode]), base.files.map(f => [f.path, f.mode]), 'check-snapshot-inventory-mismatch');
    assert(snapshot.manifest.files.every((f, i) => (f.sha256 === base.files[i].sha256 && f.size === base.files[i].size) || registration.paths.write.includes(f.path)), 'check-undeclared-write');
    assert.deepEqual(snapshot.bytes.files.find((f: SnapshotFile) => f.path === executable.file.path), executable.file, 'check-snapshot-executable-mismatch');
    assert(snapshot.bytes.files.every((f: SnapshotFile) => registration.paths.read.includes(f.path)), 'undeclared-snapshot-path');
    assert(job.cwd === '.' || snapshot.bytes.files.some((f: SnapshotFile) => f.path.startsWith(job.cwd + '/')), 'check-cwd-missing');
    if (job.revision === 'base') assert.equal(job.snapshot.treeDigest, registration.fixture.treeDigest, 'check-base-mismatch');
    active(job.deadline, signal); runtimeStarted = true; runtime = await measureRuntime(store, signal, job.deadline); active(job.deadline, signal);
    staging = await mkdtemp(join(tmpdir(), 'gaffer-baseline-check-'));
    await stage(join(staging, 'repo'), snapshot.bytes.files, job.deadline, signal); await stage(join(staging, 'inputs'), inputs.files, job.deadline, signal);
    execution = await runContainer(job.argv, job.cwd === '.' ? '/repo' : '/repo/' + job.cwd, [{ source: join(staging, 'repo'), target: '/repo' }, { source: join(staging, 'inputs'), target: '/inputs' }], job.deadline, job.outputBytes, signal, faults.failCleanupVerification);
    cleanup = execution.cleanup; reason = execution.reason; exitCode = execution.exitCode;
    status = reason !== null || !cleanup || exitCode === null ? 'unknown' : exitCode === 0 ? 'passed' : 'failed';
  } catch (error) {
    // A probe whose evidence could not be retained cannot become an acknowledged
    // not-run result: its cleanup and diagnostics would otherwise be lost.
    if (runtimeStarted && runtime === null && !(error instanceof RuntimeFailure)) throw error;
    reason = error instanceof Error && /^[a-z-]+$/.test(error.message) ? error.message : 'check-admission-failed';
    if (error instanceof RuntimeFailure) { runtime = { evidence: error.evidence, failed: true }; cleanup = error.cleanup; if (!cleanup) status = 'unknown'; }
  }
  finally {
    if (staging) try { await rm(staging, { recursive: true, force: true }); } catch { cleanup = false; reason ??= 'check-staging-cleanup-unconfirmed'; status = 'unknown'; }
  }
  const observation = await retainEvidence(store, 'check-observation', { schema: 1, kind: 'baseline-check-observation', jobDigest, registrationDigest,
    checkId, revision, snapshotDigest, startedAt, endedAt: Date.now(), status, exitCode, reason, cleanup, runtime, execution, stagingDirectory: staging ?? null,
    executionAuthorized: false, realAdvisoryAudit: false, upstreamCI: false });
  return { observation, status, exitCode, snapshotDigest, checkId, cleanup };
}

export const runCheck = (job: CheckJob, store: EvidenceStore, signal: AbortSignal) => execute(job, store, signal, {});
// Test-only lost cleanup acknowledgement. Removal still runs; this hook can only
// turn a result into a failure and cannot relax commands, limits or isolation.
export const runCheckWithCleanupFailure = (job: CheckJob, store: EvidenceStore, signal: AbortSignal) => execute(job, store, signal, { failCleanupVerification: true });
