// Actual pinned worker + shared gateway, synthetic originals only. Never starts
// or consumes an operator's real initial evaluation scope.
import assert from 'node:assert/strict';
import { execFile, spawn } from 'node:child_process';
import { promisify } from 'node:util';
import { mkdir, readFile, writeFile, readdir, open, rename, mkdtemp, rm } from 'node:fs/promises';
import { readFileSync as readFileLocal } from 'node:fs';
import { dirname, resolve, join } from 'node:path';
import { tmpdir } from 'node:os';
import { createHash, randomBytes } from 'node:crypto';
import { setTimeout as delay } from 'node:timers/promises';
import { commonArgs, inspectProfile, LABEL } from '../../router-boundary-bridge/isolation.ts';
import { inputProcess } from '../../router-boundary-bridge/lifecycle.ts';
import { assertRuntime } from '../../isolation/profile.ts';
import { profile } from '../../native-evaluation/fixtures.mjs';
import { digest, settingsDigest, ISOLATION } from './client.ts';
import { prepareRun } from './admission.ts';
import { candidateEnvelope, acknowledgeCandidate } from './evidence.ts';
import { stageWorker } from './staging.ts';
import { workerArgs, inspectWorker } from './isolation.ts';
import { createFixture } from './fixture.ts';
import { BRIEF, CONTENT } from './run.ts';
import type { Binding } from '../../inference-boundary/types.ts';
const digestBytes = (bytes: Uint8Array) => createHash('sha256').update(bytes).digest('hex');
const execute = promisify(execFile), root = resolve(import.meta.dirname, '../../..'), context = process.env.GAFFER_DOCKER_CONTEXT ?? 'desktop-linux';
const source = process.env.GAFFER_ROUTER_SOURCE, binary = process.env.GAFFER_OPENCODE_BINARY;
assert(source?.startsWith('/') && binary?.startsWith('/'), 'absolute public router and pinned binary paths required');
const image = 'gaffer-router-extension-deps:0.5.75-locked', output = resolve(process.argv[2] ?? '/tmp/gaffer-native-harness-run.json');
const all = ['edit', 'start-delay', 'delay', 'ask', 'registry', 'cancel', 'crash', 'tree', 'receipt-write', 'decision-write', 'artifact-write', 'partial', 'forbidden', 'router-error', 'disabled-hook', 'ambient', 'auth', 'containment', 'transport', 'oom'];
const cases = process.argv[3]?.split(',') ?? all; assert(cases.length && cases.every(x => all.includes(x)));
const run = 'gaffer-native-harness-' + randomBytes(6).toString('hex'), containers = new Set<string>(), volumes = new Set<string>();
const staging = await mkdtemp(join(tmpdir(), run)), fixtureRoot = join(staging, 'base'), baseSHA = createFixture(fixtureRoot);
const stop = new AbortController(); for (const signal of ['SIGINT', 'SIGTERM'] as const) process.on(signal, () => stop.abort());
const secrets: string[] = [], report: any = { schema: 1, run, evidence: 'actual-native-adapter-shared-router-synthetic', live: false, realProviderCalled: false, realEvaluationScopeStarted: false, issueComplete: false, variant: ISOLATION, baseSHA, cases: [], sources: {}, cleanup: false, result: 'blocked' };
const redact = (text: string) => { for (const [value, replacement] of [[root, '<gaffer-source>'], [source!, '<public-router-source>'], [binary!, '<pinned-opencode>'], [staging, '<synthetic-staging>'], ...secrets.map(x => [x, '<scoped-grant>'])]) text = text.replaceAll(value, replacement); return text; };
async function save() { await mkdir(dirname(output), { recursive: true }); const fd = await open(output + '.next', 'w', 0o600); try { await fd.writeFile(redact(JSON.stringify(report, null, 2)) + '\n'); await fd.sync(); } finally { await fd.close(); } await rename(output + '.next', output); const dir = await open(dirname(output), 'r'); try { await dir.sync(); } finally { await dir.close(); } }
async function docker(args: string[], timeout = 15000) { report.commands ??= []; report.commands.push(args); const value = await execute('docker', ['--context', context, ...args], { timeout, maxBuffer: 8 * 1048576 }); return (value.stdout + (args[0] === 'logs' ? value.stderr : '')).trim(); }
async function staleGrant(socketVolume: string, token: string) {
  const name = run + '-stale-' + randomBytes(4).toString('hex'); containers.add(name);
  const args = commonArgs(name, run, false); args.push('--interactive', '--mount', `type=volume,source=${socketVolume},target=/router,readonly,volume-nocopy`, image, 'node', '-e', "let token='';process.stdin.on('data',c=>token+=c);process.stdin.on('end',()=>{const q=require('node:http').request({socketPath:'/router/inference.sock',path:'/v1/responses',method:'POST',headers:{host:'localhost',authorization:'Bearer '+token,'content-type':'application/json','content-length':2}},r=>{r.resume();r.on('end',()=>console.log(r.statusCode))});q.setTimeout(2000,()=>q.destroy());q.on('error',()=>process.exit(1));q.end('{}')})");
  await docker(args); const status = Number(await inputProcess('docker', ['--context', context, 'start', '--attach', '--interactive', name], token, stop.signal, 5000)); await owned(name); await docker(['rm', name]); containers.delete(name); return status;
}
const inspect = async (id: string) => JSON.parse(await docker(['inspect', id]))[0];
async function owned(id: string) { const s = await inspect(id); assert.equal(s.Config.Labels[LABEL], run); return s; }
async function event(id: string, name: string, limit = 40000) {
  const until = Date.now() + limit; let previous = '';
  const extract = (text: string) => text.split('\n').flatMap(line => { try { return [JSON.parse(line)]; } catch { return []; } }).find(x => x.event === name);
  while (Date.now() < until) {
    stop.signal.throwIfAborted(); const logs = await docker(['logs', id]), found = extract(logs); if (found) return found;
    const state = (await owned(id)).State;
    if (state.Status !== previous) { report.lifecycle ??= []; report.lifecycle.push({ id, at: Date.now(), state }); previous = state.Status; }
    // docker start --attach is asynchronous: created is not a terminal state.
    if (['exited', 'dead'].includes(state.Status)) {
      const finalLogs = await docker(['logs', id]), final = extract(finalLogs); if (final) return final;
      throw Error('exited_before_' + name + ':' + JSON.stringify({ state, logs: finalLogs }));
    }
    await delay(50);
  }
  throw Error('event_timeout:' + name + ':' + JSON.stringify((await owned(id)).State));
}
async function control(volume: string, message: unknown) {
  const name = run + '-control-' + randomBytes(4).toString('hex'); containers.add(name);
  const program = "const http=require('node:http');let body='';process.stdin.on('data',c=>body+=c);process.stdin.on('end',()=>{const q=http.request({socketPath:'/control/gateway.sock',path:'/control',method:'POST'},r=>{let text='';r.on('data',c=>text+=c);r.on('end',()=>{console.log(JSON.stringify({status:r.statusCode,body:JSON.parse(text)}));});});q.setTimeout(5000,()=>q.destroy());q.on('error',()=>process.exit(1));q.end(body);});";
  const args = commonArgs(name, run, false); args.push('--interactive', '--mount', `type=volume,source=${volume},target=/control,readonly,volume-nocopy`, image, 'node', '-e', program); await docker(args);
  const text = await inputProcess('docker', ['--context', context, 'start', '--attach', '--interactive', name], JSON.stringify(message), stop.signal, 10000);
  const state = await owned(name); assert.equal(state.State.Pid, 0); assert.equal(state.State.OOMKilled, false); await docker(['rm', name]); containers.delete(name);
  const response = JSON.parse(text); return response;
}
const call = async (volume: string, message: unknown) => { const r = await control(volume, message); assert.equal(r.status, 200, JSON.stringify(r)); return r.body; };
async function readState(volume: string) {
  const name = run + '-read-' + randomBytes(4).toString('hex'); containers.add(name);
  const args = commonArgs(name, run, false); args.push('--mount', `type=volume,source=${volume},target=/state,readonly,volume-nocopy`, '--mount', `type=bind,source=${root},target=/gaffer,readonly`, image, 'node', '/gaffer/experiments/harness/native/read-state.mjs');
  await docker(args); await docker(['start', name]); assert.equal(Number(await docker(['wait', name])), 0);
  const result = JSON.parse(await docker(['logs', name])); await owned(name); await docker(['rm', name]); containers.delete(name); return result;
}
async function stopWorker(id: string, force = false) { const before = await owned(id); if (before.State.Running) await docker(force ? ['kill', id] : ['stop', '--timeout', '1', id]); const s = await owned(id); assert.equal(s.State.Pid, 0); await assert.rejects(docker(['exec', id, 'true'])); return s.State; }
await save();
try {
  const version = JSON.parse(await docker(['version', '--format', '{{json .}}'])), info = JSON.parse(await docker(['info', '--format', '{{json .}}'])); assertRuntime(version, info);
  report.runtime = { server: version.Server, image: JSON.parse(await docker(['image', 'inspect', image]))[0].Id, node: 'v24.21.0', arch: 'arm64' };
  assert.equal(report.runtime.image, JSON.parse(await readFile(join(root, 'experiments/router-authority-extension/runtime-identity.json'), 'utf8')).imageId);
  async function sources(dir: string) { for (const entry of await readdir(join(root, dir), { withFileTypes: true })) { if (entry.name === 'node_modules') continue; const name = dir + '/' + entry.name; assert(!entry.isSymbolicLink()); if (entry.isDirectory()) await sources(name); else if (/\.(ts|mjs|json)$/.test(name)) report.sources[name] = createHash('sha256').update(await readFile(join(root, name))).digest('hex'); } }
  for (const directory of ['experiments/harness', 'experiments/native-evaluation', 'experiments/inference-boundary', 'experiments/router-authority-extension', 'experiments/router-boundary-bridge']) await sources(directory);
  const clientStage = join(staging, 'client'); report.workerStaging = await stageWorker(root, clientStage, binary!, true);
  for (const scenario of cases) {
    stop.signal.throwIfAborted(); const current: any = { scenario, result: 'pending', workers: [] }; report.cases.push(current); await save();
    const inputs = join(staging, scenario); await mkdir(inputs);
    const native = profile(); native.harness.settings = settingsDigest('allow'); native.deployment.overlay = digest(report.sources); native.deployment.runtime = digest(report.runtime); native.deployment.sourceLock = report.sources['experiments/router-authority-extension/source-lock.json']; native.deployment.isolation = digest({ variant: ISOLATION, runtime: report.runtime });
    const ask = structuredClone(native); ask.harness.settings = settingsDigest('ask');
    const profiles = [native, ask]; await writeFile(join(inputs, 'profile.json'), JSON.stringify(native)); await writeFile(join(inputs, 'profiles.json'), JSON.stringify(profiles)); await writeFile(join(inputs, 'source-manifest.json'), JSON.stringify({ files: report.sources, runtime: report.runtime }));
    const config = { settings: { requireApiKey: true, rtkEnabled: false, headroomEnabled: false, pxpipeEnabled: false, cavemanEnabled: false, ponytailEnabled: false, ccFilterNaming: false, capacityAdapter: Object.fromEntries(['vision', 'pdf', 'audioInput', 'videoInput'].map(k => [k, { enabled: false, models: [] }])) }, providerConnections: native.connections.map((c: any, i: number) => ({ id: c.id, provider: 'codex', authType: 'oauth', name: 'Synthetic', priority: i + 1, isActive: true, accessToken: 'synthetic_access_' + i, expiresAt: new Date(c.expiresAt).toISOString(), providerSpecificData: { chatgptAccountId: 'synthetic_workspace_' + i } })), combos: [{ id: 'native_route', name: 'gpt-6-astra', models: ['cx/gpt-6-astra'] }] };
    await writeFile(join(inputs, 'config.json'), JSON.stringify(config));
    const caseVolumes = ['state', 'socket', 'control'].map(x => run + '-' + scenario + '-' + x);
    for (const volume of caseVolumes) { volumes.add(volume); await docker(['volume', 'create', '--label', LABEL + '=' + run, volume]); await docker(['run', '--rm', '--label', LABEL + '=' + run, '--network', 'none', '--user', '0:0', '--cap-drop', 'ALL', '--cap-add', 'CHOWN', '--security-opt', 'no-new-privileges=true', '--read-only', '--mount', `type=volume,source=${volume},target=/volume,volume-nocopy`, image, 'chown', '1000:1000', '/volume']); }
    const [stateVolume, socketVolume, controlVolume] = caseVolumes, gateway = run + '-' + scenario + '-gateway'; containers.add(gateway);
    const args = commonArgs(gateway, run, true), gatewayBinds = { '/gaffer': root, '/probe': join(root, 'experiments/router-authority-extension'), '/router-source': source!, '/config': inputs, '/private': inputs };
    for (const [destination, path] of Object.entries(gatewayBinds)) args.push('--mount', `type=bind,source=${path},target=${destination},readonly`);
    for (const [destination, volume] of [['/state', stateVolume], ['/router', socketVolume], ['/control', controlVolume]]) args.push('--mount', `type=volume,source=${volume},target=${destination},volume-nocopy`);
    args.push('--env', 'DATA_DIR=/state/router-db', '--env', 'GAFFER_SYNTHETIC_NATIVE=1', '--env', 'GAFFER_TEST_FAULT=' + scenario, '--env', 'GAFFER_HARNESS_NATIVE_FAULT=' + scenario, image, 'node', '--experimental-loader', '/gaffer/experiments/harness/native/fault-loader.mjs', '/gaffer/experiments/native-evaluation/bootstrap.mjs'); await docker(args);
    current.gatewayInspect = await owned(gateway); current.gatewayMeasured = inspectProfile({ ...current.gatewayInspect, Mounts: current.gatewayInspect.Mounts.filter((m: any) => !['/state', '/control'].includes(m.Destination)) }, image, run, true, socketVolume, gatewayBinds); for (const [destination, volume] of [['/state', stateVolume], ['/control', controlVolume]]) assert(current.gatewayInspect.Mounts.some((m: any) => m.Type === 'volume' && m.Destination === destination && m.Name === volume && m.RW)); const h = current.gatewayInspect.HostConfig; assert.equal(current.gatewayInspect.Image, report.runtime.image); assert.equal(h.Memory, 768 * 1048576); assert.equal(h.MemorySwap, h.Memory); assert.equal(h.NetworkMode, 'none'); assert.equal(h.Privileged, false); assert.deepEqual(h.CapDrop, ['ALL']); assert.deepEqual(h.SecurityOpt, ['no-new-privileges=true']); assert.equal(h.ReadonlyRootfs, true); assert.equal(current.gatewayInspect.Mounts.length, 8); assert(current.gatewayInspect.Mounts.filter((m: any) => m.Type === 'bind').every((m: any) => !m.RW));
    await docker(['start', gateway]); await event(gateway, 'gateway_ready', 10000);
    const prepared = await call(controlVolume, { command: 'inspect' }); current.packet = prepared.packet; current.packetDigest = prepared.packetDigest; assert.equal(prepared.current.scope, null);
    current.scopeStarted = await call(controlVolume, { command: 'start', packetDigest: prepared.packetDigest });
    let policy = prepared.selectedPolicy;
    for (const [index, mode] of (scenario === 'registry' ? ['edit', 'ask'] : [scenario]).entries()) {
      if (mode === 'ask') { const selected = prepared.packet.profiles.find((p: any) => p.harness.settings === settingsDigest('ask')); const before = await call(controlVolume, { command: 'inspect' }); const response = await call(controlVolume, { command: 'select', packetDigest: prepared.packetDigest, profileDigest: digest(selected) }); policy = response.policy; assert.equal(response.scope.spent, before.current.scope.spent); assert.equal(response.scope.deadline, before.current.scope.deadline); current.selection = response; if (current.workers.length) { current.staleGrantStatus = await staleGrant(socketVolume, secrets.at(-1)!); assert.equal(current.staleGrantStatus, 401); const old = await control(controlVolume, { command: 'grant', binding: current.workers.at(-1).binding }); assert.equal(old.status, 403); current.staleBindingGrantStatus = old.status; } }
      const binding: Binding = { attemptId: 'worker_attempt_' + index, grantId: 'worker_grant_' + index, taskId: 'worker_task_' + index, leaseId: 'worker_lease_' + index, fence: 1, role: 'worker', routerId: policy.routerId, routeId: policy.routeId, revision: policy.revision, epoch: policy.epoch, expiresAt: Date.now() + 45000, leaseExpiresAt: Date.now() + 45000, native: { profileDigest: digest(policy.native), scopeId: policy.native.scope.id, authorizationDigest: policy.native.scope.authorizationDigest } };
      const grant = await call(controlVolume, { command: 'grant', binding }); secrets.push(grant.token);
      const request = prepareRun(policy, binding, { baseSHA, brief: BRIEF, approval: mode === 'ask' ? 'ask' : 'allow', limits: { wallMs: 30000, outputBytes: 262144 }, token: grant.token });
      const worker = run + '-' + scenario + '-worker-' + index; containers.add(worker); const argv = workerArgs(worker, run); argv.push('--interactive', '--mount', `type=bind,source=${clientStage},target=/fixture,readonly`, '--mount', `type=volume,source=${socketVolume},target=/router,readonly,volume-nocopy`, '--env', 'HOST_SENTINEL=/nonexistent-host-sentinel', image, 'node', '/fixture/experiments/harness/native/' + (['edit', 'start-delay', 'delay', 'ask', 'receipt-write', 'decision-write', 'artifact-write', 'partial', 'forbidden', 'router-error'].includes(mode) ? 'worker.ts' : 'proof-worker.ts'), mode); await docker(argv);
      const measured = inspectWorker(await owned(worker), image, run, socketVolume, clientStage), entry: any = { mode, binding, policy, measured, outcome: 'pending' }; current.workers.push(entry); await save();
      const dockerStart = ['--context', context, 'start', '--attach', '--interactive', worker];
      const deferredStart = "const chunks=[];process.stdin.on('data',c=>chunks.push(c));process.stdin.on('end',()=>setTimeout(()=>{const c=require('node:child_process').spawn('docker',process.argv.slice(1),{stdio:['pipe','inherit','inherit']});c.stdin.end(Buffer.concat(chunks));c.on('exit',code=>process.exit(code??1))},750))";
      const child = mode === 'start-delay' ? spawn(process.execPath, ['-e', deferredStart, ...dockerStart], { stdio: ['pipe', 'pipe', 'pipe'] }) : spawn('docker', dockerStart, { stdio: ['pipe', 'pipe', 'pipe'] }); let stdout = '', stderr = ''; child.stdout.on('data', b => stdout += b); child.stderr.on('data', b => stderr += b); child.stdin.on('error', () => {}); child.stdin.end(JSON.stringify(request)); const exited = new Promise(resolve => child.once('exit', (code, signal) => resolve({ code, signal })));
      const watchdog = setTimeout(() => { void stopWorker(worker, true).finally(() => child.kill('SIGKILL')).catch(() => {}); }, 42000);
      try {
        if (mode === 'oom') { entry.observation = await event(worker, 'native_worker_observation', 15000); assert(entry.observation.oom.after.oom_kill > entry.observation.oom.before.oom_kill); assert.equal(entry.observation.oom.exit.signal, 'SIGKILL'); entry.state = await stopWorker(worker); entry.outcome = 'cgroup_oom_observed'; }
        else {
          entry.observation = await event(worker, 'native_worker_observation'); await save();
          const artifact = JSON.parse(await docker(['exec', worker, 'node', '/fixture/experiments/harness/native/inspect-artifact.ts', baseSHA])); entry.observedArtifact = artifact;
          entry.evidence = await call(controlVolume, { command: 'evidence', attemptId: binding.attemptId }); await save();
          const completed = ['edit', 'start-delay', 'delay', 'artifact-write'].includes(mode);
          if (completed) {
            assert.equal(entry.observation.result.outcome, 'completed_candidate'); assert.equal(artifact.content, CONTENT);
            const envelope = candidateEnvelope({ policy, packetDigest: entry.evidence.packetDigest, scope: entry.evidence.scope, binding, request, result: entry.observation.result, requests: entry.observation.requests, decisions: entry.evidence.decisions, receipts: entry.evidence.receipts, pendingReservations: entry.evidence.reservations, durableDecisionTimes: entry.evidence.decisionTimes, observedArtifact: artifact }); entry.candidate = envelope;
            const ack = await control(controlVolume, { command: 'candidate', value: envelope }); entry.acknowledgement = ack;
            if (mode === 'artifact-write') { assert.equal(ack.status, 403); entry.outcome = 'artifact_durability_refused'; }
            else { assert.equal(ack.status, 200); const after = await call(controlVolume, { command: 'evidence', attemptId: binding.attemptId }); entry.qualified = acknowledgeCandidate(envelope, ack.body, after.artifacts); entry.retryAcknowledgement = await call(controlVolume, { command: 'candidate', value: envelope }); assert.deepEqual(entry.retryAcknowledgement, ack.body); entry.outcome = 'completed_candidate'; }
          } else if (mode === 'ask') { assert.equal(entry.observation.result.outcome, 'approval_blocked'); assert.equal(artifact.content, 'hello\n'); entry.outcome = 'approval_blocked'; }
          else if (['ambient', 'auth'].includes(mode)) { assert.equal(entry.observation.admission, 'refused'); assert.equal(entry.evidence.decisions.length + entry.evidence.reservations.length, 0); entry.outcome = 'ambient_admission_refused'; }
          else if (mode === 'transport') { assert(entry.observation.transport.length >= 10); assert.equal(entry.evidence.decisions.length + entry.evidence.reservations.length, 0); entry.outcome = 'transport_refused'; }
          else if (mode === 'containment') { assert(entry.observation.containment); entry.outcome = 'containment_observed'; }
          else if (mode === 'disabled-hook') { assert.equal(entry.observation.requests.length, 0); assert(entry.observation.rejected.length > 0); assert(entry.observation.rejected.every((r: any) => r.body.max_output_tokens === 128)); assert.equal(artifact.content, 'hello\n'); entry.outcome = 'cap_request_refused'; }
          else { assert.notEqual(entry.observation.result.outcome, 'completed_candidate'); assert.equal(artifact.content, 'hello\n'); assert(!entry.observation.result.events.some((e: any) => e.type === 'tool_use' && e.native.part.state.status === 'completed')); entry.outcome = entry.observation.result.outcome; }
          if (mode === 'tree') entry.treeBeforeStop = await docker(['top', worker, '-eo', 'pid,ppid,comm']);
          entry.state = await stopWorker(worker, mode === 'tree'); assert.equal(entry.state.OOMKilled, false); if (mode !== 'tree') assert.equal(entry.state.ExitCode, 0);
        }
      } finally { clearTimeout(watchdog); if ((await owned(worker)).State.Running) await stopWorker(worker, true); entry.attach = await exited; entry.stdout = stdout; entry.stderr = stderr; await save(); }
    }
    await call(controlVolume, { command: 'stop' }); await docker(['wait', gateway], 15000); current.gatewayState = (await owned(gateway)).State; current.gatewayLogs = await docker(['logs', gateway]); current.retained = await readState(stateVolume); current.gateway = current.retained.result; await save();
    if (scenario === 'decision-write') { assert.equal(current.gatewayState.ExitCode, 1); assert.equal(current.gateway, null); assert(current.retained.records['boundary/state.json']); current.closure = 'failed_closed_quiescence_unknown'; } else assert.equal(current.gatewayState.ExitCode, 0); assert.equal(current.gatewayState.OOMKilled, false); assert.equal(current.gatewayState.Pid, 0);
    if (['ambient', 'auth', 'containment', 'transport', 'oom', 'disabled-hook'].includes(scenario)) assert.equal(current.gateway.observed.sends.length, 0);
    if (['edit', 'start-delay', 'delay', 'artifact-write'].includes(scenario)) assert.equal(current.gateway.observed.sends.length, 2);
    if (scenario === 'delay') assert(current.workers[0].observation.result.events.find((e: any) => e.type === 'tool_use').native.part.state.time.start >= current.gateway.observed.harnessFault.originalEnds[0].at);
    for (const entry of current.workers) if (entry.qualified) { const stored = current.retained.records[entry.acknowledgement.body.file]; assert.deepEqual(stored, entry.candidate); assert.equal(digest(stored), entry.qualified.artifactDigest); }
    if (scenario === 'start-delay') assert(report.lifecycle.some((e: any) => e.id.includes('-worker-') && e.state.Status === 'created'));
    current.result = 'passed'; await save(); console.log(JSON.stringify({ scenario, result: current.result, physicalSyntheticSends: current.gateway?.observed.sends.length ?? null }));
    for (const id of [...containers]) { await owned(id); await docker(['rm', '--force', id]); containers.delete(id); }
    for (const volume of caseVolumes) { const v = JSON.parse(await docker(['volume', 'inspect', volume]))[0]; assert.equal(v.Labels[LABEL], run); await docker(['volume', 'rm', volume]); volumes.delete(volume); }
  }
  report.sourcesVerifiedAfterRun = Object.entries(report.sources).every(([path, expected]) => digestBytes(readFileLocal(join(root, path))) === expected); assert(report.sourcesVerifiedAfterRun);
  report.result = cases.length === all.length ? 'passed' : 'selected-cases-passed';
} catch (error: any) { report.result = 'failed'; report.error = String(error.stack ?? error); process.exitCode = 1; report.failureLogs = []; for (const id of containers) try { report.failureLogs.push({ id, state: (await owned(id)).State, logs: await docker(['logs', id]) }); } catch {} }
finally {
  await save(); const errors: string[] = [];
  for (const id of containers) try { await owned(id); await docker(['rm', '--force', id]); } catch { errors.push(id); }
  // Synthetic-only fixture volumes may be removed after evidence is durable.
  for (const volume of volumes) try { const v = JSON.parse(await docker(['volume', 'inspect', volume]))[0]; assert.equal(v.Labels[LABEL], run); await docker(['volume', 'rm', volume]); } catch { errors.push(volume); }
  report.cleanup = !errors.length && !(await docker(['ps', '-aq', '--filter', 'label=' + LABEL + '=' + run])) && !(await docker(['volume', 'ls', '-q', '--filter', 'label=' + LABEL + '=' + run])); report.cleanupErrors = errors; await save(); await rm(staging, { recursive: true }); console.log(JSON.stringify({ result: report.result, cleanup: report.cleanup, output }));
}
