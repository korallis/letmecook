import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, chmod, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { readEvidence, retainEvidence, readSnapshot } from '../artifacts/index.ts';
import { digest } from '../validation.ts';
import type { EvidenceStore, CheckJob } from '../execution-contract.ts';
import { runCheck, runCheckWithCleanupFailure, measureRuntime, IMAGE, BINARY_DIGEST, ENVIRONMENT_DIGEST } from './index.ts';
import { docker } from './container.ts';
import { checkFixture, AUDIT_CHECK, snapshotFile, snapshot } from './fixture.ts';

const signal = () => new AbortController().signal;
async function inStore(body: (store: EvidenceStore) => Promise<void>) {
  const directory = await mkdtemp(join(tmpdir(), 'gaffer-baseline-check-test-')); await chmod(directory, 0o700);
  try { await body({ directory }); } finally { await rm(directory, { recursive: true, force: true }); }
}
async function observation(store: EvidenceStore, result: Awaited<ReturnType<typeof runCheck>>) { return await readEvidence(store, result.observation) as any; }
async function absent(id: string) { const result = await docker(['inspect', id], Date.now() + 5000); assert.equal(result.code, 1); assert.match(result.stderr.toString(), /no such (object|container)/i); }

test('native Docker CLI output capture bounds stdout and stderr and preserves command failure', async () => {
  const help = await docker(['--help'], Date.now() + 5000, 64);
  assert.equal(help.reason, 'output-limit'); assert.equal(help.stdout.length + help.stderr.length, 64);
  const failure = await docker(['gaffer-invalid-command'], Date.now() + 5000, 4096);
  assert.notEqual(failure.code, 0); assert.equal(failure.reason, null); assert.match(failure.stderr.toString(), /gaffer-invalid-command/);
  const limitedError = await docker(['gaffer-invalid-command'], Date.now() + 5000, 16);
  assert.equal(limitedError.reason, 'output-limit'); assert.equal(limitedError.stdout.length + limitedError.stderr.length, 16);
});

test('actual pinned-image runtime measurement is retained and uses the 128MiB isolated profile', async () => inStore(async store => {
  const measured = await measureRuntime(store, signal(), Date.now() + 10000); assert.equal(measured.version, '24.21.0'); assert.equal(measured.binaryDigest, BINARY_DIGEST); assert.equal(measured.environmentDigest, ENVIRONMENT_DIGEST); assert.equal(measured.imageDigest, IMAGE);
  const evidence = await readEvidence(store, measured.evidence) as any; assert.equal(evidence.execution.inspection.memory, 134217728); assert.equal(evidence.execution.inspection.network, 'none'); assert.equal(evidence.execution.cleanup, true); await absent(evidence.execution.id);
}));

test('real base/candidate pin checks retain their actual result and frozen synthetic audit failure stays inherited', async t => inStore(async store => {
  for (const [code, checkId] of [[undefined, 'pins'], [AUDIT_CHECK, 'audit']] as const) {
    const f = await checkFixture(store, code, { checkId });
    for (const revision of ['base', 'candidate'] as const) await t.test(`${checkId}-${revision}`, async () => {
      const result = await runCheck({ ...f.job, revision, snapshot: f[revision], deadline: Date.now() + 10000 }, store, signal());
      assert.equal(result.status, checkId === 'pins' && revision === 'candidate' ? 'passed' : 'failed'); assert.equal(result.exitCode, result.status === 'passed' ? 0 : 1); assert.equal(result.cleanup, true);
      const retained = await observation(store, result); assert.equal(retained.snapshotDigest, f[revision].treeDigest); assert.equal(retained.realAdvisoryAudit, false); assert.equal(retained.upstreamCI, false);
      const stdout = JSON.parse(Buffer.from(retained.execution.stdoutBase64, 'base64').toString()); if (checkId === 'audit') { assert.equal(stdout.inherited, true); assert.equal(stdout.realAudit, false); }
      await absent(retained.execution.id);
    });
  }
}));

test('tampered job/image/toolchain/input/executable/snapshot and non-synthetic registration refuse before container start', async () => inStore(async store => {
  const f = await checkFixture(store);
  const edits: ((job: CheckJob) => void)[] = [j => { j.argv = [j.argv[0], '-e', 'process.exit(0)']; }, j => { j.cwd = '../'; }, j => { j.imageDigest = 'sha256:' + '0'.repeat(64); },
    j => { j.environmentDigest = '0'.repeat(64); }, j => { j.registrationDigest = '0'.repeat(64); }, j => { j.checkId = 'missing'; }, j => { j.outputBytes = 65537; },
    j => { j.deadline = Number.POSITIVE_INFINITY; }, j => { j.deadline = Date.now() - 1; }, j => { j.snapshot.treeDigest = '0'.repeat(64); }, j => { j.executable.sha256 = '0'.repeat(64); }, j => { j.inputs.sha256 = '0'.repeat(64); }];
  for (const edit of edits) { const job = structuredClone(f.job); job.deadline = Date.now() + 10000; edit(job); const result = await runCheck(job, store, signal()); assert.equal(result.status, 'not-run'); const retained = await observation(store, result); assert.equal(retained.execution, null); }
  for (const change of [(r: any) => { r.toolchains[0].binaryDigest = '0'.repeat(64); }, (r: any) => { r.toolchains[0].version = '0.0.0'; },
    (r: any) => { r.toolchains[0].environmentDigest = '0'.repeat(64); }, (r: any) => { r.checks[0].revisions = 'candidate'; }, (r: any) => { r.origin = 'operator'; }]) {
    const registration = structuredClone(f.registration); change(registration); const job = { ...f.job, registration: await retainEvidence(store, 'changed-registration', registration), registrationDigest: digest(registration), deadline: Date.now() + 10000 };
    const result = await runCheck(job, store, signal()); assert.equal(result.status, 'not-run'); assert.equal((await observation(store, result)).execution, null);
  }
  const modified = await snapshot(store, [snapshotFile('checks/pins.mjs', 'process.exit(0)')]);
  const result = await runCheck({ ...f.job, revision: 'candidate', snapshot: modified, deadline: Date.now() + 10000 }, store, signal()); assert.equal(result.status, 'not-run'); assert.equal((await observation(store, result)).execution, null);
}));

test('admission rejects malformed jobs, full-inventory drift, undeclared edits and malformed frozen inputs', async () => inStore(async store => {
  const f = await checkFixture(store), { bytes } = await readSnapshot(store, f.candidate);
  async function denied(job: unknown) {
    const result = await runCheck(job as CheckJob, store, signal()); assert.equal(result.status, 'not-run'); assert.equal(result.cleanup, true);
    const retained = await observation(store, result); assert.equal(retained.execution, null); assert.equal(retained.runtime, null);
  }
  for (const job of [null, undefined, [], {}, { ...f.job, schema: 2 }]) await denied(job);
  const variants = [bytes.files.slice(1), [...bytes.files, snapshotFile('extra.txt', 'extra')],
    bytes.files.map(file => file.path === 'lock.json' ? snapshotFile(file.path, 'changed') : file),
    bytes.files.map(file => file.path === 'ci/first.yml' ? { ...file, mode: 0o755 as const } : file)];
  for (const files of variants) await denied({ ...f.job, revision: 'candidate', snapshot: await snapshot(store, files), deadline: Date.now() + 10000 });
  await denied({ ...f.job, snapshot: f.candidate, deadline: Date.now() + 10000 });
  const original = await readEvidence(store, f.job.inputs) as any;
  for (const change of [(v: any) => { v.imageDigest = 'sha256:' + '0'.repeat(64); }, (v: any) => { v.executable.sha256 = '0'.repeat(64); },
    (v: any) => { v.files[0].contentBase64 = 'bad'; }, (v: any) => { v.files = [snapshotFile('x', ''), snapshotFile('x/y', '')]; },
    (v: any) => { v.files = [snapshotFile('X', ''), snapshotFile('x', '')]; }]) {
    const changed = structuredClone(original); change(changed); const inputs = await retainEvidence(store, 'changed-inputs', changed);
    const registration = structuredClone(f.registration); registration.checks[0].inputs = inputs;
    await denied({ ...f.job, inputs, registration: await retainEvidence(store, 'changed-registration', registration), registrationDigest: digest(registration), deadline: Date.now() + 10000 });
  }
  const registration = structuredClone(f.registration); registration.checks[0].argv = [f.job.argv[0], '-e', 'process.exit(0)'];
  await denied({ ...f.job, argv: registration.checks[0].argv, registration: await retainEvidence(store, 'changed-registration', registration), registrationDigest: digest(registration), deadline: Date.now() + 10000 });
}));

test('admission freezes caller job/store before async reads and pre-abort starts no container', async () => inStore(async store => {
  const f = await checkFixture(store), original = structuredClone(f.job), suppliedStore = { ...store }, controller = new AbortController(); controller.abort();
  const pending = runCheck(f.job, suppliedStore, controller.signal);
  f.job.argv[1] = '-e'; f.job.checkId = 'changed'; f.job.snapshot.treeDigest = '0'.repeat(64); f.job.deadline = Date.now() + 60000; suppliedStore.directory = '/invalid';
  const result = await pending, retained = await observation(store, result);
  assert.equal(result.status, 'not-run'); assert.equal(result.cleanup, true); assert.equal(retained.reason, 'aborted'); assert.equal(retained.runtime, null); assert.equal(retained.execution, null);
  assert.equal(retained.jobDigest, digest(original)); assert.equal(result.checkId, original.checkId); assert.equal(result.snapshotDigest, original.snapshot.treeDigest);
}));

test('actual check cannot write source/inputs/root or reach egress/control/provider/Docker sockets', async () => inStore(async store => {
  const code = `import fs from 'node:fs';import net from 'node:net';import assert from 'node:assert/strict';
for(const p of ['/repo/lock.json','/inputs/pins.json','/etc/gaffer-probe']) assert.throws(()=>fs.writeFileSync(p,'bad'),e=>['EROFS','EACCES','EPERM'].includes(e.code));
fs.writeFileSync('/tmp/probe','scratch');assert.equal(fs.readFileSync('/tmp/probe','utf8'),'scratch');
for(const p of ['/var/run/docker.sock','/run/gaffer-control.sock','/control','/provider']) assert.equal(fs.existsSync(p),false);
assert.equal(process.getuid(),65532);assert.equal(fs.readFileSync('/sys/fs/cgroup/memory.max','utf8').trim(),'134217728');
assert.equal(fs.statSync('/repo/toolchain.txt').mode&0o777,0o755);assert.equal(fs.statSync('/repo/lock.json').mode&0o777,0o644);
assert.deepEqual(Object.keys(process.env).sort(),['HOME','HOSTNAME','NODE_OPTIONS','NODE_VERSION','PATH','YARN_VERSION']);
for(const host of ['1.1.1.1','127.0.0.1']) await new Promise((resolve,reject)=>{const s=net.connect({host,port:443});s.setTimeout(500,()=>{s.destroy();resolve()});s.on('error',()=>resolve());s.on('connect',()=>{s.destroy();reject(Error('unexpected-network'))})});
console.log('readonly-egress-control-isolation-confirmed');`;
  const f = await checkFixture(store, code); const oldMask = process.umask(0o077); let result;
  try { result = await runCheck(f.job, store, signal()); } finally { process.umask(oldMask); }
  assert.equal(result.status, 'passed'); const retained = await observation(store, result);
  assert.equal(retained.execution.inspection.network, 'none'); assert.deepEqual(retained.execution.inspection.mounts.toSorted((a: any, b: any) => a.target.localeCompare(b.target)), [{ target: '/inputs', readonly: true }, { target: '/repo', readonly: true }]);
  assert.match(Buffer.from(retained.execution.stdoutBase64, 'base64').toString(), /isolation-confirmed/); await absent(retained.execution.id);
}));

test('absolute timeout, output flood, descendant work and abnormal exit retain bounded failure logs and remove the whole container', async t => inStore(async store => {
  const cases = [
    { name: 'timeout', code: `console.error('timeout-started');setInterval(()=>{},1000);`, milliseconds: 5000, limit: 1024, reason: 'deadline' },
    { name: 'flood', code: `console.error('flood-started');for(let i=0;i<10000;i++)process.stdout.write('x'.repeat(1024));setInterval(()=>{},1000);`, milliseconds: 10000, limit: 4096, reason: 'output-limit' },
    { name: 'descendants', code: `import{spawn}from'node:child_process';const c=spawn(process.execPath,['-e','setInterval(()=>{},1000)'],{detached:true,stdio:'ignore'});c.unref();console.error('descendant-started');setInterval(()=>{},1000);`, milliseconds: 5000, limit: 1024, reason: 'deadline' },
    { name: 'abnormal', code: `console.error('abnormal-started');process.kill(process.pid,'SIGKILL');`, milliseconds: 10000, limit: 1024, reason: null },
  ];
  for (const c of cases) await t.test(c.name, async () => {
    const f = await checkFixture(store, c.code, { deadlineMs: c.milliseconds, outputBytes: c.limit }); const result = await runCheck(f.job, store, signal());
    assert.equal(result.status, c.name === 'abnormal' ? 'failed' : 'unknown'); assert.equal(result.cleanup, true); const retained = await observation(store, result);
    assert(retained.execution, c.name); assert(retained.execution.outputBytes <= c.limit); assert.equal(retained.execution.reason, c.reason); if (c.name === 'abnormal') assert.equal(result.exitCode, 137);
    assert.match(Buffer.from(retained.execution.stderrBase64, 'base64').toString() + Buffer.from(retained.execution.stdoutBase64, 'base64').toString(), /started|x/); await absent(retained.execution.id);
    assert(retained.execution.startedAt < f.job.deadline); assert(retained.execution.endedAt <= f.job.deadline + 6000);
  });
}));

test('abort and lost cleanup acknowledgement never become passed and diagnostics remain retained', async t => inStore(async store => {
  await t.test('abort', async () => {
  const f = await checkFixture(store, `console.error('started');setInterval(()=>{},1000);`), controller = new AbortController(); const timer = setTimeout(() => controller.abort(), 5000);
  try { const result = await runCheck(f.job, store, controller.signal); assert.equal(result.status, 'unknown'); assert.equal(result.cleanup, true); assert.equal((await observation(store, result)).execution.reason, 'aborted'); }
  finally { clearTimeout(timer); }
  });
  await t.test('cleanup acknowledgement', async () => {
  const successful = await checkFixture(store, `console.log('would-pass');`); const failedCleanup = await runCheckWithCleanupFailure(successful.job, store, signal());
  assert.equal(failedCleanup.status, 'unknown'); assert.equal(failedCleanup.cleanup, false); const retained = await observation(store, failedCleanup); assert(retained.execution, 'check-never-started');
  assert.equal(retained.execution.reason, 'container-cleanup-unconfirmed'); assert.equal(retained.execution.exitCode, 0); assert.equal(retained.execution.cleanupVerificationFaultInjected, true);
  assert.match(Buffer.from(retained.execution.stdoutBase64, 'base64').toString(), /would-pass/); await absent(retained.execution.id);
  });
}));
