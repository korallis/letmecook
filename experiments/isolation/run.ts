import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { mkdtemp, mkdir, copyFile, writeFile, readFile, rm } from 'node:fs/promises';
import { tmpdir, homedir } from 'node:os';
import { dirname, resolve, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { randomBytes } from 'node:crypto';
import { setTimeout as delay } from 'node:timers/promises';
import { IMAGE, PROFILE, LABEL, selectedProfile, assertRuntime, containerArgs, assertContainer, cleanupOwned } from './profile.ts';

const exec = promisify(execFile);
const context = process.env.GAFFER_ISOLATION_DOCKER_CONTEXT ?? (await exec('docker', ['context', 'show'])).stdout.trim();
assert.ok(context && !context.startsWith('-'), 'Select an explicit Docker context');
const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..');
const output = resolve(process.argv[2] ?? join(root, 'docs/evidence/linux-profile-run.json'));
const runId = `gaffer-isolation-${randomBytes(6).toString('hex')}`;
const token = randomBytes(32).toString('hex');
const host = await mkdtemp(join(tmpdir(), `${runId}-`));
const staging = join(host, 'fixture');
const ids: string[] = [];
let volume: string | undefined;
const evidence: any = { schema: 1, profile: PROFILE, selectedProfile, dockerContext: context, runId, observedAt: new Date().toISOString(), image: IMAGE,
  syntheticInference: true, unattendedSupported: false, result: 'blocked', commands: [], observations: {},
  acceptance: { independentReview: 'required', prerequisiteIssue: 2, prerequisiteAcceptance: 'not-evaluated-by-experiment' },
  limitations: ['Synthetic inference fixture. Combined live harness/model integration belongs to #6/#7. This experiment does not enable a product unattended runner.'],
  blockers: [] };
// Persist a blocked run marker before any Docker mutation. SIGKILL/host loss cannot leave a prior pass.
await mkdir(dirname(output), { recursive: true });
await writeFile(output, JSON.stringify({ ...evidence, cleanup: { verified: false } }, null, 2) + '\n');
console.log(JSON.stringify({ runId, profile: PROFILE, result: 'blocked-until-proof-and-cleanup' }));
const redact = (text: string) => text.replaceAll(token, '<scoped-fixture-token>').replaceAll(host, '<host-fixture>').replaceAll(homedir(), '<host-home>');
const stop = new AbortController();
let cleaning = false;
function refuseInterrupted() {
  if (stop.signal.aborted) throw new Error('Interrupted: new execution refused');
}
const pause = (ms: number) => delay(ms, undefined, { signal: stop.signal });
async function docker(args: string[], timeout = 15_000) {
  if (!cleaning) refuseInterrupted();
  evidence.commands.push(['docker', '--context', context, ...args].map(redact));
  try {
    const { stdout, stderr } = await exec('docker', ['--context', context, ...args], {
      timeout, maxBuffer: 2 * 1024 * 1024,
      // Interrupt observation/wait CLIs, but let mutations settle before reconciling
      // ownership. Aborting create/start could race a late daemon-side mutation.
      signal: !cleaning && ['wait', 'logs', 'inspect', 'top'].includes(args[0]) ? stop.signal : undefined,
      // Pass the disposable attempt token through environment inheritance, never CLI arguments.
      env: { ...process.env, FIXTURE_TOKEN: token },
    });
    return (stdout + (args[0] === 'logs' ? stderr : '')).trim();
  } catch (error: any) {
    throw new Error(redact(`docker ${args[0]} failed: ${error.stderr || error.message}`));
  }
}
async function inspect(id: string) { return JSON.parse(await docker(['inspect', id]))[0]; }
async function create(mode: string, gateway = false) {
  refuseInterrupted();
  const args = containerArgs(`${runId}-${mode}`, runId, staging, volume!, gateway);
  const targets = { secret: join(host, 'synthetic-secret'), policy: join(host, 'runner-policy'), hostHome: homedir() };
  args.push('--env', `HOST_TARGETS=${JSON.stringify(targets)}`, '--env', 'FIXTURE_TOKEN',
    IMAGE, 'node', gateway ? '/fixture/gateway.ts' : '/fixture/worker.ts', mode);
  // Remember deterministic name before create so ambiguous Docker CLI completion is cleaned up.
  const name = `${runId}-${mode}`;
  ids.push(name);
  await docker(args);
  const state = await inspect(name);
  assertContainer(state, staging, volume!, gateway);
  evidence.observations[`${mode}Preflight`] = {
    user: state.Config.User, network: state.HostConfig.NetworkMode, readonlyRoot: state.HostConfig.ReadonlyRootfs,
    capDrop: state.HostConfig.CapDrop, securityOpt: state.HostConfig.SecurityOpt,
    memory: state.HostConfig.Memory, memorySwap: state.HostConfig.MemorySwap, nanoCpus: state.HostConfig.NanoCpus,
    pids: state.HostConfig.PidsLimit, tmpfs: state.HostConfig.Tmpfs,
    mounts: state.Mounts.map((m: any) => ({ type: m.Type, destination: m.Destination, writable: m.RW })),
  };
  refuseInterrupted();
  await docker(['start', name]);
  return name;
}
async function waitForEvent(id: string, name: string, ms = 5000) {
  const deadline = Date.now() + ms;
  while (Date.now() < deadline) {
    refuseInterrupted();
    const logs = await docker(['logs', id]);
    if (logs.includes(name)) return logs;
    if (!(await inspect(id)).State.Running) throw new Error(`Container exited before ${name}: ${logs}`);
    await pause(100);
  }
  throw new Error(`Timed out waiting for ${name}`);
}
for (const signal of ['SIGINT', 'SIGTERM'] as const) {
  process.on(signal, () => { stop.abort(); });
}
try {
  await mkdir(staging);
  for (const name of ['worker.ts', 'gateway.ts', 'package.json']) await copyFile(join(root, 'tests/fixtures/isolation', name), join(staging, name));
  await writeFile(join(host, 'synthetic-secret'), 'SYNTHETIC-HOST-SECRET-DO-NOT-EXPOSE', { mode: 0o600 });
  await writeFile(join(host, 'runner-policy'), 'disabled-until-reviewed', { mode: 0o600 });
  const version = JSON.parse(await docker(['version', '--format', '{{json .}}']));
  const info = JSON.parse(await docker(['info', '--format', '{{json .}}']));
  evidence.runtime = { clientVersion: version.Client.Version, server: version.Server, cgroupVersion: info.CgroupVersion,
    securityOptions: info.SecurityOptions, hostNode: process.version, hostPlatform: process.platform, hostArch: process.arch };
  if (process.platform === 'darwin') evidence.runtime.macOS = (await exec('sw_vers', [])).stdout.trim();
  assertRuntime(version, info);
  await docker(['image', 'inspect', IMAGE]);
  volume = `${runId}-socket`;
  await docker(['volume', 'create', '--label', `${LABEL}=${runId}`, '--driver', 'local',
    '--opt', 'type=tmpfs', '--opt', 'device=tmpfs', '--opt', 'o=size=1m,uid=1000,gid=1000,mode=0700', volume]);
  const gateway = await create('gateway', true);
  await waitForEvent(gateway, '"ready":true');
  const probe = await create('probe');
  const exit = Number(await docker(['wait', probe], 35_000));
  const logs = await docker(['logs', probe]);
  evidence.observations.probe = logs.split('\n').filter(line => line.startsWith('{"name":')).map(line => JSON.parse(line));
  assert.equal(exit, 0, logs);
  assert.ok(logs.includes('"probe-complete"'));
  refuseInterrupted();

  const oom = await create('oom');
  const oomExit = Number(await docker(['wait', oom], 15_000));
  const oomState = (await inspect(oom)).State;
  assert.equal(oomExit, 137);
  assert.equal(oomState.OOMKilled, true);
  assert.equal(oomState.Running, false);
  evidence.observations.memoryExhaustion = { exit: oomExit, oomKilled: oomState.OOMKilled, running: oomState.Running };
  refuseInterrupted();

  const cancel = await create('cancel');
  await waitForEvent(cancel, '"tree-started"');
  await pause(200);
  const treeBefore = await docker(['top', cancel, '-eo', 'pid,ppid,comm']);
  assert.ok(treeBefore.split('\n').length >= 5, treeBefore);
  const started = Date.now();
  await docker(['stop', '--timeout', '1', cancel]);
  const cancelState = (await inspect(cancel)).State;
  assert.equal(cancelState.Running, false);
  assert.equal(cancelState.Pid, 0);
  assert.equal(cancelState.ExitCode, 137);
  await assert.rejects(docker(['exec', cancel, 'true']), /not running/);
  evidence.observations.cancellation = { treeBefore, elapsedMs: Date.now() - started,
    running: cancelState.Running, initPid: cancelState.Pid, exit: cancelState.ExitCode, execAfterStopDenied: true };
  assert.equal(await readFile(join(host, 'synthetic-secret'), 'utf8'), 'SYNTHETIC-HOST-SECRET-DO-NOT-EXPOSE');
  assert.equal(await readFile(join(host, 'runner-policy'), 'utf8'), 'disabled-until-reviewed');
  evidence.observations.hostSentinelsUnchanged = true;
  refuseInterrupted();
  evidence.result = 'containment-fixture-passed';
} catch (error: any) {
  evidence.error = redact(error.stack ?? error.message);
  process.exitCode = 1;
} finally {
  // Stop never cancels ownership verification, removal or final resource queries.
  cleaning = true;
  try {
    await cleanupOwned(ids, volume, docker, runId);
    const containers = await docker(['ps', '-aq', '--filter', `label=${LABEL}=${runId}`]);
    const volumes = await docker(['volume', 'ls', '-q', '--filter', `label=${LABEL}=${runId}`]);
    assert.equal(containers, '');
    assert.equal(volumes, '');
    evidence.cleanup = { verified: true, containersRemaining: 0, volumesRemaining: 0 };
  } catch (error: any) {
    evidence.cleanup = { verified: false, error: redact(error.message) };
    evidence.result = 'blocked';
    evidence.blockers.push('Containment cleanup is uncertain; quarantine this execution profile and inspect owned resources.');
    process.exitCode = 1;
  }
  await rm(host, { recursive: true, force: true });
  await mkdir(dirname(output), { recursive: true });
  if (stop.signal.aborted) {
    evidence.result = 'blocked';
    evidence.error ??= 'Interrupted: proof is not accepted';
    process.exitCode = 1;
  }
  await writeFile(output, redact(JSON.stringify(evidence, null, 2)) + '\n');
  console.log(JSON.stringify({ result: evidence.result, unattendedSupported: false, cleanup: evidence.cleanup, evidence: output, error: evidence.error }));
}
