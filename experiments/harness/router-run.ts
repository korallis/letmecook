import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { mkdir, readFile, writeFile, readdir, mkdtemp, copyFile, rm } from 'node:fs/promises';
import { dirname, resolve, join } from 'node:path';
import { tmpdir } from 'node:os';
import { randomBytes, createHash } from 'node:crypto';
import { setTimeout as delay } from 'node:timers/promises';
import { inputProcess } from '../router-boundary-bridge/lifecycle.ts';
import { LABEL } from '../router-boundary-bridge/isolation.ts';
import { routerHarnessArgs, inspectRouterHarness, ROUTER_VARIANT } from './isolation.ts';
import { assertRuntime, selectedProfile, IMAGE } from '../isolation/profile.ts';
import { probe, pins } from './adapter.ts';
import { hashDocument } from '../inference-boundary/router-policy.ts';
import { canonical } from '../inference-boundary/json.ts';
import { codecOverlayFiles, routerWorkerFiles, stageFiles } from './staging.ts';
const exec = promisify(execFile), root = resolve(import.meta.dirname, '../..');
const context = process.env.GAFFER_BRIDGE_DOCKER_CONTEXT ?? 'desktop-linux';
const source = process.env.GAFFER_BRIDGE_ROUTER_SOURCE, binary = process.env.GAFFER_HARNESS_BINARY;
assert(source && binary && source.startsWith('/') && binary.startsWith('/'), 'absolute public pinned router source and OpenCode binary required'); probe(binary);
const image = process.env.GAFFER_BRIDGE_IMAGE ?? 'gaffer-router-extension-deps:0.5.75-locked';
const output = resolve(process.argv[2] ?? join(root, 'docs/evidence/first-harness-router-run.json'));
const runId = 'gaffer-harness-router-' + randomBytes(6).toString('hex'), ids: string[] = [], volumes: string[] = [];
const staging = await mkdtemp(join(tmpdir(), runId));
const workerStaging = join(staging, 'worker'), gatewayStaging = join(staging, 'gateway');
const stop = new AbortController(); for (const signal of ['SIGINT', 'SIGTERM'] as const) process.on(signal, () => stop.abort()); let cleaning = false;
const evidence: any = { schema: 1, observedAt: new Date().toISOString(), runId, result: 'blocked', variant: ROUTER_VARIANT, selectedProfile, pins, realProviderCalled: false, syntheticUpstreamCalled: true, privateDataUsed: false, liveAdmission: false, issueComplete: false, independentReview: 'required', sources: {}, cases: [], cleanup: { verified: false } };
await mkdir(dirname(output), { recursive: true });
const secrets: string[] = []; const redact = (s: string) => { for (const [value, name] of [[root, '<gaffer-source>'], [source, '<public-router-source>'], [staging, '<worker-fixture>'], ...secrets.map(s => [s, '<scoped-token>'])]) s = s.replaceAll(value, name); return s.replace(/Bearer [a-f0-9]{64}/g, 'Bearer <scoped-token>'); };
const save = () => writeFile(output, redact(JSON.stringify(evidence, null, 2)) + '\n'); await save();
async function docker(args: string[], timeout = 15000) { if (!cleaning) stop.signal.throwIfAborted(); try { const r = await exec('docker', ['--context', context, ...args], { timeout, maxBuffer: 8 * 1048576, signal: !cleaning && ['inspect', 'logs', 'wait', 'top'].includes(args[0]) ? stop.signal : undefined }); return (r.stdout + (args[0] === 'logs' ? r.stderr : '')).trim(); } catch (e: any) { throw new Error(redact(e.stderr ?? e.message)); } }
const inspect = async (id: string) => JSON.parse(await docker(['inspect', id]))[0];
async function event(id: string, name: string, ms = 10000) {
  const until = Date.now() + ms;
  while (Date.now() < until) {
    const logs = await docker(['logs', id]);
    const found = logs.split('\n').filter(l => l.startsWith('{')).map(l => { try { return JSON.parse(l); } catch { return null; } }).find(e => e?.event === name);
    if (found) return found;
    if (!(await inspect(id)).State.Running) throw new Error('gateway_exited_before_' + name + ': ' + logs);
    await delay(50, undefined, { signal: stop.signal });
  }
  throw new Error('event_timeout_' + name);
}
try {
  async function digestTree(dir: string) { for (const f of await readdir(join(root, dir), { withFileTypes: true })) { if (f.name === 'node_modules') continue; const file = dir + '/' + f.name; if (f.isDirectory()) await digestTree(file); else if (/\.(ts|mjs|json)$/.test(f.name)) evidence.sources[file] = createHash('sha256').update(await readFile(join(root, file))).digest('hex'); } }
  for (const dir of ['experiments/harness', 'tests/fixtures/harness', 'experiments/inference-boundary', 'experiments/router-boundary-bridge', 'experiments/router-authority-extension']) await digestTree(dir);
  // Worker gets only client/validation files. Authority, journal, router source and
  // synthetic credentials are exclusively mounted in the gateway's namespace.
  await stageFiles(root, workerStaging, routerWorkerFiles);
  await stageFiles(root, gatewayStaging, codecOverlayFiles);
  await copyFile(binary, join(workerStaging, 'opencode')); probe(join(workerStaging, 'opencode'));
  evidence.workerStagedFiles = [...routerWorkerFiles, 'package.json', 'opencode'];
  evidence.gatewayPureStagedFiles = codecOverlayFiles;
  const version = JSON.parse(await docker(['version', '--format', '{{json .}}'])), info = JSON.parse(await docker(['info', '--format', '{{json .}}'])); assertRuntime(version, info);
  evidence.runtime = { server: version.Server, cgroupVersion: info.CgroupVersion, securityOptions: info.SecurityOptions, hostNode: process.version, gatewayImage: JSON.parse(await docker(['image', 'inspect', image]))[0].Id, workerImage: JSON.parse(await docker(['image', 'inspect', IMAGE]))[0].Id };
  assert.equal(evidence.runtime.gatewayImage, JSON.parse(await readFile(join(root, 'experiments/router-authority-extension/runtime-identity.json'), 'utf8')).imageId);
  const all = ['edit', 'write', 'delay', 'eof-delay', 'ask', 'unknown', 'malformed-receipt', 'persist-failure', 'persist-after-rename', 'receipt-stop', 'post-decision-cancel', 'changed-graph', 'zero-auth', 'partial', 'forbidden', 'router-error', 'cancel', 'crash', 'tree', 'fence', 'containment', 'ambient-probe', 'ambient-timeout', 'oom'];
  const selected = process.env.GAFFER_HARNESS_ROUTER_CASES?.split(','); if (selected) assert(selected.length && selected.every(s => all.includes(s)));
  for (const scenario of all) {
    if (selected && !selected.includes(scenario)) continue;
    stop.signal.throwIfAborted(); const volume = runId + '-' + scenario + '-socket'; volumes.push(volume);
    await docker(['volume', 'create', '--label', `${LABEL}=${runId}`, '--driver', 'local', '--opt', 'type=tmpfs', '--opt', 'device=tmpfs', '--opt', 'o=size=1m,uid=1000,gid=1000,mode=0700', volume]);
    const gateway = runId + '-' + scenario + '-gateway'; ids.push(gateway);
    const binds = { '/harness': join(root, 'experiments/harness'), '/router-boundary-bridge': join(root, 'experiments/router-boundary-bridge'), '/inference-boundary': join(root, 'experiments/inference-boundary'), '/router-authority-extension': join(gatewayStaging, 'experiments/router-authority-extension'), '/probe': join(root, 'experiments/router-authority-extension'), '/router-source': source };
    const args = routerHarnessArgs(gateway, runId, true);
    for (const [target, path] of Object.entries(binds)) args.push('--mount', `type=bind,source=${path},target=${target},readonly`);
    args.push('--mount', `type=volume,source=${volume},target=/router,volume-nocopy`, '--env', 'DATA_DIR=/work/router-db', '--env', 'GAFFER_SYNTHETIC_OPENCODE=1', image, 'node', '/harness/router-supervise.ts', scenario);
    await docker(args); const gatewayProfile = inspectRouterHarness(await inspect(gateway), image, runId, true, volume, binds); await docker(['start', gateway]);
    const grant = await event(gateway, 'ready'); secrets.push(grant.token);
    const worker = runId + '-' + scenario + '-worker'; ids.push(worker);
    const wargs = routerHarnessArgs(worker, runId, false);
    wargs.push('--interactive', '--mount', `type=bind,source=${workerStaging},target=/fixture,readonly`, '--mount', `type=volume,source=${volume},target=/router,readonly,volume-nocopy`, '--env', 'HOST_SENTINEL=/nonexistent-host-sentinel', IMAGE, 'node', '/fixture/tests/fixtures/harness/router-worker.ts', scenario);
    await docker(wargs); const workerProfile = inspectRouterHarness(await inspect(worker), IMAGE, runId, false, volume, { '/fixture': workerStaging });
    const running = inputProcess('docker', ['--context', context, 'start', '--attach', '--interactive', worker], JSON.stringify(grant), stop.signal, 35000).catch((e: any) => ({ error: String(e.message) }));
    if (scenario === 'tree') { const until = Date.now() + 30000; while (!(await docker(['logs', worker])).includes('"type":"finished"')) { assert(Date.now() < until); await delay(100); } evidence.treeTeardown = { before: await docker(['top', worker, '-eo', 'pid,ppid,comm']) }; await docker(['stop', '--timeout', '1', worker]); }
    const workerInputResult = await running;
    await docker(['wait', worker], 35000);
    const workerState = (await inspect(worker)).State, workerLogs = await docker(['logs', worker]);
    evidence.pending = { scenario, workerState, workerLogs, workerInputResult, gatewayLogs: await docker(['logs', gateway]) }; await save();
    await docker(['kill', '--signal', 'SIGTERM', gateway]); await docker(['wait', gateway]);
    const finished = await event(gateway, 'finished'), gatewayState = (await inspect(gateway)).State;
    const workerEvents = workerLogs.split('\n').filter(l => l.startsWith('{')).map(l => JSON.parse(l));
    const observation = workerEvents.find(e => e.type === 'observation')?.data;
    evidence.pending = { ...evidence.pending, gatewayState, gatewayResult: finished.result }; await save();
    assert.equal(gatewayState.ExitCode, 0); assert.equal(gatewayState.OOMKilled, false); assert.equal(gatewayState.Pid, 0); assert.equal(workerState.Pid, 0);
    if (scenario === 'oom') { assert.equal(workerState.OOMKilled, true); assert.equal(workerState.ExitCode, 137); }
    else if (scenario === 'tree') assert.equal(workerState.ExitCode, 137);
    else if (scenario === 'ambient-timeout') { assert.equal(workerState.ExitCode, 1); assert.equal(observation.stopReason, 'forced_timeout'); assert.equal(observation.canaryExecutedDespiteFlags, false); assert.match(observation.stdout, /forced timeout stdout diagnostic/); assert.match(observation.stderr, /forced timeout stderr diagnostic/); assert(!workerEvents.some(e => e.type === 'finished')); }
    else { assert.equal(workerState.ExitCode, 0, workerLogs); assert(workerEvents.some(e => e.type === 'finished')); }
    const result = finished.result;
    const accepted = ['edit', 'write', 'delay', 'eof-delay'].includes(scenario);
    const decisions = result.gateSnapshot.decisions;
    if (accepted) {
      assert.equal(observation.outcome, 'completed_candidate'); assert.equal(observation.requests.length, 2); assert.equal(result.sends.length, 2); assert.equal(decisions.length, 2); assert.equal(result.actualGlobalQuiescence, true);
      for (const [i, r] of observation.requests.entries()) {
        const decision = decisions.find((d: any) => d.requestId === r.requestId); assert.equal(decision.verdict, 'validated_success'); assert.equal(decision.delivery, 'completed');
        const persisted = result.events.find((e: any) => e.event === 'decision_persisted' && e.requestId === r.requestId); assert(r.endedAt >= persisted.at); if (i === 0) { assert(observation.firstEditAt >= persisted.at); assert(r.firstToolChunkAt >= persisted.at); }
        const wire = JSON.parse(r.body), ingress = result.ingress.find((e: any) => e.requestId === r.requestId), original = result.sends.find((e: any) => e.requestId === r.requestId);
        assert.equal(decision.router.requestDigest, hashDocument(JSON.stringify(ingress.body)));
        assert.equal(canonical(decision.router.binding), canonical(grant.binding));
        assert.equal(canonical(wire), canonical(ingress.body)); const { model, ...rest } = ingress.body; assert.equal(canonical(rest), canonical(Object.fromEntries(Object.entries(original.body).filter(([k]) => k !== 'model'))));
        assert.equal(original.body.max_tokens, 128); assert.equal(original.body.tool_choice, 'auto'); assert(!Object.hasOwn(original.body, 'stream_options'));
      }
      const tool = observation.events.find((e: any) => e.type === 'tool_use').native.part;
      const firstId = observation.requests[0].requestId;
      assert.equal(tool.callID, `call_${firstId}_0`);
      const continuation = result.ingress[1].body.messages;
      assert.equal(continuation.find((m: any) => m.tool_calls)?.tool_calls[0].id, tool.callID);
      assert.equal(continuation.find((m: any) => m.role === 'tool')?.tool_call_id, tool.callID);
      if (['delay', 'eof-delay'].includes(scenario)) {
        const release = result.events.find((e: any) => e.requestId === firstId && e.event === (scenario === 'delay' ? 'receipt_visible' : 'original_eof_released'));
        assert(observation.firstEditAt >= release.at); assert(tool.state.time.start >= release.at);
      }
      assert.equal(observation.artifact.headSHA, observation.artifact.baseSHA);
      result.acceptedCandidate = { baseSHA: observation.artifact.baseSHA, headSHA: observation.artifact.headSHA, changedFiles: observation.artifact.changedFiles, diff: observation.artifact.diff, artifactSHA256: createHash('sha256').update(observation.artifact.file).digest('hex'), toolCallJoin: { original: 'call_original_edit_1', boundaryAndNative: tool.callID, continuation: true }, nativeToolEvents: observation.events.filter((e: any) => e.type === 'tool_use'), nativeTerminal: observation.events.at(-1), requests: decisions.map((d: any) => ({ requestId: d.requestId, decisionDigest: createHash('sha256').update(canonical(d)).digest('hex'), decision: d })), usage: 'unknown', acceptedByTrustedSupervisor: true };
    } else if (observation && !scenario.startsWith('ambient')) {
      assert.notEqual(observation.outcome, 'completed_candidate'); assert.equal(observation.artifact.file, 'hello\n'); assert.equal(observation.artifact.headSHA, observation.artifact.baseSHA);
      assert(!observation.events.some((e: any) => e.type === 'tool_use' && e.native.part.state.status === 'completed'));
      assert(result.ingress.every((r: any) => !r.body.messages.some((m: any) => m.role === 'tool')), 'no tool-result continuation after denied completion');
    }
    if (['persist-failure', 'persist-after-rename'].includes(scenario)) { assert.equal(decisions.length, 0); assert(result.audit.every((r: any) => !r.receipt)); assert.equal(result.closeFailed, true); }
    if (['unknown', 'malformed-receipt', 'cancel', 'crash', 'tree', 'fence', 'partial'].includes(scenario)) assert(result.gateSnapshot.reservations.length > 0);
    for (const r of result.ingress) { assert.equal(r.host, '127.0.0.1'); assert(!r.headerNames.some((h: string) => /task|attempt|grant|session/.test(h))); }
    await assert.rejects(docker(['exec', gateway, 'true'])); await assert.rejects(docker(['exec', worker, 'true']));
    evidence.cases.push({ scenario, result: 'passed', gatewayProfile, workerProfile, workerEvents, gateway: result, workerState, gatewayState, execAfterStopDenied: true }); delete evidence.pending; await save();
    console.log(JSON.stringify({ scenario, result: 'passed' }));
    await docker(['rm', gateway, worker]); ids.splice(ids.indexOf(gateway), 1); ids.splice(ids.indexOf(worker), 1); await docker(['volume', 'rm', volume]); volumes.splice(volumes.indexOf(volume), 1);
  }
  assert(evidence.cases.length > 0); evidence.result = selected ? 'selected-cases-passed' : 'synthetic-harness-router-passed';
} catch (error: any) {
  evidence.error = redact(error.stack ?? String(error)); process.exitCode = 1; console.error(evidence.error);
  evidence.failedContainers = []; for (const id of ids) try { evidence.failedContainers.push({ name: id, state: (await inspect(id)).State, logs: await docker(['logs', id]) }); } catch {}
} finally {
  cleaning = true; const errors: string[] = [];
  for (const id of ids) try { assert.equal((await inspect(id)).Config.Labels[LABEL], runId); await docker(['rm', '--force', id]); } catch { errors.push('container:' + id); }
  for (const volume of volumes) try { assert.equal(JSON.parse(await docker(['volume', 'inspect', volume]))[0].Labels[LABEL], runId); await docker(['volume', 'rm', volume]); } catch { errors.push('volume:' + volume); }
  const containers = await docker(['ps', '-aq', '--filter', `label=${LABEL}=${runId}`]), remainingVolumes = await docker(['volume', 'ls', '-q', '--filter', `label=${LABEL}=${runId}`]);
  evidence.cleanup = { verified: !errors.length && !containers && !remainingVolumes, containersRemaining: containers ? containers.split('\n').length : 0, volumesRemaining: remainingVolumes ? remainingVolumes.split('\n').length : 0, errors };
  if (!evidence.cleanup.verified || stop.signal.aborted) { evidence.result = 'blocked'; process.exitCode = 1; }
  await save(); if (evidence.cleanup.verified) await rm(staging, { recursive: true, force: true }); console.log(JSON.stringify({ result: evidence.result, cases: evidence.cases.length, cleanup: evidence.cleanup.verified }));
}
