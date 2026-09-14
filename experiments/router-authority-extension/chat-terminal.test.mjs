import test from 'node:test';
import assert from 'node:assert/strict';
import { createChatTerminalObserver as observer } from './overlay/chat-terminal.mjs';
import { responseId, model, chunk, frame, frames, textChunks, toolChunks, toolStart, toolPart } from './chat-fixtures.mjs';
const encode = text => new TextEncoder().encode(text);
function consume(text, options = {}, reason = 'eof') {
  const subject = observer(options); subject.push(encode(text)); return subject.finish({ reason });
}
function unknown(text, reason, options) {
  const result = consume(text, options);
  assert.equal(result.disposition, 'unknown', JSON.stringify(result)); assert.equal(result.terminal, null);
  if (reason !== undefined) assert.equal(result.invalidReason, reason);
  return result;
}
function valid(text, finishReason = 'stop') {
  const result = consume(text, { expectedModel: model });
  assert.equal(result.disposition, 'provider_terminal', JSON.stringify(result));
  assert.equal(result.terminal.responseId, responseId); assert.equal(result.terminal.model, model);
  assert.equal(result.terminal.finishReason, finishReason); return result;
}
test('original text finish plus DONE plus EOF yields immutable terminal, never a push-time receipt', () => {
  const subject = observer({ expectedModel: model }); subject.push(encode(frames(textChunks())));
  assert.equal(subject.state.disposition, 'unknown'); assert.equal(subject.state.terminal, null);
  assert.equal(subject.state.terminalCandidate.finishReason, 'stop');
  const result = subject.finish(); assert.equal(result.disposition, 'provider_terminal');
  assert.throws(() => { result.model = 'changed'; }, TypeError);
  assert.throws(() => { result.terminal.finishReason = 'tool_calls'; }, TypeError);
});
test('complete two-call tool arguments can interleave but declarations retain stable identity', () => {
  const result = valid(frames(toolChunks()), 'tool_calls'); assert.equal(result.terminal.toolCallCount, 2);
});
test('every binary split preserves text and tools with fragmented CRLF and UTF8', () => {
  for (const source of [textChunks(), toolChunks()]) {
    const input = encode(frames(source, { newline: '\r\n' }));
    for (let at = 0; at <= input.length; at++) {
      const subject = observer(); subject.push(input.subarray(0, at)); subject.push(input.subarray(at));
      assert.equal(subject.finish().disposition, 'provider_terminal', `split ${at}`);
    }
    const subject = observer(); for (const byte of input) subject.push(Uint8Array.of(byte));
    assert.equal(subject.finish().disposition, 'provider_terminal');
  }
});
test('SSE accepts LF, CR, message event, multiline JSON and fully framed comments', () => {
  valid(': keepalive\n\n' + frames(textChunks(), { named: true }));
  valid(frames(textChunks(), { newline: '\r' }));
  const first = JSON.stringify(chunk({ content: 'line' }));
  valid('data: ' + first.slice(0, first.indexOf(',') + 1) + '\ndata: '
    + first.slice(first.indexOf(',') + 1) + '\n\n' + frames([chunk({}, 'stop'), '[DONE]']));
});
test('missing or changing response ID cannot be repaired by later finish', () => {
  unknown(frames([chunk({}, null, { id: undefined }), chunk({}, 'stop'), '[DONE]']), 'missing_or_invalid_identity');
  unknown(frames([chunk({ content: 'first' }), chunk({}, 'stop', { id: 'other' }), '[DONE]']), 'response_id_changed');
});
test('missing, changing or unexpected model is unknown', () => {
  unknown(frames([chunk({}, null, { model: undefined }), chunk({}, 'stop'), '[DONE]']), 'missing_or_invalid_identity');
  unknown(frames([chunk({ content: 'first' }), chunk({}, 'stop', { model: 'changed-model' }), '[DONE]']), 'response_model_changed');
  unknown(frames(textChunks()), 'unexpected_response_model', { expectedModel: 'other-model' });
});
test('explicit upstream error plus ordinary choices/stop never becomes terminal', () => {
  for (const error of [{ message: 'failed' }, 'failed', false]) {
    unknown(frames([chunk({}, 'stop', { error }), '[DONE]']), 'explicit_upstream_error');
  }
  unknown(frame({ error: { message: 'overloaded' } }) + frames([chunk({}, 'stop'), '[DONE]']), 'explicit_upstream_error');
  unknown('event: error\ndata: {"error":{"message":"failed"}}\n\n' + frames(textChunks()), 'upstream_error_or_unsupported_event');
  unknown(frames([chunk({ content: 'partial' }), { error: { message: 'bad' } }, chunk({}, 'stop'), '[DONE]']), 'explicit_upstream_error');
});
test('bare EOF, DONE alone, stop alone, and reversed terminal order stay unknown', () => {
  unknown(''); unknown(frame(chunk({ content: 'partial' })));
  unknown(frame('[DONE]'), 'done_before_finish');
  unknown(frame(chunk({}, 'stop')));
  unknown(frames([chunk({ content: 'text' }), '[DONE]', chunk({}, 'stop')]), 'done_before_finish');
});
test('cancellation and error remain unknown even after valid finish plus DONE', () => {
  for (const reason of ['cancel', 'error']) {
    for (const source of ['', frame(chunk({}, 'stop')), frames(textChunks())]) {
      const result = consume(source, {}, reason); assert.equal(result.disposition, 'unknown');
      assert.equal(result.localEnd, reason); assert.equal(result.terminal, null);
    }
  }
});
test('duplicate/conflicting finish, data after finish, duplicate DONE and data after DONE poison evidence', () => {
  unknown(frames([chunk({}, 'stop'), chunk({}, 'stop'), '[DONE]']), 'event_after_finish');
  unknown(frames([chunk({}, 'stop'), chunk({}, 'tool_calls'), '[DONE]']), 'event_after_finish');
  unknown(frames([chunk({}, 'stop'), chunk({ content: 'late' }), '[DONE]']), 'event_after_finish');
  unknown(frames([...textChunks(), '[DONE]']), 'event_after_done');
  unknown(frames([...textChunks(), chunk({ content: 'late' })]), 'event_after_done');
});
test('every proper terminal frame truncation and missing final separator stays unknown', () => {
  const prefix = frame(chunk({ content: 'text' })), finish = frame(chunk({}, 'stop')), done = frame('[DONE]');
  for (let at = 1; at < finish.length; at++) unknown(prefix + finish.slice(0, at));
  for (let at = 1; at < done.length; at++) unknown(prefix + finish + done.slice(0, at));
  unknown(frames(textChunks()) + 'data: {', 'truncated_frame');
  unknown(frames(textChunks()) + ': unfinished comment', 'truncated_frame');
});
test('invalid UTF8 including post-terminal bytes and split invalid codepoints poisons evidence', () => {
  for (const prefix of ['', frames(textChunks())]) {
    const subject = observer(); subject.push(encode(prefix)); subject.push(Uint8Array.of(0xc3));
    subject.push(Uint8Array.of(0x28, 10, 10)); assert.equal(subject.finish().invalidReason, 'invalid_utf8');
  }
  const subject = observer(); subject.push(Uint8Array.of(0xf0, 0x9f));
  assert.equal(subject.finish().disposition, 'unknown');
});
test('malformed SSE, JSON, duplicate/escaped JSON keys, and depth overflow fail closed', () => {
  unknown('event: message\n\n', 'missing_event_data');
  unknown('id: abc\ndata: {}\n\n', 'unsupported_sse_field');
  unknown('event: message\nevent: message\ndata: {}\n\n', 'invalid_event_field');
  unknown('data: broken\n\n', 'malformed_json');
  const text = JSON.stringify(chunk({}, 'stop'));
  unknown('data: ' + text.replace('"id":', '"id":"wrong","id":') + '\n\n' + frame('[DONE]'), 'duplicate_json_key');
  unknown('data: ' + text.replace('"model":', '"mo\\u0064el":"wrong","model":') + '\n\n' + frame('[DONE]'), 'duplicate_json_key');
  unknown(frame(chunk({}, 'stop', { nested: [[[[[0]]]]] })) + frame('[DONE]'), 'json_depth_limit', { maxJsonDepth: 4 });
});
test('only one selected choice and valid chunk/delta envelopes are supported', () => {
  unknown(frame(chunk({}, 'stop', { object: 'chat.completion' })), 'invalid_chunk_type');
  unknown(frame(chunk({}, 'stop', { choices: [] })), 'unsupported_choices');
  const extra = chunk(); extra.choices.push({ index: 1, delta: {}, finish_reason: 'stop' });
  unknown(frame(extra), 'unsupported_choices');
  const other = chunk(); other.choices[0].index = 1; unknown(frame(other), 'unsupported_choice_index');
  unknown(frame(chunk({ role: 'user' })), 'invalid_delta_role');
  unknown(frame(chunk({ content: { text: 'fake' } })), 'invalid_delta_content');
  unknown(frame(chunk({ reasoning_content: 'not profiled' })), 'unsupported_fields');
  unknown(frame(chunk({ refusal: 'not a success' }, 'stop')), 'unsupported_fields');
});
test('unsupported finish reasons and usage-only post-finish chunks never certify success', () => {
  for (const reason of ['length', 'content_filter', 'function_call', 'error', 'cancelled']) {
    unknown(frames([chunk({}, reason), '[DONE]']), 'unsupported_finish_reason');
  }
  unknown(frames([chunk({}, 'stop'), chunk({}, null, { choices: [], usage: { total_tokens: 4 } }), '[DONE]']), 'event_after_finish');
});
test('tool_calls finish requires tools and stop rejects any pending tool', () => {
  unknown(frames([chunk({}, 'tool_calls'), '[DONE]']), 'finish_tool_mismatch');
  unknown(frames([chunk({ tool_calls: [toolStart(0, 'call_01', 'read_file', '{}')] }), chunk({}, 'stop'), '[DONE]']), 'finish_tool_mismatch');
});
test('incomplete, scalar, array, duplicate-key and overly deep function arguments cannot finish', () => {
  for (const [args, error] of [['{', 'incomplete_tool_arguments'], ['', 'incomplete_tool_arguments'],
    ['[]', 'invalid_tool_argument_object'], ['null', 'invalid_tool_argument_object'], ['42', 'invalid_tool_argument_object'],
    ['{"path":"one","path":"two"}', 'duplicate_json_key']]) {
    unknown(frames([chunk({ tool_calls: [toolStart(0, 'call_01', 'read_file', args)] }), chunk({}, 'tool_calls'), '[DONE]']), error);
  }
  const args = '{"nested":' + '['.repeat(70) + '0' + ']'.repeat(70) + '}';
  unknown(frames([chunk({ tool_calls: [toolStart(0, 'call_01', 'read_file', args)] }), chunk({}, 'tool_calls'), '[DONE]']), 'json_depth_limit');
});
test('call declarations need unique stable indices, IDs, names and function type', () => {
  const start = () => chunk({ tool_calls: [toolStart(0, 'call_01', 'read_file', '{')] });
  unknown(frames([chunk({ tool_calls: [toolStart(1, 'call_01', 'read_file', '{}')] })]), 'reordered_tool_declaration');
  unknown(frames([start(), chunk({ tool_calls: [toolStart(1, 'call_01', 'read_file', '{}')] })]), 'duplicate_call_id');
  unknown(frames([start(), chunk({ tool_calls: [toolStart(0, 'call_changed', 'read_file', '}')] })]), 'repeated_or_changed_tool_identity');
  unknown(frames([start(), chunk({ tool_calls: [toolStart(0, 'call_01', 'changed_name', '}')] })]), 'repeated_or_changed_tool_identity');
  unknown(frames([start(), chunk({ tool_calls: [toolStart(0, 'call_01', 'read_file', '}')] })]), 'repeated_or_changed_tool_identity');
  unknown(frame(chunk({ tool_calls: [toolStart(0, 'call_01', 'read_file', '{}'), toolPart(0, '')] })), 'duplicate_tool_index');
  unknown(frame(chunk({ tool_calls: [{ ...toolStart(0, 'call_01', 'read_file'), type: 'custom' }] })), 'unsupported_tool_type');
  unknown(frame(chunk({ tool_calls: [toolPart(0, '{}')] })), 'invalid_tool_identity');
});
test('finite byte, frame, event, call and argument limits reject resource-exhaustion inputs', () => {
  unknown(frames(textChunks()), 'byte_limit', { maxBytes: 10 });
  unknown(frames(textChunks()), 'event_byte_limit', { maxEventBytes: 50 });
  unknown(frames(textChunks()), 'event_limit', { maxEvents: 2 });
  unknown(frames(toolChunks()), 'invalid_tool_index', { maxToolCalls: 1 });
  unknown(frames(toolChunks()), 'argument_byte_limit', { maxArgumentBytes: 3 });
  unknown('x'.repeat(4000), 'event_byte_limit', { maxEventBytes: 32 });
});
test('limits and API misuse are explicit; finalized or poisoned observer cannot recover', () => {
  assert.throws(() => observer({ maxEvents: 0 }), TypeError);
  assert.throws(() => observer({ maxJsonDepth: 10000 }), TypeError);
  assert.throws(() => observer({ unknownOption: 1 }), TypeError);
  assert.throws(() => observer({ expectedModel: '' }), /invalid_expected_model/);
  const subject = observer(); subject.push(encode('data: nope\n\n')); subject.push(encode(frames(textChunks())));
  assert.equal(subject.state.bytes, encode('data: nope\n\n' + frames(textChunks())).byteLength);
  assert.equal(subject.finish().invalidReason, 'malformed_json');
  assert.deepEqual(subject.finish(), subject.finish({ reason: 'cancel' }));
  assert.throws(() => subject.push(encode('')), /observer_finished/);
  assert.throws(() => observer().push('wrong'), TypeError);
});
