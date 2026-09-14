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
