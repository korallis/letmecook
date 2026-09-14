// Original Chat Completions SSE observer. No network, routing, or persistence.
const object = value => value !== null && typeof value === 'object' && !Array.isArray(value);
const own = (value, key) => Object.prototype.hasOwnProperty.call(value, key);
const identity = value => typeof value === 'string' && /^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,511}$/u.test(value);
const name = value => typeof value === 'string' && /^[A-Za-z_][A-Za-z0-9_.:-]{0,127}$/u.test(value);
const assert = (condition, reason) => { if (!condition) throw new Error(reason); };
function keys(value, allowed, required = []) {
  assert(object(value), 'invalid_object');
  assert(Object.keys(value).every(key => allowed.includes(key)) && required.every(key => own(value, key)), 'unsupported_fields');
}

function strictJSON(text, maxDepth) {
  let parsed; try { parsed = JSON.parse(text); } catch { throw new Error('malformed_json'); }
  // Detect duplicates before trusting a parsed value; JSON.parse keeps the last key.
  let at = 0;
  const space = () => { while (at < text.length && /\s/u.test(text[at])) at++; };
  function string() {
    const start = at++;
    while (at < text.length) {
      const ch = text[at++];
      if (ch === '\\') at++;
      else if (ch === '"') return JSON.parse(text.slice(start, at));
    }
    throw new Error('malformed_json');
  }
  function value(depth) {
    assert(depth <= maxDepth, 'json_depth_limit'); space();
    if (text[at] === '"') { string(); return; }
    if (text[at] === '{') {
      at++; space(); const seen = new Set();
      if (text[at] === '}') { at++; return; }
      while (at < text.length) {
        space(); const key = string(); assert(!seen.has(key), 'duplicate_json_key'); seen.add(key);
        space(); at++; value(depth + 1); space(); if (text[at++] === '}') return;
      }
    } else if (text[at] === '[') {
      at++; space(); if (text[at] === ']') { at++; return; }
      while (at < text.length) { value(depth + 1); space(); if (text[at++] === ']') return; }
    } else { while (at < text.length && !/[\s,}\]]/u.test(text[at])) at++; }
  }
  value(0); return parsed;
}

export function createChatTerminalObserver(options = {}) {
  const { expectedModel, ...overrides } = options;
  assert(expectedModel === undefined || identity(expectedModel), 'invalid_expected_model');
  const limits = { maxBytes: 8 * 1024 * 1024, maxEventBytes: 256 * 1024,
    maxEvents: 100000, maxToolCalls: 8, maxArgumentBytes: 256 * 1024, maxJsonDepth: 64, ...overrides };
  for (const [key, value] of Object.entries(limits)) {
    if (!['maxBytes', 'maxEventBytes', 'maxEvents', 'maxToolCalls', 'maxArgumentBytes', 'maxJsonDepth'].includes(key)
      || !Number.isSafeInteger(value) || value <= 0 || (key === 'maxJsonDepth' && value > 128)) {
      throw new TypeError(`Invalid limit: ${key}`);
    }
  }
  const decoder = new TextDecoder('utf-8', { fatal: true, ignoreBOM: true });
  const encoder = new TextEncoder();
  let bytes = 0, events = 0, eventBytes = 0, line = [], data = [], eventName = null;
  let fields = false, skipLF = false, invalidReason = null, localEnd = null;
  let responseId = null, model = null, finishReason = null, doneMarker = false;
  let terminalCandidate = null;
  const tools = new Map();
  const callIds = new Set();
  function fail(reason) {
    invalidReason ??= reason;
    line = []; data = []; eventName = null; fields = false;
    terminalCandidate = null; tools.clear(); callIds.clear();
  }
  function toolFragment(part, seenIndices) {
    keys(part, ['index', 'id', 'type', 'function'], ['index', 'function']);
    keys(part.function, ['name', 'arguments']);
    assert(Number.isSafeInteger(part.index) && part.index >= 0 && part.index < limits.maxToolCalls, 'invalid_tool_index');
    assert(!seenIndices.has(part.index), 'duplicate_tool_index'); seenIndices.add(part.index);
    assert(part.type === undefined || part.type === 'function', 'unsupported_tool_type');
    let tool = tools.get(part.index);
    if (!tool) {
      assert(part.index === tools.size, 'reordered_tool_declaration');
      assert(identity(part.id) && name(part.function.name) && part.type === 'function', 'invalid_tool_identity');
      assert(!callIds.has(part.id), 'duplicate_call_id'); callIds.add(part.id);
      tool = { id: part.id, name: part.function.name, arguments: '', argumentBytes: 0 };
      tools.set(part.index, tool);
    } else {
      // This strict profile declares ID/name once; later fragments carry arguments.
      assert(part.id === undefined && part.function.name === undefined, 'repeated_or_changed_tool_identity');
    }
    if (part.function.arguments !== undefined) {
      assert(typeof part.function.arguments === 'string', 'invalid_tool_arguments');
      tool.argumentBytes += encoder.encode(part.function.arguments).byteLength;
      assert(tool.argumentBytes <= limits.maxArgumentBytes, 'argument_byte_limit');
      tool.arguments += part.function.arguments;
    }
  }
  function validateTools() {
    assert(finishReason === 'tool_calls' ? tools.size > 0 : tools.size === 0, 'finish_tool_mismatch');
    for (const tool of tools.values()) {
      let args;
      try { args = strictJSON(tool.arguments, limits.maxJsonDepth); }
      catch (error) { throw new Error(error.message === 'malformed_json' ? 'incomplete_tool_arguments' : error.message); }
      assert(object(args), 'invalid_tool_argument_object');
    }
  }
  function payload(value) {
    assert(object(value), 'invalid_chunk');
    assert(!own(value, 'error') || value.error === null, 'explicit_upstream_error');
    assert(value.object === 'chat.completion.chunk', 'invalid_chunk_type');
    assert(identity(value.id) && identity(value.model), 'missing_or_invalid_identity');
    if (responseId === null) { responseId = value.id; model = value.model; }
    assert(value.id === responseId, 'response_id_changed');
    assert(value.model === model, 'response_model_changed');
    assert(expectedModel === undefined || value.model === expectedModel, 'unexpected_response_model');
    assert(Array.isArray(value.choices) && value.choices.length === 1, 'unsupported_choices');
    const choice = value.choices[0];
    keys(choice, ['index', 'delta', 'finish_reason', 'logprobs'], ['index', 'delta']);
    assert(choice.index === 0, 'unsupported_choice_index');
    assert(choice.logprobs === undefined || choice.logprobs === null, 'unsupported_logprobs');
    const delta = choice.delta;
    keys(delta, ['role', 'content', 'tool_calls']);
    assert(delta.role === undefined || delta.role === 'assistant', 'invalid_delta_role');
    assert(delta.content === undefined || delta.content === null || typeof delta.content === 'string', 'invalid_delta_content');
    if (delta.tool_calls !== undefined) {
      assert(Array.isArray(delta.tool_calls) && delta.tool_calls.length > 0 && delta.tool_calls.length <= limits.maxToolCalls, 'invalid_tool_fragments');
      const seen = new Set(); for (const part of delta.tool_calls) toolFragment(part, seen);
    }
    if (choice.finish_reason !== undefined && choice.finish_reason !== null) {
      assert(['stop', 'tool_calls'].includes(choice.finish_reason), 'unsupported_finish_reason');
      finishReason = choice.finish_reason;
      validateTools();
    }
  }
  function dispatch() {
    if (!fields && data.length === 0) return;
    assert(data.length > 0, 'missing_event_data');
    events++; assert(events <= limits.maxEvents, 'event_limit');
    assert(eventName === null || eventName === 'message', 'upstream_error_or_unsupported_event');
    assert(!doneMarker, 'event_after_done');
    const text = data.join('\n');
    if (text === '[DONE]') {
      assert(finishReason !== null, 'done_before_finish');
      validateTools(); doneMarker = true;
      terminalCandidate = Object.freeze({ kind: 'completed', responseId, model, finishReason, toolCallCount: tools.size });
      return;
    }
    assert(finishReason === null, 'event_after_finish');
    payload(strictJSON(text, limits.maxJsonDepth));
  }
  function consumeLine() {
    let text;
    try { text = decoder.decode(Uint8Array.from(line)); } catch { throw new Error('invalid_utf8'); }
    line = [];
    if (text === '') { dispatch(); data = []; eventName = null; fields = false; eventBytes = 0; return; }
    if (text.startsWith(':')) return;
    const colon = text.indexOf(':');
    const field = colon < 0 ? text : text.slice(0, colon);
    let value = colon < 0 ? '' : text.slice(colon + 1); if (value.startsWith(' ')) value = value.slice(1);
    fields = true;
    if (field === 'data') data.push(value);
    else if (field === 'event') {
      assert(eventName === null && value.length > 0, 'invalid_event_field'); eventName = value;
    } else throw new Error('unsupported_sse_field');
  }
  function state() {
    const terminal = localEnd === 'eof' && !invalidReason && doneMarker ? terminalCandidate : null;
    return Object.freeze({ responseId, model, terminalCandidate, terminal,
      disposition: terminal ? 'provider_terminal' : 'unknown', finishReason, doneMarker,
      invalidReason, localEnd, bytes, events });
  }
  return Object.freeze({
    push(chunk) {
      if (!(chunk instanceof Uint8Array)) throw new TypeError('push expects Uint8Array');
      if (localEnd) throw new Error('observer_finished');
      bytes = Math.min(Number.MAX_SAFE_INTEGER, bytes + chunk.byteLength);
      if (invalidReason) return state();
      if (bytes > limits.maxBytes) { fail('byte_limit'); return state(); }
      try {
        for (const byte of chunk) {
          if (skipLF) { skipLF = false; if (byte === 10) continue; }
          eventBytes++; assert(eventBytes <= limits.maxEventBytes, 'event_byte_limit');
          if (byte === 13 || byte === 10) { consumeLine(); skipLF = byte === 13; }
          else line.push(byte);
        }
      } catch (error) { fail(error.message); }
      return state();
    },
    finish({ reason = 'eof' } = {}) {
      if (!['eof', 'cancel', 'error'].includes(reason)) throw new TypeError('Invalid finish reason');
      if (localEnd) return state(); localEnd = reason;
      if (!invalidReason && (line.length || data.length || fields)) fail('truncated_frame');
      return state();
    },
    get state() { return state(); },
  });
}
