import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { mkdtemp, mkdir, copyFile, writeFile, readFile, readdir, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, resolve, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { randomBytes, createHash } from 'node:crypto';
import { setTimeout as delay } from 'node:timers/promises';
import { IMAGE, LABEL, selectedProfile, assertRuntime, cleanupOwned } from '../isolation/profile.ts';
import { harnessContainerArgs, assertHarnessContainer, VARIANT } from './isolation.ts';
import { pins, probe } from './adapter.ts';
import { codecOverlayFiles, stageFiles } from './staging.ts';
const exec = promisify(execFile);
const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..');
const output = resolve(process.argv[2] ?? join(root, 'docs/evidence/first-harness-run.json'));
const binary = process.env.GAFFER_HARNESS_BINARY;
assert(binary, 'Set GAFFER_HARNESS_BINARY to the publicly downloaded pinned Linux arm64 binary; host never executes it');
probe(binary);
const context = process.env.GAFFER_ISOLATION_DOCKER_CONTEXT ?? 'desktop-linux';
assert(context && !context.startsWith('-'));
const runId = 'gaffer-harness-' + randomBytes(6).toString('hex');
const host = await mkdtemp(join(tmpdir(), runId + '-')); const staging = join(host, 'fixture');
const evidence: any = { schema: 1, observedAt: new Date().toISOString(), result: 'blocked', runId, pins, selectedProfile, variant: VARIANT,
  syntheticInferenceOnly: true, liveRouterCalled: false, unattendedSupported: false, independentReview: 'required', sourceDigests: {}, cases: [], cleanup: { verified: false },
  liveBlockers: probe(binary).liveBlockers, limitations: ['This is a synthetic authority, not a live 9Router service or provider cancellation proof.', 'Native JSON CLI events are step projections, not token-stream or interactive approval/resume support.', '128 MiB preparatory worker OOM preceded inference; this explicitly separate 512 MiB variant requires review.'] };
await mkdir(dirname(output), { recursive: true }); await writeFile(output, JSON.stringify(evidence, null, 2) + '\n');
console.log(JSON.stringify({ runId, result: 'blocked-until-proof-and-cleanup' }));
let cleaning = false; const stop = new AbortController();
for (const signal of ['SIGINT', 'SIGTERM']) process.once(signal, () => stop.abort());
let ids: string[] = []; let volume: string | undefined;
async function docker(args: string[], timeout = 15000) {
  if (!cleaning && stop.signal.aborted) throw new Error('interrupted');
  const result = await exec('docker', ['--context', context, ...args], { timeout, maxBuffer: 4 * 1048576,
    signal: !cleaning && ['wait', 'logs', 'inspect', 'top'].includes(args[0]) ? stop.signal : undefined });
  return (result.stdout + (args[0] === 'logs' ? result.stderr : '')).trim();
}
async function inspect(id: string) { return JSON.parse(await docker(['inspect', id]))[0]; }
async function clean() {
  cleaning = true;
  try { await cleanupOwned(ids, volume, docker, runId); ids = []; volume = undefined; } finally { cleaning = false; }
}
async function waitEvent(id: string, type: string, ms = 30000) {
  const deadline = Date.now() + ms;
  while (Date.now() < deadline) {
    if ((await docker(['logs', id])).includes(`"type":"${type}"`)) return;
    if (!(await inspect(id)).State.Running) throw new Error('exited_before_' + type + ': ' + await docker(['logs', id]));
    await delay(100, undefined, { signal: stop.signal });
  }
  throw new Error('event_timeout_' + type);
}
async function create(mode: string, gateway: boolean) {
  const name = `${runId}-${mode}-${gateway ? 'gateway' : 'worker'}`; ids.push(name);
  const args = harnessContainerArgs(name, runId, staging, volume!, gateway);
  args.push(IMAGE, '/usr/bin/env', '-i', 'PATH=/usr/local/bin:/usr/bin:/bin', 'HOME=/work', `HOST_SENTINEL=${host}/sentinel`, 'node', gateway ? '/fixture/tests/fixtures/harness/gateway.ts' : '/fixture/tests/fixtures/harness/worker.ts', mode);
  await docker(args); const state = await inspect(name); assertHarnessContainer(state, staging, volume!, gateway);
  await docker(['start', name]);
  return { name, preflight: { user: state.Config.User, image: state.Config.Image, hostConfig: state.HostConfig, mounts: state.Mounts.map((m: any) => ({ type: m.Type, destination: m.Destination, writable: m.RW })) } };
}
try {
  await mkdir(staging); await writeFile(join(host, 'sentinel'), 'SYNTHETIC_HOST_SENTINEL', { mode: 0o600 });
  await writeFile(join(staging, 'package.json'), '{"type":"module"}\n');
  for (const dir of ['experiments/harness', 'experiments/inference-boundary', 'tests/fixtures/harness']) {
    await mkdir(join(staging, dir), { recursive: true });
    for (const name of (await readdir(join(root, dir))).filter(n => /\.(ts|json)$/.test(n) && !n.endsWith('.test.ts') && n !== 'package-lock.json')) {
      const path = join(dir, name); const bytes = await readFile(join(root, path));
      evidence.sourceDigests[path] = createHash('sha256').update(bytes).digest('hex'); await copyFile(join(root, path), join(staging, path));
    }
  }
  for (const name of ['experiments/harness/isolation.ts', 'experiments/isolation/profile.ts', 'experiments/isolation/profile.json']) evidence.sourceDigests[name] = createHash('sha256').update(await readFile(join(root, name))).digest('hex');
  await stageFiles(root, staging, codecOverlayFiles);
  for (const name of codecOverlayFiles) evidence.sourceDigests[name] = createHash('sha256').update(await readFile(join(root, name))).digest('hex');
  evidence.pureOverlayStagedFiles = codecOverlayFiles;
  await copyFile(binary, join(staging, 'opencode')); probe(join(staging, 'opencode'));
  const version = JSON.parse(await docker(['version', '--format', '{{json .}}'])); const info = JSON.parse(await docker(['info', '--format', '{{json .}}'])); assertRuntime(version, info);
  evidence.runtime = { server: version.Server, cgroup: info.CgroupVersion, securityOptions: info.SecurityOptions, hostNode: process.version };
  await docker(['image', 'inspect', IMAGE]);
  const modes = ['containment', 'ambient-probe', 'ambient-timeout', 'edit', 'ask', 'usage-missing', 'partial', 'forbidden', 'router-error', 'cancel', 'crash', 'tree', 'oom'];
  for (const mode of modes) {
    volume = runId + '-' + mode + '-socket';
    await docker(['volume', 'create', '--label', `${LABEL}=${runId}`, '--driver', 'local', '--opt', 'type=tmpfs', '--opt', 'device=tmpfs', '--opt', 'o=size=1m,uid=1000,gid=1000,mode=0700', volume]);
    const gateway = await create(mode, true); await waitEvent(gateway.name, 'ready', 5000);
    const worker = await create(mode, false);
    if (mode === 'tree') {
      await waitEvent(worker.name, 'finished');
      const before = await docker(['top', worker.name, '-eo', 'pid,ppid,comm']);
      assert(before.split('\n').length >= 5); const started = Date.now(); await docker(['stop', '--timeout', '1', worker.name]);
      evidence.treeTeardown = { before, elapsedMs: Date.now() - started };
    }
    const exit = Number(await docker(['wait', worker.name], 35000)); const state = (await inspect(worker.name)).State;
    evidence.pendingCase = { mode, workerState: state, workerLogs: await docker(['logs', worker.name]), gatewayLogs: await docker(['logs', gateway.name]) };
    assert.equal(state.Running, false); assert.equal(state.Pid, 0);
    const workerEvents = evidence.pendingCase.workerLogs.split('\n').filter((l: string) => l.startsWith('{')).map((l: string) => JSON.parse(l));
    if (mode === 'oom') { assert.equal(state.OOMKilled, true); assert.equal(exit, 137); }
    else if (mode === 'tree') assert.equal(exit, 137);
    else if (mode === 'ambient-timeout') {
      assert.equal(exit, 1, 'forced timeout must fail the worker discovery criterion');
      const actual = workerEvents.find((e: any) => e.type === 'observation')?.data;
      assert(actual, 'failed worker must retain its pre-assertion observation');
      assert.equal(actual.stopReason, 'forced_timeout'); assert.equal(actual.canaryExecutedDespiteFlags, false);
      assert.equal(actual.exitCode, null); assert(['SIGTERM', 'SIGKILL'].includes(actual.signal));
      assert.match(actual.stdout, /forced timeout stdout diagnostic/); assert.match(actual.stderr, /forced timeout stderr diagnostic/);
      assert.equal(actual.artifact.file, 'hello\n'); assert.equal(actual.artifact.headSHA, actual.artifact.baseSHA);
      assert.equal(actual.requestsObserved, 1); assert.doesNotMatch(actual.processes, /\bopencode\b/);
      assert(!workerEvents.some((e: any) => e.type === 'finished'), 'failed criterion must not claim completion');
    } else assert.equal(exit, 0, evidence.pendingCase.workerLogs);
    if (!['oom', 'ambient-timeout'].includes(mode)) assert(workerEvents.some((e: any) => e.type === 'finished'));
    await assert.rejects(docker(['exec', worker.name, 'true']));
    await docker(['stop', '--timeout', '2', gateway.name]);
    const gatewayState = (await inspect(gateway.name)).State; assert.equal(gatewayState.Running, false); assert.equal(gatewayState.Pid, 0); assert.equal(gatewayState.ExitCode, 0);
    const gatewayEvents = (await docker(['logs', gateway.name])).split('\n').filter(l => l.startsWith('{')).map(l => JSON.parse(l));
    assert(gatewayEvents.some(e => e.type === 'finished'));
    if (['partial', 'forbidden'].includes(mode)) {
      const audit = gatewayEvents.find(e => e.type === 'finished').data.audit;
      assert(audit.some((entry: any) => entry.outcome === 'partial_failure' && entry.reason === 'invalid_stream'));
      assert(gatewayEvents.filter(e => e.type === 'request').length <= 3);
    }
    assert.equal(await readFile(join(host, 'sentinel'), 'utf8'), 'SYNTHETIC_HOST_SENTINEL');
    evidence.cases.push({ mode, workerPreflight: worker.preflight, gatewayPreflight: gateway.preflight, exit, workerState: state, gatewayState, workerEvents, gatewayEvents, execAfterStopDenied: true });
    delete evidence.pendingCase;
    await clean();
    console.log(JSON.stringify({ mode, result: 'passed', exit }));
  }
  evidence.result = 'synthetic-harness-and-containment-passed';
} catch (error: any) {
  evidence.error = String(error.stack ?? error.message); process.exitCode = 1;
} finally {
  try {
    await clean(); cleaning = true;
    assert.equal(await docker(['ps', '-aq', '--filter', `label=${LABEL}=${runId}`]), '');
    assert.equal(await docker(['volume', 'ls', '-q', '--filter', `label=${LABEL}=${runId}`]), '');
    evidence.cleanup = { verified: true, containersRemaining: 0, volumesRemaining: 0 };
  } catch (error: any) { evidence.cleanup = { verified: false, error: String(error.message) }; evidence.result = 'blocked'; process.exitCode = 1; }
  if (stop.signal.aborted) { evidence.result = 'blocked'; evidence.interrupted = true; process.exitCode = 1; }
  // Only scoped fixture credentials can occur. Remove them by shape before durable public evidence.
  const safe = JSON.stringify(evidence, null, 2).replaceAll(host, '<host-fixture>').replace(/Bearer [a-f0-9]{64}/g, 'Bearer <scoped-fixture-token>');
  await writeFile(output, safe + '\n');
  if (evidence.cleanup.verified) await rm(host, { recursive: true, force: true });
  console.log(JSON.stringify({ result: evidence.result, cases: evidence.cases.length, cleanup: evidence.cleanup.verified, output, liveRouterCalled: false }));
}
