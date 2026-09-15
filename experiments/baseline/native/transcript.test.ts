import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import { canonical } from '../../inference-boundary/json.ts';
import { classifyReceipt, hashDocument } from '../../inference-boundary/router-policy.ts';
import { continuationOutput, expectedPhysicalRequest } from '../../router-authority-extension/overlay/native-responses.mjs';
import { NativeEvents, digest, settingsDigest } from '../../harness/native/client.ts';
import { verifyBaselineTranscript } from './transcript.ts';

// Public synthetic capture supplies the pinned policy/tool/header schema only.
// All transcript bodies, clocks, events, receipts and debit rows below are invented test data.
const capture = JSON.parse(readFileSync(new URL('../../../tests/fixtures/harness/native/captured.json', import.meta.url), 'utf8'));
function fixture(count = 2, fallback = false, callsPerTurn = 1): any {
  const p = structuredClone(capture.gateway.policy), n = p.native, start = 1789430400000;
  n.authorization.caseRef = 'case01';
  n.scope = { ...n.scope, id: 'synthetic_case01', caseRef: 'case01', phase: 'baseline', authorizationDigest: digest(n.authorization), maxInferenceAttempts: 32, elapsedMs: 900000 };
  n.harness.settings = settingsDigest('allow');
  n.toolPaths = ['ci/first.yml', 'ci/second.yml'];
  for (const c of n.connections) c.expiresAt = start + 3600000;
  for (const c of p.graph.connections) c.expiresAt = start + 3600000;
  p.graph.profileDigest = digest(n); p.graph.bounds.scope = n.scope; p.authority.graphDigest = digest(p.graph);
  const b = structuredClone(capture.gateway.journal.decisions[0].router.binding);
  b.expiresAt = b.leaseExpiresAt = start + 600000;
  b.native = { profileDigest: digest(n), scopeId: n.scope.id, authorizationDigest: n.scope.authorizationDigest };
  const scope = { id: n.scope.id, phase: 'baseline', spec: canonical(n.scope), boot: p.authority.boot, started: start, deadline: start + n.scope.elapsedMs, spent: count + Number(fallback), state: 'active' };
  const e: any = { schema: 1, origin: 'synthetic', caseId: 'case01', expected: { prompt: 'Pin the public synthetic CI actions.', context: 'Public synthetic case01: only the two authorized CI files may change.', physicalInstructions: 'Synthetic pinned provider instructions.', policyDigest: digest(p), bindingDigest: digest(b), packetDigest: digest('synthetic packet'), scopeDigest: digest(scope) }, policy: p, binding: b, packetDigest: digest('synthetic packet'), scope, requests: [], decisions: [], receipts: [], pendingReservations: [], durableDecisionTimes: [], physicalRequests: [], scopeOperations: [], events: [], exitCode: 0, signal: null, localProcessExited: true };
  let previous: any = null, part = 0; const native: any[] = [];
  for (let i = 0; i < count; i++) {
    const at = start + 100 * (i + 1), id = (i + 1).toString(16).padStart(32, '0'), final = i === count - 1;
    const body: any = { ...structuredClone(capture.consumer.captures[0].body), prompt_cache_key: 'ses_case01', input: previous ? [...previous.body.input, ...continuationOutput(previous.output, n), ...previous.calls.map((c: any) => ({ type: 'function_call_output', call_id: c.call_id, output: 'Applied ' + c.call_id }))] : [{ role: 'developer', content: e.expected.context }, { role: 'user', content: [{ type: 'input_text', text: e.expected.prompt }] }] };
    const rawBody = JSON.stringify(body), headers = { ...capture.consumer.captures[0].headers, 'content-length': String(Buffer.byteLength(rawBody)), 'session-id': body.prompt_cache_key, 'x-session-id': body.prompt_cache_key, 'x-session-affinity': body.prompt_cache_key };
    const calls = final ? [] : Array.from({ length: callsPerTurn }, (_, j) => ({ id: `fc_${i}_${j}`, type: 'function_call', call_id: `call_${i}_${j}`, name: 'apply_patch', arguments: JSON.stringify({ patchText: `*** Begin Patch\n*** Update File: ci/${j % 2 ? 'second' : 'first'}.yml\n@@\n-action: old${i}\n+action: pin${i}\n*** End Patch` }), status: 'completed' }));
    const output = final ? [{ id: `msg_${i}`, type: 'message', role: 'assistant', status: 'completed', content: [{ type: 'output_text', text: 'Synthetic pins updated 🌍.', annotations: [] }] }] : [{ id: `rs_${i}`, type: 'reasoning', summary: [{ type: 'summary_text', text: 'Synthetic reasoning.' }], encrypted_content: `opaque-synthetic-${i}` }, ...calls];
    const router = { policy: p, binding: b, requestDigest: hashDocument(JSON.stringify(body)), send: 'send_possible' as const, nativeRequest: body };
    const operations = Array.from({ length: i === 0 && fallback ? 2 : 1 }, (_, j) => {
      const rejected = i === 0 && fallback && j === 0;
      return { request_id: id, ordinal: j + 1, boot: p.authority.boot, generation: p.authority.generation, revision: p.revision, provider: 'codex', model: 'gpt-6-astra', connection_id: n.connections[j].id, terminal: rejected ? 'provider_rejected' : 'provider_completed', local_stop: 'original_eof', scope_id: scope.id, body_digest: digest(expectedPhysicalRequest(body, 'gpt-6-astra', e.expected.physicalInstructions)), output_digest: rejected ? null : digest(output) };
    });
    const receipt = { id, known: true, quiescent: true, boot: p.authority.boot, generation: p.authority.generation, revision: p.revision, route: p.routerModel, handler_done: 1, local_stop: 'local_eof', native: { profileDigest: digest(n), scopeId: scope.id, authorizationDigest: n.scope.authorizationDigest, bindingDigest: digest(b), requestDigest: router.requestDigest }, operations };
    e.requests.push({ path: '/v1/responses', headers, body, rawBody, requestId: id, status: 200, startedAt: at, firstResponseAt: at + 3, endedAt: at + 4 });
    e.decisions.push({ requestId: id, taskId: b.taskId, attemptId: b.attemptId, router, verdict: 'validated_success', evidence: classifyReceipt(receipt, id, router), completionDigest: digest(`synthetic response frames ${i}`), delivery: 'completed', nativeOutput: output });
    e.receipts.push(receipt); e.durableDecisionTimes.push({ requestId: id, at: at + 2 });
    for (const op of operations) {
      e.physicalRequests.push({ request_id: id, ordinal: op.ordinal, scope_id: scope.id, body: expectedPhysicalRequest(body, op.model, e.expected.physicalInstructions) });
      e.scopeOperations.push({ request_id: id, ordinal: op.ordinal, scope_id: scope.id, kind: 'inference', body_digest: op.body_digest, output_digest: op.output_digest });
    }
    const event = (type: string, offset: number, value: any) => native.push({ type, timestamp: at + offset, sessionID: body.prompt_cache_key, part: { id: 'prt_' + ++part, messageID: 'msg_step_' + i, sessionID: body.prompt_cache_key, ...value } });
    event('step_start', 5, { type: 'step-start' });
    for (const [j, call] of calls.entries()) event('tool_use', 8 + j, { type: 'tool', tool: 'apply_patch', callID: call.call_id, state: { status: 'completed', input: JSON.parse(call.arguments), output: 'Applied ' + call.call_id, time: { start: at + 6 + j, end: at + 7 + j } } });
    if (final) event('text', 10, { type: 'text', text: 'Synthetic pins updated 🌍.' });
    event('step_finish', 20, { type: 'step-finish', reason: final ? 'stop' : 'tool-calls' });
    previous = { body, output, calls };
  }
  const parser = new NativeEvents(); parser.push(Buffer.from(native.map(x => JSON.stringify(x)).join('\n') + '\n')); parser.end(); e.events = parser.values;
  return structuredClone(e);
}
function eventChange(e: any, index: number, change: (native: any) => void) {
  change(e.events[index].native); e.events[index].raw = JSON.stringify(e.events[index].native);
}
function resealOutput(e: any, i: number) {
  const outputDigest = digest(e.decisions[i].nativeOutput);
  e.receipts[i].operations.at(-1).output_digest = outputDigest;
  e.scopeOperations.filter((op: any) => op.request_id === e.requests[i].requestId).at(-1).output_digest = outputDigest;
  e.decisions[i].evidence = classifyReceipt(e.receipts[i], e.requests[i].requestId, e.decisions[i].router);
}
function resealRequest(e: any, i: number) {
  const r = e.requests[i], d = e.decisions[i];
  r.rawBody = JSON.stringify(r.body); r.headers['content-length'] = String(Buffer.byteLength(r.rawBody));
  d.router.nativeRequest = structuredClone(r.body); d.router.requestDigest = hashDocument(r.rawBody);
  e.receipts[i].native.requestDigest = d.router.requestDigest;
  for (const physical of e.physicalRequests.filter((op: any) => op.request_id === r.requestId)) physical.body = expectedPhysicalRequest(r.body, 'gpt-6-astra', e.expected.physicalInstructions);
  const hash = digest(expectedPhysicalRequest(r.body, 'gpt-6-astra', e.expected.physicalInstructions));
  for (const op of [...e.receipts[i].operations, ...e.scopeOperations.filter((op: any) => op.request_id === r.requestId)]) op.body_digest = hash;
  d.evidence = classifyReceipt(e.receipts[i], r.requestId, d.router);
}

test('two and three requests preserve every ordered call, encrypted continuation and final text', () => {
  for (const count of [2, 3]) for (const calls of [1, 2]) {
    const e = fixture(count, false, calls), before = structuredClone(e), result = verifyBaselineTranscript(e);
    assert.deepEqual(e, before); assert.deepEqual(result.requestIds, e.requests.map((r: any) => r.requestId));
    assert.equal(result.physicalAttempts, count); assert.equal(result.callIds.length, (count - 1) * calls);
    assert.equal(result.finalText, 'Synthetic pins updated 🌍.'); assert.equal(result.transcriptDigest, digest(e));
    assert.equal(result.promptDigest, digest(e.expected.prompt)); assert.equal(result.contextDigest, digest(e.expected.context));
  }
});
test('delayed CLI event delivery is allowed, but a continuation before tool execution ends is refused', () => {
  const e=fixture(), next=e.requests[1].startedAt;
  eventChange(e,1,n=>n.timestamp=next+1);eventChange(e,2,n=>n.timestamp=next+2);
  assert.equal(verifyBaselineTranscript(e).physicalAttempts,2);
  eventChange(e,1,n=>n.part.state.time.end=next+1);
  assert.throws(()=>verifyBaselineTranscript(e),/baseline_tool_before_durable/);
});
test('fallback is charged as a distinct per-request ordinal and matched to an original terminal receipt', () => {
  for (const count of [2, 3]) {
    const e = fixture(count, true), result = verifyBaselineTranscript(e);
    assert.equal(result.physicalAttempts, count + 1); assert.deepEqual(e.physicalRequests.slice(0, 3).map((o: any) => o.ordinal), [1, 2, 1]);
    e.scope.state = 'closed'; e.expected.scopeDigest = digest(e.scope); assert.equal(verifyBaselineTranscript(e).physicalAttempts, count + 1);
  }
});

const corruptions: [string, (e: any) => void][] = [
  ['top-level unknown', e => e.extra = true],
  ['expected unknown', e => e.expected.extra = true],
  ['live origin', e => e.origin = 'live'],
  ['other case', e => e.caseId = 'other'],
  ['prompt binding', e => e.expected.prompt += ' changed'],
  ['context binding', e => e.expected.context += ' changed'],
  ['policy binding', e => e.expected.policyDigest = digest('other')],
  ['binding digest', e => e.expected.bindingDigest = digest('other')],
  ['packet binding', e => e.packetDigest = digest('other')],
  ['scope binding', e => e.expected.scopeDigest = digest('other')],
  ['scope unknown field', e => { e.scope.extra = true; e.expected.scopeDigest = digest(e.scope); }],
  ['scope spec unknown field', e => { e.scope.spec = JSON.stringify({ ...JSON.parse(e.scope.spec), extra: true }); e.expected.scopeDigest = digest(e.scope); }],
  ['scope deadline', e => { e.scope.deadline++; e.expected.scopeDigest = digest(e.scope); }],
  ['missing charged fallback', e => { e.scope.spent--; e.expected.scopeDigest = digest(e.scope); }],
  ['reused scope spend', e => { e.scope.spent++; e.expected.scopeDigest = digest(e.scope); }],
  ['worker not exited', e => e.localProcessExited = false],
  ['worker failed', e => e.exitCode = 1],
  ['worker signal', e => e.signal = 'SIGTERM'],
  ['pending reservations', e => e.pendingReservations.push({ requestId: 'pending' })],
  ['missing decision', e => e.decisions.pop()],
  ['request reordered', e => e.requests.reverse()],
  ['decision reordered', e => e.decisions.reverse()],
  ['receipt reordered', e => e.receipts.reverse()],
  ['durability reordered', e => e.durableDecisionTimes.reverse()],
  ['physical reordered', e => e.physicalRequests.reverse()],
  ['debit reordered', e => e.scopeOperations.reverse()],
  ['duplicate request', e => e.requests[1].requestId = e.requests[0].requestId],
  ['duplicate receipt', e => e.receipts[1] = e.receipts[0]],
  ['duplicate physical', e => e.physicalRequests[1] = e.physicalRequests[0]],
  ['invented global ordinal', e => e.physicalRequests[0].globalOrdinal = 1],
  ['wrong debit ordinal', e => e.scopeOperations[0].ordinal = 2],
  ['wrong debit scope', e => e.scopeOperations[0].scope_id = 'other'],
  ['refresh debit', e => e.scopeOperations[0].kind = 'refresh'],
  ['debit digest', e => e.scopeOperations[0].body_digest = digest('other')],
  ['debit output', e => e.scopeOperations[0].output_digest = digest('other')],
  ['physical body', e => e.physicalRequests[0].body.instructions += ' changed'],
  ['physical expected instructions', e => e.expected.physicalInstructions += ' changed'],
  ['request unknown', e => e.requests[0].extra = true],
  ['decision unknown', e => e.decisions[0].extra = true],
  ['decision router unknown', e => e.decisions[0].router.extra = true],
  ['decision evidence unknown', e => e.decisions[0].evidence.extra = true],
  ['receipt unknown field', e => e.receipts[0].extra = true],
  ['durability unknown', e => e.durableDecisionTimes[0].extra = true],
  ['physical unknown', e => e.physicalRequests[0].extra = true],
  ['debit unknown', e => e.scopeOperations[0].extra = true],
  ['unknown receipt', e => e.receipts[0] = { id: e.requests[0].requestId, known: false, quiescent: false }],
  ['unknown fallback', e => { e.receipts[0].operations[0].terminal = 'unknown'; e.receipts[0].operations[0].local_stop = 'transport_error'; e.receipts[0].quiescent = false; }],
  ['unknown final operation', e => e.receipts.at(-1).operations[0].terminal = 'unknown'],
  ['receipt nonquiescent', e => e.receipts[0].quiescent = false],
  ['receipt handler running', e => e.receipts[0].handler_done = 0],
  ['unobserved delivery', e => e.decisions[0].delivery = 'unobserved'],
  ['failed decision', e => e.decisions[0].verdict = 'quiescent_failure'],
  ['invalid completion digest', e => e.decisions[0].completionDigest = null],
  ['premature response', e => e.requests[0].firstResponseAt = e.requests[0].startedAt],
  ['premature durability', e => e.durableDecisionTimes[0].at = e.requests[0].startedAt - 1],
  ['overlapping requests', e => e.requests[1].startedAt = e.requests[0].startedAt],
  ['past lease', e => { e.binding.expiresAt = e.scope.started; e.expected.bindingDigest = digest(e.binding); }],
  ['raw body mismatch', e => e.requests[0].rawBody += 'x'],
  ['duplicate JSON key', e => e.requests[0].rawBody = e.requests[0].rawBody.replace('{', '{"model":"other",')],
  ['auth in observation', e => e.requests[0].headers.authorization = 'Bearer synthetic'],
  ['unknown authority header', e => e.requests[0].headers['x-policy'] = 'extra'],
  ['missing tool event', e => e.events.splice(1, 1)],
  ['raw native event drift', e => e.events[0].native.timestamp++],
  ['native wrapper unknown', e => e.events[0].extra = true],
  ['unknown native event', e => { e.events[0].type = 'unknown'; eventChange(e, 0, n => n.type = 'unknown'); }],
  ['unknown native finish', e => eventChange(e, 3, n => n.part.reason = 'unknown')],
  ['native wrong session', e => eventChange(e, 1, n => n.sessionID = 'ses_other')],
  ['native duplicate part ID', e => eventChange(e, 1, n => n.part.id = e.events[0].native.part.id)],
  ['native wrong message', e => eventChange(e, 1, n => n.part.messageID = 'msg_other')],
  ['native event reordering', e => { [e.events[1], e.events[2]] = [e.events[2], e.events[1]]; e.events.forEach((x: any, i: number) => x.sequence = i + 1); }],
  ['premature tool start', e => eventChange(e, 1, n => n.part.state.time.start = e.durableDecisionTimes[0].at - 1)],
  ['missing tool clock', e => eventChange(e, 1, n => delete n.part.state.time)],
  ['tool error', e => eventChange(e, 1, n => { n.part.state.status = 'error'; n.part.state.error = 'Synthetic rejection'; })],
  ['tool output corruption', e => eventChange(e, 1, n => n.part.state.output = 'wrong')],
  ['tool patch corruption', e => eventChange(e, 1, n => n.part.state.input.patchText = n.part.state.input.patchText.replace('pin0', 'wrong'))],
  ['unauthorized tool', e => eventChange(e, 1, n => n.part.tool = 'bash')],
  ['final text corruption', e => eventChange(e, e.events.length - 2, n => n.part.text = 'wrong')],
  ['unclosed native step', e => e.events.pop()],
];
for (const [name, change] of corruptions) test('rejects ' + name, () => { const e = fixture(3, true, 2); change(e); assert.throws(() => verifyBaselineTranscript(e)); });

test('validly resealed output still must preserve the exact encrypted continuation', () => {
  const e = fixture(3); e.decisions[0].nativeOutput[0].encrypted_content += '-corrupt'; resealOutput(e, 0);
  assert.throws(() => verifyBaselineTranscript(e), /native_continuation_drift/);
});
test('validly resealed continuation must include every exact ordered tool result', () => {
  for (const change of [(r: any) => r.body.input.at(-1).output = 'wrong', (r: any) => { const input = r.body.input; [input[input.length - 1], input[input.length - 2]] = [input.at(-2), input.at(-1)]; }, (r: any) => r.body.input.pop()]) {
    const e = fixture(2, false, 2); change(e.requests[1]); resealRequest(e, 1); assert.throws(() => verifyBaselineTranscript(e));
  }
});
test('globally duplicated output and call IDs refuse even when receipts and continuation are resealed', () => {
  for (const key of ['id', 'call_id']) {
    const e = fixture(3); e.decisions[1].nativeOutput[1][key] = e.decisions[0].nativeOutput[1][key]; resealOutput(e, 1);
    assert.throws(() => verifyBaselineTranscript(e), /baseline_(output|call)_identity/);
  }
});
test('a nonfinal success with no pending call and a final response with pending calls refuse', () => {
  const early = fixture(3); early.decisions[1].nativeOutput = early.decisions[2].nativeOutput; resealOutput(early, 1);
  assert.throws(() => verifyBaselineTranscript(early), /baseline_native_terminal/);
  const pending = fixture(2); pending.decisions[1].nativeOutput.push({ ...pending.decisions[0].nativeOutput[1], id: 'fc_pending', call_id: 'call_pending' }); resealOutput(pending, 1);
  assert.throws(() => verifyBaselineTranscript(pending), /baseline_native_terminal/);
});
test('a known but failed last physical send cannot establish candidate success', () => {
  const e = fixture(2, true); const op = e.receipts[0].operations.at(-1); op.terminal = 'provider_failed'; op.output_digest = null;
  e.decisions[0].evidence = classifyReceipt(e.receipts[0], e.requests[0].requestId, e.decisions[0].router);
  assert.throws(() => verifyBaselineTranscript(e), /baseline_receipt_unknown/);
});
