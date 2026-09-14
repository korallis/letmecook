import test from 'node:test';
import assert from 'node:assert/strict';
import { createResponsesTerminalObserver as observer } from './overlay/responses-terminal.mjs';
import { responseId, message, functionOne, functionTwo, created, completed, failed, incomplete,
  itemAdded, itemDone, delta, frame, frames, nativeToolEvents } from './native-fixtures.mjs';

const encode = value => new TextEncoder().encode(value);
function consume(text, options = {}, reason = 'eof') {
  const subject = observer(options); subject.push(encode(text)); return subject.finish({ reason });
}
function terminal(text, kind = 'completed') {
  const state = consume(text);
  assert.equal(state.disposition, 'provider_terminal', JSON.stringify(state));
  assert.equal(state.terminal.kind, kind); assert.equal(state.responseId, responseId);
  return state;
}
function unknown(text, invalidReason) {
  const state = consume(text);
  assert.equal(state.disposition, 'unknown'); assert.equal(state.terminal, null);
  if (invalidReason !== undefined) assert.equal(state.invalidReason, invalidReason);
  return state;
}

test('native completed: authoritative identity/status, optional end_turn, provisional until finish', () => {
  const subject = observer(); subject.push(encode(frames([created(), completed()])));
  assert.equal(subject.state.disposition, 'unknown');
  assert.equal(subject.state.terminalCandidate.responseId, responseId);
  assert.equal(subject.state.terminal, null);
  const result = subject.finish(); assert.equal(result.disposition, 'provider_terminal');
  assert.equal(result.terminal.endTurn, true);
  assert.throws(() => { result.responseId = 'forged'; }, TypeError);
  assert.throws(() => { result.terminal.kind = 'failed'; }, TypeError);
});
test('valid failed and incomplete are provider terminals, never completed successes', () => {
  terminal(frames([created(), failed()]), 'failed');
  terminal(frames([created(), incomplete()]), 'incomplete');
});
test('every binary split including UTF8 and CRLF preserves valid evidence', () => {
  const input = encode(frames([created(), itemDone(message, 0), completed()], { newline: '\r\n' }));
  for (let split = 0; split <= input.length; split++) {
    const subject = observer(); subject.push(input.subarray(0, split)); subject.push(input.subarray(split));
    assert.equal(subject.finish().disposition, 'provider_terminal', `split=${split}`);
  }
  const subject = observer(); for (const byte of input) subject.push(Uint8Array.of(byte));
  assert.equal(subject.finish().disposition, 'provider_terminal');
});
test('SSE accepts comments, data-only type, multiline JSON and CR separators', () => {
  terminal(': keepalive\r\r' + frame(created(), { newline: '\r', named: false })
    + 'event: response.completed\rdata: {"type":"response.completed",\rdata: "response":'
    + JSON.stringify(completed().response) + '}\r\r');
});
test('interleaved native function items retain distinct completed arguments', () => {
  terminal(frames(nativeToolEvents()));
});
test('native text content lifecycle and custom tools have bounded supported shapes', () => {
  terminal(frames([created(), itemAdded({ ...message, content: [] }, 0),
    { type: 'response.content_part.added', item_id: message.id, output_index: 0, content_index: 0,
      part: { type: 'output_text', text: '' } },
    { type: 'response.output_text.delta', item_id: message.id, output_index: 0, content_index: 0, delta: 'Hello 🌍 café' },
    { type: 'response.output_text.done', item_id: message.id, output_index: 0, content_index: 0, text: 'Hello 🌍 café' },
    { type: 'response.content_part.done', item_id: message.id, output_index: 0, content_index: 0,
      part: { type: 'output_text', text: 'Hello 🌍 café' } }, itemDone(message, 0), completed()]));
  const custom = { id: 'ct_01', type: 'custom_tool_call', status: 'completed', call_id: 'call_ct', name: 'patch', input: 'freeform text' };
  terminal(frames([created(), itemDone(custom, 0), completed([custom])]));
});
test('completed final items need not have separate done events; reasoning without status does', () => {
  terminal(frames([created(), completed([functionOne, functionTwo])]));
  const reasoning = { id: 'rs_01', type: 'reasoning', summary: [{ type: 'summary_text', text: 'Plan' }] };
  terminal(frames([created(), itemDone(reasoning, 0), completed([reasoning])]));
  unknown(frames([created(), completed([reasoning])]), 'reasoning_completion_unproved');
});
test('empty completed output and refusal may terminate inference, not certify a useful task result', () => {
  terminal(frames([created(), completed([])]));
  terminal(frames([created(), completed([{ ...message, content: [{ type: 'refusal', refusal: 'No' }] }])]));
});
test('EOF, cancellation and local error cannot create terminal evidence', () => {
  for (const reason of ['eof', 'cancel', 'error']) {
    const noTerminal = consume(frame(created()), {}, reason);
    assert.equal(noTerminal.disposition, 'unknown'); assert.equal(noTerminal.localEnd, reason);
    const withTerminal = consume(frames([created(), completed()]), {}, reason);
    assert.equal(withTerminal.disposition, 'provider_terminal');
  }
  unknown(''); unknown('data: [DONE]\n\n');
  unknown(frame(created()) + 'data: [DONE]\n\n');
  terminal(frames([created(), completed()]) + 'data: [DONE]\n\n');
});
test('malformed UTF8 including unfinished multibyte line remains unknown', () => {
  const subject = observer(); subject.push(encode(frame(created())));
  subject.push(Uint8Array.of(0xc3, 0x28, 10, 10));
  assert.equal(subject.finish().invalidReason, 'invalid_utf8');
  const partial = observer(); partial.push(Uint8Array.of(0xf0, 0x9f));
  assert.equal(partial.finish().disposition, 'unknown');
});
test('terminal must be a completely framed SSE JSON event', () => {
  const start = frame(created()), end = frame(completed());
  for (let cut = 1; cut < end.length; cut++) unknown(start + end.slice(0, cut));
  unknown(start + 'data: {broken}\n\n', 'malformed_json');
  unknown(start + 'event: response.completed\n\n', 'missing_event_data');
});
test('conflicting terminal or any event after terminal revokes provisional evidence', () => {
  for (const next of [completed(), failed(), incomplete(), created(), { type: 'response.output_text.delta', delta: 'late' }]) {
    unknown(frames([created(), completed(), next]), 'event_after_terminal');
  }
  unknown(frames([created(), failed(), completed()]), 'event_after_terminal');
  unknown(frames([created(), completed()]) + 'data: {', 'truncated_frame');
});
test('duplicate and reordered creation, sequence, and item events fail closed', () => {
  unknown(frames([completed()]), 'event_before_created');
  unknown(frames([created(), created()]), 'invalid_or_duplicate_created');
  unknown(frames([{ ...created(), sequence_number: 5 }, { ...completed(), sequence_number: 5 }]), 'reordered_or_duplicate_sequence');
  unknown(frames([{ ...created(), sequence_number: 5 }, { ...completed(), sequence_number: 4 }]), 'reordered_or_duplicate_sequence');
  unknown(frames([{ ...created(), sequence_number: 0 }, completed()]), 'inconsistent_sequence_numbers');
  unknown(frames([created(), itemDone(functionOne, 0), itemDone(functionOne, 0)]), 'duplicate_done_item');
  unknown(frames([created(), itemDone(functionOne, 0), itemAdded(functionOne, 0)]), 'duplicate_or_reordered_item');
  unknown(frames([created(), itemDone(functionOne, 0), delta(functionOne, 0, '{}')]), 'event_after_item_done');
  unknown(frames([created(), delta(functionOne, 0, '{}')]), 'unknown_item_id');
});
test('identity, event names and status must agree exactly', () => {
  unknown(frame(created()) + frame(completed()).replace('event: response.completed', 'event: response.failed'), 'event_type_mismatch');
  const mismatched = completed(); mismatched.response.id = 'resp_other';
  unknown(frames([created(), mismatched]), 'response_id_mismatch');
  unknown(frames([created(), { ...completed(), response_id: 'resp_other' }]), 'response_id_mismatch');
  const wrongStatus = completed(); wrongStatus.response.status = 'in_progress';
  unknown(frames([created(), wrongStatus]), 'terminal_status_mismatch');
  unknown(frames([created(), { ...completed(), type: 'response.done' }]), 'unsupported_terminal_alias');
  unknown(frames([created(), { ...completed(), type: 'response.in_progress' }]), 'nonterminal_status_mismatch');
  unknown(frame(created()) + 'event: response.completed\ndata: [DONE]\n\n', 'named_done_sentinel');
});
test('JSON ambiguity and excessive nesting cannot establish completion', () => {
  unknown(frame(created()) + 'data: {"type":"response.failed","type":"response.completed","response":'
    + JSON.stringify(completed().response) + '}\n\n', 'duplicate_json_key');
  unknown(frame(created()) + 'data: {"type":"response.in_progress","x":'
    + '['.repeat(70) + '0' + ']'.repeat(70) + '}\n\n', 'json_depth_limit');
});
test('malformed intermediate events and unknown types cannot be laundered by completion', () => {
  unknown(frames([created(), { type: 'response.in_progress' }, completed()]), 'missing_response');
  unknown(frames([created(), { type: 'response.future_unknown' }, completed()]), 'unsupported_event_type');
  unknown(frames([created(), itemAdded(functionOne, 0), { ...delta(functionOne, 0, 'x'), delta: 12 }, completed()]), 'invalid_text_event');
  unknown(frames([created(), itemAdded({ ...message, content: [] }, 0), {
    type: 'response.output_text.delta', item_id: message.id, output_index: 0, content_index: -1, delta: 'text',
  }, completed()]), 'invalid_content_index');
  unknown(frames([created(), itemAdded(message, 0), delta({ ...functionOne, id: message.id }, 0, 'x')]), 'item_event_type_mismatch');
  const done = { type: 'response.function_call_arguments.done', item_id: functionOne.id, output_index: 0, arguments: functionOne.arguments };
  unknown(frames([created(), itemAdded(functionOne, 0), done, done]), 'event_after_channel_done');
  unknown(frames([created(), itemAdded(functionOne, 0), done, delta(functionOne, 0, 'late')]), 'event_after_channel_done');
});
test('failure/incomplete schemas reject contradictory or missing declarations', () => {
  const badFailure = failed(); badFailure.response.error = null;
  unknown(frames([created(), badFailure]), 'invalid_failure');
  const badIncomplete = incomplete(); badIncomplete.response.incomplete_details = {};
  unknown(frames([created(), badIncomplete]), 'invalid_incomplete');
  const conflict = completed(); conflict.response.error = { message: 'failed' };
  unknown(frames([created(), conflict]), 'contradictory_completed_response');
  const wrongEndTurn = completed(); wrongEndTurn.response.end_turn = 'yes';
  unknown(frames([created(), wrongEndTurn]), 'invalid_end_turn');
});
test('completed output items must be complete, consistent, bounded and correlated', () => {
  unknown(frames([created(), completed([{ ...message, status: 'in_progress' }])]), 'incomplete_output_item');
  unknown(frames([created(), completed([{ ...functionOne, arguments: '{' }])]), 'invalid_tool_arguments');
  unknown(frames([created(), completed([functionOne, { ...functionTwo, call_id: functionOne.call_id }])]), 'duplicate_call_id');
  unknown(frames([created(), completed([message, message])]), 'duplicate_terminal_item');
  unknown(frames([created(), itemDone(functionOne, 0), completed([{ ...functionOne, arguments: '{}' }])]), 'terminal_item_mismatch');
  unknown(frames([created(), itemDone(functionOne, 0), completed([])]), 'missing_terminal_item');
  unknown(frames([created(), itemAdded(functionOne, 0), itemAdded(functionTwo, 0)]), 'conflicting_output_index');
  unknown(frames([created(), completed([{ id: 'hosted', type: 'web_search_call', status: 'completed' }])]), 'unsupported_output_item');
});
test('byte, frame, event, output-item, and no-newline attacks hit explicit bounds', () => {
  const text = frames([created(), completed()]);
  assert.equal(consume(text, { maxBytes: 10 }).invalidReason, 'byte_limit');
  assert.equal(consume(text, { maxEventBytes: 50 }).invalidReason, 'event_byte_limit');
  assert.equal(consume(text, { maxEvents: 1 }).invalidReason, 'event_limit');
  assert.equal(consume(frames([created(), completed([message, functionOne])]), { maxOutputItems: 1 }).invalidReason, 'invalid_terminal_output');
  assert.equal(consume('x'.repeat(4096), { maxEventBytes: 32 }).invalidReason, 'event_byte_limit');
  assert.throws(() => observer({ maxEvents: 0 }), TypeError);
  assert.throws(() => observer({ unexpected: 1 }), TypeError);
});
test('observer finalization is idempotent; invalid evidence cannot recover', () => {
  const subject = observer(); subject.push(encode('data: nope\n\n'));
  subject.push(encode(frames([created(), completed()])));
  assert.equal(subject.finish().invalidReason, 'malformed_json');
  assert.deepEqual(subject.finish(), subject.finish({ reason: 'cancel' }));
  assert.throws(() => subject.push(encode('')), /observer_finished/);
  assert.throws(() => observer().push('text'), TypeError);
});

test('PR78 regression: added call ID/name cannot change in done/completed', () => {
  for (const patch of [{ call_id: 'call_previous' }, { name: 'previous_tool' }, { namespace: 'previous_namespace' }]) {
    const events = nativeToolEvents().map(event => event.type === 'response.output_item.added' && event.item.id === functionOne.id
      ? { ...event, item: { ...event.item, ...patch } } : event);
    unknown(frames(events), 'item_identity_mismatch');
  }
  // A namespace introduced only in a completed item also changes the declared root namespace.
  unknown(frames([created(), itemAdded(functionOne, 0), completed([{ ...functionOne, namespace: 'other' }])]), 'item_identity_mismatch');
});
test('PR78 regression: streamed different.txt cannot be laundered by one.txt done/final snapshots', () => {
  const events = nativeToolEvents().map(event => event.type === 'response.function_call_arguments.delta'
    && event.item_id === functionOne.id && event.delta.includes('one.txt')
    ? { ...event, delta: '"different.txt"}' } : event);
  unknown(frames(events), 'streamed_content_mismatch');
  unknown(frames(events.filter(event => event.type !== 'response.output_item.done')), 'streamed_content_mismatch');
});
test('function channel done and optional identity fields must corroborate accumulated deltas', () => {
  const prefix = [created(), itemAdded(functionOne, 0), delta(functionOne, 0, functionOne.arguments)];
  const done = { type: 'response.function_call_arguments.done', item_id: functionOne.id, output_index: 0,
    arguments: functionOne.arguments, call_id: functionOne.call_id, name: functionOne.name };
  terminal(frames([...prefix, done, itemDone(functionOne, 0), completed([functionOne])]));
  unknown(frames([...prefix, { ...done, arguments: '{"path":"different.txt"}' }]), 'streamed_content_mismatch');
  unknown(frames([...prefix, { ...done, call_id: 'call_wrong' }]), 'item_identity_mismatch');
  unknown(frames([...prefix, { ...done, name: 'wrong_tool' }]), 'item_identity_mismatch');
  unknown(frames([...prefix, done, completed([{ ...functionOne, arguments: '{}' }])]), 'streamed_content_mismatch');
});
test('matching repeated metadata and snapshots do not duplicate incremental tool arguments', () => {
  const added = itemAdded(functionOne, 0); added.item.arguments = '{"path":';
  const part = { ...delta(functionOne, 0, '"one.txt"}'), call_id: functionOne.call_id, name: functionOne.name };
  const done = { type: 'response.function_call_arguments.done', item_id: functionOne.id, output_index: 0,
    arguments: functionOne.arguments, name: functionOne.name };
  terminal(frames([created(), added, part, done, itemDone(functionOne, 0), completed([functionOne])]));
  // Authoritative complete items without deltas may extend a declared initial prefix.
  terminal(frames([created(), added, itemDone(functionOne, 0), completed([functionOne])]));
});
test('message delta and part-done text must match item-done and final completed text', () => {
  const added = itemAdded({ ...message, content: [] }, 0);
  const text = { type: 'response.output_text.delta', item_id: message.id, output_index: 0, content_index: 0, delta: 'different' };
  unknown(frames([created(), added, text, itemDone(message, 0), completed()]), 'streamed_content_mismatch');
  unknown(frames([created(), added, text, completed()]), 'streamed_content_mismatch');
  const partDone = { type: 'response.content_part.done', item_id: message.id, output_index: 0, content_index: 0,
    part: { type: 'output_text', text: 'different' } };
  unknown(frames([created(), added, partDone, itemDone(message, 0)]), 'streamed_content_mismatch');
  unknown(frames([created(), added, text, { ...partDone, part: { type: 'output_text', text: 'other' } }]), 'streamed_content_mismatch');
});
test('message initial prefix and repeated part snapshots support legitimate incremental content', () => {
  const initial = { ...message, content: [{ type: 'output_text', text: 'Hello ' }] };
  const partAdded = { type: 'response.content_part.added', item_id: message.id, output_index: 0, content_index: 0,
    part: { type: 'output_text', text: 'Hello ' } };
  const text = { type: 'response.output_text.delta', item_id: message.id, output_index: 0, content_index: 0, delta: '🌍 café' };
  terminal(frames([created(), itemAdded(initial, 0), partAdded, text, itemDone(message, 0), completed()]));
  unknown(frames([created(), itemAdded(initial, 0), completed([{ ...message, phase: 'commentary' }])]), 'item_identity_mismatch');
});
test('custom-tool and refusal streamed channels cannot disagree with complete snapshots', () => {
  const custom = { id: 'ct_01', type: 'custom_tool_call', status: 'completed', call_id: 'call_ct', name: 'patch', input: 'expected patch' };
  const added = itemAdded({ ...custom, input: '' }, 0);
  const text = { type: 'response.custom_tool_call_input.delta', item_id: custom.id, output_index: 0, delta: 'wrong patch' };
  unknown(frames([created(), added, text, itemDone(custom, 0), completed([custom])]), 'streamed_content_mismatch');
  terminal(frames([created(), added, { ...text, delta: custom.input }, itemDone(custom, 0), completed([custom])]));
  const refusal = { ...message, content: [{ type: 'refusal', refusal: 'declined' }] };
  const refusalDelta = { type: 'response.refusal.delta', item_id: message.id, output_index: 0, content_index: 0, delta: 'different refusal' };
  unknown(frames([created(), itemAdded({ ...refusal, content: [] }, 0), refusalDelta, completed([refusal])]), 'streamed_content_mismatch');
});
test('reasoning summary/content have distinct channels and must appear consistently at completion', () => {
  const reasoning = { id: 'rs_01', type: 'reasoning', status: 'completed',
    summary: [{ type: 'summary_text', text: 'summary' }], content: [{ type: 'reasoning_text', text: 'thinking' }] };
  const added = itemAdded({ ...reasoning, summary: [], content: [] }, 0);
  const summary = { type: 'response.reasoning_summary_text.delta', item_id: reasoning.id, output_index: 0, summary_index: 0, delta: 'summary' };
  const content = { type: 'response.reasoning_text.delta', item_id: reasoning.id, output_index: 0, content_index: 0, delta: 'thinking' };
  terminal(frames([created(), added, summary, content, itemDone(reasoning, 0), completed([reasoning])]));
  unknown(frames([created(), added, { ...summary, delta: 'wrong' }, itemDone(reasoning, 0)]), 'streamed_content_mismatch');
  unknown(frames([created(), added, content, completed([{ ...reasoning, content: undefined }])]), 'missing_output_channel');
  unknown(frames([created(), added, { ...summary, content_index: 0, summary_index: 1 }, completed([reasoning])]), 'conflicting_channel_index');
});
test('content part type switches and dropped streamed parts cannot establish completion', () => {
  const added = itemAdded({ ...message, content: [] }, 0);
  const text = { type: 'response.output_text.delta', item_id: message.id, output_index: 0, content_index: 0, delta: 'text' };
  unknown(frames([created(), added, text, completed([{ ...message, content: [] }])]), 'missing_output_channel');
  unknown(frames([created(), added, text, completed([{ ...message, content: [{ type: 'refusal', refusal: 'text' }] }])]), 'content_part_type_mismatch');
});
test('new accumulated channels preserve byte bounds and strict complete-argument parsing', () => {
  const subject = observer({ maxBytes: 700, maxEventBytes: 600 });
  for (const event of [created(), itemAdded(functionOne, 0), ...Array.from({ length: 10 }, () => delta(functionOne, 0, 'x'.repeat(80)))]) {
    subject.push(encode(frame(event)));
  }
  assert.equal(subject.finish().invalidReason, 'byte_limit');
  unknown(frames([created(), completed([{ ...functionOne, arguments: '{"path":"one","path":"other"}' }])]), 'duplicate_json_key');
  // Interleaved tool regression stream retains reconciliation at every binary boundary.
  const input = encode(frames(nativeToolEvents(), { newline: '\r\n' }));
  for (let at = 0; at <= input.length; at++) {
    const subject = observer(); subject.push(input.subarray(0, at)); subject.push(input.subarray(at));
    assert.equal(subject.finish().disposition, 'provider_terminal', `split=${at}`);
  }
});
