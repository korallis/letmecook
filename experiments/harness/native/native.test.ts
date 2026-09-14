import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { NativeEvents, classify, config, environment, describe, digest, settingsDigest, PROFILE } from './client.ts';
import { BRIEF, CONTENT, artifactMatches, validateInventory, validateRunRequest, start, type RunRequest, type Artifact } from './run.ts';
import { prepareRun } from './admission.ts';
import { candidateArtifact, candidateEnvelope, acknowledgeCandidate, type CandidateEvidence } from './evidence.ts';
import { validateHeaders } from './relay.ts';
import { workerFiles } from './staging.ts';
import { createFixture } from './fixture.ts';
import { workerArgs, inspectWorker } from './isolation.ts';
const capture = JSON.parse(readFileSync(new URL('../../../tests/fixtures/harness/native/captured.json', import.meta.url), 'utf8'));
const parsed = () => { const p = new NativeEvents(); for (const byte of Buffer.from(capture.consumer.output)) p.push(Buffer.of(byte)); p.end(); return p.values; };
function runRequest(): RunRequest { return { schema: 1, profile: PROFILE, bindingDigest: digest(capture.gateway.journal.decisions[0].router.binding), settingsDigest: settingsDigest('allow'), baseSHA: 'a'.repeat(40), brief: BRIEF, approval: 'allow', limits: { wallMs: 30000, outputBytes: 262144 }, token: 'f'.repeat(64) }; }
function evidence(): CandidateEvidence {
  const result = structuredClone(capture), request = runRequest(), decisions = result.gateway.journal.decisions;
  const observedArtifact: Artifact = { baseSHA: request.baseSHA, headSHA: request.baseSHA, path: 'greeting.txt', content: CONTENT, changedFiles: ['greeting.txt'], status: ' M greeting.txt\n', diff: 'diff --git a/greeting.txt b/greeting.txt\n-hello\n+hello from probe\n' };
  const requests = result.consumer.captures.map((c: any, i: number) => ({ ...c, rawBody: JSON.stringify(c.body), requestId: decisions[i].requestId, status: 200, startedAt: result.gateway.observed.decisions[i].at - 1, firstResponseAt: result.gateway.observed.decisions[i].at + 1, endedAt: result.gateway.observed.decisions[i].at + 2 }));
  // Retained proof did not capture adapter clocks/base/diff. These additions are
  // declared test fixtures, never presented as measured adapter runtime evidence.
  for (const r of requests) r.headers['content-length'] = String(Buffer.byteLength(r.rawBody));
  return { policy: result.gateway.policy, packetDigest: result.gateway.packetDigest, scope: result.gateway.scope, binding: decisions[0].router.binding, request,
    result: { outcome: 'completed_candidate', exitCode: 0, signal: null, events: parsed(), stderr: '', artifact: observedArtifact, bindingDigest: request.bindingDigest, settingsDigest: request.settingsDigest, usage: 'unverified_native_observation', localProcessExited: true, upstreamQuiescence: 'unknown' },
    requests, decisions, receipts: result.gateway.receipts, pendingReservations: result.gateway.journal.reservations, durableDecisionTimes: result.gateway.observed.decisions, observedArtifact };
}
test('native profile matches the actual captured settings without inheriting parent auth or disabling its builtin hook', () => {
  assert.equal(settingsDigest('allow'), capture.gateway.policy.native.harness.settings);
  assert.deepEqual(config('<boundary-grant>', 'allow'), capture.consumer.config);
  assert.deepEqual(environment(), capture.consumer.environment);
  assert(!Object.hasOwn(environment(), 'OPENCODE_DISABLE_DEFAULT_PLUGINS'));
  assert(!Object.keys(environment()).some(k => /(?:KEY|TOKEN|PROXY|OTEL)/.test(k) && k !== 'OPENCODE_EXPERIMENTAL_OUTPUT_TOKEN_MAX'));
  assert.equal(config('f'.repeat(64), 'ask').permission.edit, 'ask'); assert.notEqual(settingsDigest('ask'), settingsDigest('allow'));
  assert.equal(describe().workerMemoryMiB, 768); assert.equal(describe().providerOutputTokens, 'unavailable');
});
test('actual native tool/final records preserve split UTF8, raw identities and observed approval semantics', () => {
  const events = parsed(); assert.equal(events.length, 6); assert.equal(events[1].native.part.tool, 'apply_patch');
  assert.equal(classify(events, 0, null, false, false, true), 'completed_candidate');
  assert.equal(classify(events, 0, null, false, false, false), 'artifact_mismatch');
  const blocked = structuredClone(events); blocked[1].native.part.state.status = 'error'; blocked[1].native.part.state.error = 'The user rejected permission to use this specific tool call.';
  assert.equal(classify(blocked, 0, null, false, false, false), 'approval_blocked');
  blocked[1].native.part.state.error = 'Synthetic tool failure'; assert.equal(classify(blocked, 0, null, false, false, false), 'tool_failed');
  assert.equal(classify(events, 1, null, false, false, true), 'harness_failed'); assert.equal(classify(events, 0, null, true, false, true), 'cancelled_unknown'); assert.equal(classify(events, 0, null, false, true, true), 'invalid_events');
});
test('native decoder rejects old edit/write authority, duplicated calls, invalid UTF8 and truncated records', () => {
  for (const tool of ['edit', 'write', 'bash']) { const line = structuredClone(parsed()[1].native); line.part.tool = tool; assert.throws(() => new NativeEvents().push(Buffer.from(JSON.stringify(line) + '\n'))); }
  const decoder = new NativeEvents(); decoder.push(Buffer.from(capture.consumer.output)); assert.throws(() => decoder.push(Buffer.from(parsed()[1].raw + '\n')), /invalid_tool_event/);
  assert.throws(() => new NativeEvents().push(Buffer.from([255, 10]))); assert.throws(() => new NativeEvents(1).push(Buffer.from(capture.consumer.output)));
  const truncated = new NativeEvents(); truncated.push(Buffer.from('{')); assert.throws(() => truncated.end());
  const cross = new NativeEvents(); cross.push(Buffer.from(capture.consumer.output)); const line = structuredClone(parsed()[0].native); line.sessionID = 'ses_other'; assert.throws(() => cross.push(Buffer.from(JSON.stringify(line) + '\n')));
});
test('trusted preparation binds worker role, lease, native authority, exact settings, task and finite local limits', () => {
  const e = evidence(), now = e.binding.expiresAt - 40000;
  const { schema, profile, bindingDigest, settingsDigest: omitted, ...input } = e.request;
  assert.deepEqual(prepareRun(e.policy, e.binding, input, now), e.request);
  for (const change of [(x: any) => x.binding.role = 'planner', (x: any) => x.binding.routeId = 'other', (x: any) => x.binding.native.profileDigest = 'b'.repeat(64), (x: any) => x.binding.leaseExpiresAt = now, (x: any) => x.policy.native.harness.settings = 'c'.repeat(64)]) {
    const x = evidence(); change(x); assert.throws(() => prepareRun(x.policy, x.binding, input, now));
  }
  assert.throws(() => prepareRun(e.policy, e.binding, { ...input, approval: 'ask' }, now));
  for (const change of [(r: any) => r.profile = 'opencode-1.18.30-chat-edit-v1', (r: any) => r.brief = 'Run shell commands', (r: any) => r.settingsDigest = 'b'.repeat(64), (r: any) => r.limits.wallMs = 30001, (r: any) => r.limits.outputBytes = 0, (r: any) => r.extra = 'authority']) { const r = runRequest(); change(r); assert.throws(() => validateRunRequest(r)); }
  if (process.platform !== 'linux') assert.throws(() => start(runRequest()), /unverified_execution_environment/);
});
test('client metadata validates exact OpenCode origin and session without admitting alternate hosts or authority headers', () => {
  const c = capture.consumer.captures[0], raw = JSON.stringify(c.body), h: Record<string, string> = { ...c.headers, 'content-length': String(Buffer.byteLength(raw)), authorization: 'Bearer ' + 'f'.repeat(64) };
  const headers = validateHeaders(Object.entries(h).flat(), c.body, 'f'.repeat(64), raw); assert(!Object.hasOwn(headers, 'authorization'));
  for (const patch of [{ originator: 'codex_cli_rs' }, { 'session-id': 'ses_other' }, { 'x-provider-key': 'forbidden' }, { host: 'example.com' }, { 'user-agent': 'opencode/other' }, { authorization: 'Bearer wrong' }]) assert.throws(() => validateHeaders(Object.entries({ ...h, ...patch }).flat(), c.body, 'f'.repeat(64), raw));
  assert.throws(() => validateHeaders([...Object.entries(h).flat(), 'Host', h.host], c.body, 'f'.repeat(64), raw), /duplicate/);
});
test('candidate joins exact ordered continuation, native output digest, durable receipt, event and separately observed artifact', () => {
  const e = evidence(), artifact = candidateEnvelope(e), ack = { acknowledged: true, digest: digest(artifact), file: 'candidate-' + digest(artifact) + '.json' };
  assert.equal(artifact.artifact.metadata.callIds[0], 'call_synthetic'); assert.equal(artifact.requestIds.length, 2);
  assert.equal(acknowledgeCandidate(artifact, ack, [{ path: artifact.artifact.path, digest: ack.digest, file: ack.file, attemptId: artifact.attemptId, kind: artifact.kind }]).outcome, 'completed_candidate');
  assert.throws(() => acknowledgeCandidate(artifact, { ...ack, file: 'different.json' }, [{ path: artifact.artifact.path, digest: ack.digest, file: ack.file, attemptId: artifact.attemptId, kind: artifact.kind }])); assert.throws(() => acknowledgeCandidate(artifact, ack, [])); assert.throws(() => acknowledgeCandidate(artifact, capture.artifactAcknowledgement, [{ path: artifact.artifact.path, digest: ack.digest, file: ack.file, attemptId: artifact.attemptId, kind: artifact.kind }]));
});
test('unknown/corrupted receipts, false decisions, early output, altered history and forged worker artifacts cannot qualify', () => {
  const changes: ((e: any) => void)[] = [
    e => e.receipts[0] = { id: e.requests[0].requestId, known: false, quiescent: false }, e => e.receipts[0].operations[0].terminal = 'unknown',
    e => e.decisions[0].delivery = 'withheld', e => e.decisions[0].evidence.receiptDigest = 'a'.repeat(64), e => e.decisions[0].router.requestDigest = 'a'.repeat(64),
    e => e.decisions[0].nativeOutput[0].encrypted_content = 'changed', e => e.requests[1].body.input[2].encrypted_content = 'changed', e => e.requests[1].body.input.at(-1).call_id = 'orphan',
    e => e.requests.reverse(), e => e.requests[0].firstResponseAt = 1, e => e.requests.push(structuredClone(e.requests[1])),
    e => e.result.events[1].native.part.callID = 'forged', e => e.result.events[1].raw = e.result.events[1].raw.replace('call_synthetic', 'forged'),
    e => e.observedArtifact.content = 'wrong', e => e.observedArtifact.headSHA = 'b'.repeat(40), e => e.observedArtifact.changedFiles.push('unauthorized'),
    e => e.result.outcome = 'cancelled_unknown', e => e.result.localProcessExited = false, e => e.result.bindingDigest = 'c'.repeat(64),
    e => e.pendingReservations.push({ requestId: 'unknown-original' }), e => e.request.brief = 'Unapproved task', e => e.requests[0].rawBody = e.requests[0].rawBody.replace('"model":', '"model":"duplicate","model":'),
  ];
  for (const mutate of changes) { const e = evidence(); mutate(e); assert.throws(() => candidateArtifact(e)); }
});
test('client staging excludes authority and deterministic disposable Git fixture preserves base identity', () => {
  assert(!workerFiles.some(f => /authority|native-policy|admission|evidence|gateway|staging|control/.test(f)));
  validateInventory(['.git', 'greeting.txt'], ['pre-commit.sample']);
  for (const path of ['.opencode', 'opencode.json', '.claude', '.mcp.json']) assert.throws(() => validateInventory(['.git', 'greeting.txt', path], []));
  assert.throws(() => validateInventory(['.git', 'greeting.txt'], ['post-commit']));
  const directory = mkdtempSync(join(tmpdir(), 'gaffer-native-fixture-'));
  try { assert.equal(createFixture(join(directory, 'a')), createFixture(join(directory, 'b'))); assert.throws(() => createFixture(join(directory, 'a'))); } finally { rmSync(directory, { recursive: true }); }
  const e = evidence(); assert(artifactMatches(e.observedArtifact, e.request.baseSHA));
});
test('native worker enforces the separately measured 768 MiB profile and network-none worker topology', () => {
  const args = workerArgs('name', 'run');
  assert.equal(args[args.indexOf('--memory') + 1], '768m'); assert.equal(args[args.indexOf('--memory-swap') + 1], '768m'); assert.equal(args[args.indexOf('--network') + 1], 'none');
  assert.throws(() => inspectWorker({ HostConfig: { Memory: 512 * 1048576 } }, 'image', 'run', 'socket', '/staging'));
});
