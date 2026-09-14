// Original Responses SSE only. No network, translation, persistence, or routing.
const TERMINALS = new Map([
  ['response.completed', 'completed'], ['response.failed', 'failed'],
  ['response.incomplete', 'incomplete'],
]);
const object = value => value !== null && typeof value === 'object' && !Array.isArray(value);
const identifier = value => typeof value === 'string' && value.length > 0 && value.length <= 512 && !/[\u0000-\u0020]/u.test(value);
const integer = value => Number.isSafeInteger(value) && value >= 0;
const ITEM_EVENTS = new Set(['response.output_item.added', 'response.output_item.done']);
const RESPONSE_EVENTS = new Set(['response.created', 'response.in_progress', 'response.queued', ...TERMINALS.keys()]);
const PART_EVENTS = new Set(['response.content_part.added', 'response.content_part.done',
  'response.reasoning_summary_part.added', 'response.reasoning_summary_part.done']);
const TEXT_EVENTS = new Map([
  ['response.output_text.delta', 'delta'], ['response.output_text.done', 'text'],
  ['response.refusal.delta', 'delta'], ['response.refusal.done', 'refusal'],
  ['response.function_call_arguments.delta', 'delta'], ['response.function_call_arguments.done', 'arguments'],
  ['response.custom_tool_call_input.delta', 'delta'], ['response.custom_tool_call_input.done', 'input'],
  ['response.reasoning_summary_text.delta', 'delta'], ['response.reasoning_summary_text.done', 'text'],
  ['response.reasoning_text.delta', 'delta'], ['response.reasoning_text.done', 'text'],
]);

export function parseUnambiguousJSON(text) {
  let parsed; try { parsed = JSON.parse(text); } catch { throw new Error('malformed_json'); }
  // JSON.parse silently accepts duplicate keys; they cannot establish authority.
  let at = 0;
  const space = () => { while (/\s/u.test(text[at] ?? '') && at < text.length) at++; };
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
    if (depth > 64) throw new Error('json_depth_limit');
    space();
    if (text[at] === '"') { string(); return; }
    if (text[at] === '{') {
      at++; space(); const keys = new Set();
      if (text[at] === '}') { at++; return; }
      while (at < text.length) {
        space(); const key = string();
        if (keys.has(key)) throw new Error('duplicate_json_key'); keys.add(key);
        space(); at++; value(depth + 1); space();
        if (text[at++] === '}') return;
      }
    } else if (text[at] === '[') {
      at++; space(); if (text[at] === ']') { at++; return; }
      while (at < text.length) {
        value(depth + 1); space(); if (text[at++] === ']') return;
      }
    } else { while (at < text.length && !/[\s,}\]]/u.test(text[at])) at++; }
  }
  value(0); return parsed;
}

export function createResponsesTerminalObserver(options = {}) {
  const limits = { maxBytes: 8 * 1024 * 1024, maxEventBytes: 256 * 1024,
    maxEvents: 100000, maxOutputItems: 256, ...options };
  for (const [name, value] of Object.entries(limits)) {
    if (!['maxBytes', 'maxEventBytes', 'maxEvents', 'maxOutputItems'].includes(name)
      || !Number.isSafeInteger(value) || value <= 0) throw new TypeError(`Invalid limit: ${name}`);
  }
  const decoder = new TextDecoder('utf-8', { fatal: true, ignoreBOM: true });
  let bytes = 0, events = 0, eventBytes = 0, responseId = null;
  let line = [], data = [], eventName = null, hasFields = false, skipLF = false;
  let invalidReason = null, localEnd = null, terminalCandidate = null;
  let numbered = null, lastSequence = -1;
  const items = new Map();
  const indexes = new Map();
  const doneItems = new Map();
  const closedChannels = new Set();
  const addedParts = new Set();
  const closedParts = new Set();

  function fail(reason) {
    invalidReason ??= reason;
    line = []; data = []; eventName = null; hasFields = false;
    terminalCandidate = null;
    items.clear(); indexes.clear(); doneItems.clear();
    closedChannels.clear(); addedParts.clear(); closedParts.clear();
  }
  function assert(condition, reason) { if (!condition) throw new Error(reason); }
  function bindIdentity(record, fields) {
    for (const key of ['call_id', 'name', 'namespace', 'role', 'phase']) {
      if (fields[key] === undefined) continue;
      assert(typeof fields[key] === 'string', 'invalid_item_identity');
      if (Object.hasOwn(record.identity, key)) {
        assert(record.identity[key] === fields[key], 'item_identity_mismatch');
      } else record.identity[key] = fields[key];
    }
  }
  function recordFor(item, index) {
    let record = items.get(item.id);
    if (!record) {
      assert(items.size < limits.maxOutputItems, 'output_item_limit');
      const identity = ['function_call', 'custom_tool_call'].includes(item.type) ? { namespace: item.namespace ?? '' } : {};
      record = { index, type: item.type, identity, channels: new Map(), parts: new Map() };
      items.set(item.id, record);
    }
    bindIdentity(record, ['function_call', 'custom_tool_call'].includes(item.type)
      ? { ...item, namespace: item.namespace ?? '' } : item);
    return record;
  }
  function channelKey(record, family, index, partType) {
    const key = JSON.stringify([family, index]);
    if (partType !== undefined) {
      if (record.parts.has(key)) assert(record.parts.get(key) === partType, 'content_part_type_mismatch');
      else record.parts.set(key, partType);
    }
    return key;
  }
  function snapshot(record, key, value, complete) {
    const channel = record.channels.get(key);
    if (!channel) record.channels.set(key, { text: value, streamed: false, complete });
    else {
      // Added snapshots may contain an initial prefix. Repeated full snapshots
      // corroborate it; they are never appended as another delta.
      assert(channel.streamed || channel.complete ? value === channel.text : value.startsWith(channel.text), 'streamed_content_mismatch');
      channel.text = value;
      channel.complete ||= complete;
    }
  }
  function appendDelta(record, key, value) {
    let channel = record.channels.get(key);
    if (!channel) { channel = { text: '', streamed: false, complete: false }; record.channels.set(key, channel); }
    assert(!channel.complete, 'delta_after_complete_snapshot');
    // All retained prefixes/deltas originated inside maxBytes; concatenation
    // cannot grow beyond that bound, even across many individually small events.
    channel.text += value; channel.streamed = true;
  }
  function itemContent(record, item, complete) {
    const present = new Set();
    const take = (family, index, type, value) => {
      const key = channelKey(record, family, index, type); present.add(key); snapshot(record, key, value, complete);
    };
    if (item.type === 'function_call') take('arguments', 0, undefined, item.arguments);
    else if (item.type === 'custom_tool_call') take('input', 0, undefined, item.input);
    else {
      if (item.type === 'message') item.content.forEach((part, index) => take('content', index, part.type, part.type === 'refusal' ? part.refusal : part.text));
      else {
        item.summary.forEach((part, index) => take('summary', index, 'summary_text', part.text));
        (item.content || []).forEach((part, index) => take('content', index, 'reasoning_text', part.text));
      }
    }
    if (complete) for (const key of record.channels.keys()) assert(present.has(key), 'missing_output_channel');
  }
  function itemSummary(item, complete) {
    assert(object(item) && identifier(item.id) && typeof item.type === 'string', 'invalid_output_item');
    assert(['message', 'function_call', 'custom_tool_call', 'reasoning'].includes(item.type), 'unsupported_output_item');
    if (complete) assert(item.status === 'completed' || (item.type === 'reasoning' && item.status === undefined), 'incomplete_output_item');
    else if (item.status !== undefined) assert(['in_progress', 'completed'].includes(item.status), 'invalid_item_status');
    const result = { id: item.id, type: item.type, status: item.status ?? null };
    if (item.type === 'message') {
      assert(item.role === 'assistant' && Array.isArray(item.content), 'invalid_message_item');
      result.content = item.content.map(part => {
        assert(object(part), 'invalid_message_content');
        if (part.type === 'output_text') {
          assert(typeof part.text === 'string', 'invalid_output_text');
          return { type: part.type, text: part.text };
        }
        assert(part.type === 'refusal' && typeof part.refusal === 'string', 'unsupported_message_content');
        return { type: part.type, refusal: part.refusal };
      });
      if (item.phase !== undefined) {
        assert(typeof item.phase === 'string', 'invalid_message_phase');
        result.phase = item.phase;
      }
    } else if (item.type === 'function_call' || item.type === 'custom_tool_call') {
      assert(identifier(item.call_id) && identifier(item.name), 'invalid_tool_item');
      const value = item.type === 'function_call' ? item.arguments : item.input;
      assert(typeof value === 'string', 'invalid_tool_arguments');
      // Full tool-schema validation and dispatch authority belong to the caller.
      if (complete && item.type === 'function_call') {
        let args; try { args = parseUnambiguousJSON(value); } catch (error) {
          if (error.message === 'malformed_json') throw new Error('invalid_tool_arguments');
          throw error;
        }
        assert(object(args), 'invalid_tool_arguments');
      }
      Object.assign(result, { callId: item.call_id, name: item.name, value });
      if (item.namespace !== undefined) {
        assert(typeof item.namespace === 'string', 'invalid_tool_namespace');
        result.namespace = item.namespace;
      }
    } else {
      assert(Array.isArray(item.summary), 'invalid_reasoning_item');
      result.summary = item.summary.map(part => {
        assert(object(part) && part.type === 'summary_text' && typeof part.text === 'string', 'invalid_reasoning_summary');
        return part.text;
      });
      if (item.content !== undefined && item.content !== null) {
        assert(Array.isArray(item.content), 'invalid_reasoning_content');
        result.content = item.content.map(part => {
          assert(object(part) && ['reasoning_text', 'text'].includes(part.type) && typeof part.text === 'string', 'invalid_reasoning_content');
          return { type: 'reasoning_text', text: part.text };
        });
      }
      if (item.encrypted_content !== undefined && item.encrypted_content !== null) {
        assert(typeof item.encrypted_content === 'string', 'invalid_reasoning_content');
        result.encryptedContent = item.encrypted_content;
      }
    }
    return JSON.stringify(result);
  }
  function validateEvent(payload, explicitType) {
    assert(object(payload) && typeof payload.type === 'string', 'invalid_event');
    const type = payload.type;
    assert(!explicitType || explicitType === type, 'event_type_mismatch');
    assert(/^response\.[a-z_]+(?:\.[a-z_]+)*$/u.test(type), 'unsupported_event_type');
    assert(type !== 'response.done', 'unsupported_terminal_alias');
    assert(!terminalCandidate, 'event_after_terminal');
    assert(RESPONSE_EVENTS.has(type) || ITEM_EVENTS.has(type) || PART_EVENTS.has(type) || TEXT_EVENTS.has(type), 'unsupported_event_type');
    const hasSequence = payload.sequence_number !== undefined;
    if (numbered === null) numbered = hasSequence;
    assert(numbered === hasSequence, 'inconsistent_sequence_numbers');
    if (hasSequence) {
      assert(integer(payload.sequence_number) && payload.sequence_number > lastSequence, 'reordered_or_duplicate_sequence');
      lastSequence = payload.sequence_number;
    }
    if (type === 'response.created') {
      assert(responseId === null && object(payload.response) && identifier(payload.response.id), 'invalid_or_duplicate_created');
      assert(payload.response.status === 'in_progress' || payload.response.status === 'queued', 'invalid_created_status');
      responseId = payload.response.id;
    } else assert(responseId !== null, 'event_before_created');
    if (payload.response_id !== undefined) assert(payload.response_id === responseId, 'response_id_mismatch');
    if (RESPONSE_EVENTS.has(type)) assert(object(payload.response), 'missing_response');
    if (payload.response !== undefined) {
      assert(object(payload.response) && payload.response.id === responseId, 'response_id_mismatch');
      if (!TERMINALS.has(type)) assert(['in_progress', 'queued'].includes(payload.response.status), 'nonterminal_status_mismatch');
    }
    if (PART_EVENTS.has(type) || TEXT_EVENTS.has(type)) {
      assert(identifier(payload.item_id) && integer(payload.output_index), 'invalid_item_event');
      if (TEXT_EVENTS.has(type)) assert(typeof payload[TEXT_EVENTS.get(type)] === 'string', 'invalid_text_event');
      if (type.startsWith('response.reasoning_summary_')) assert(integer(payload.summary_index), 'invalid_summary_index');
      else if (type.startsWith('response.content_part.') || type.startsWith('response.output_text.')
        || type.startsWith('response.refusal.') || type.startsWith('response.reasoning_text.')) {
        assert(integer(payload.content_index), 'invalid_content_index');
      }
      if (PART_EVENTS.has(type)) {
        assert(object(payload.part) && ['output_text', 'refusal', 'summary_text', 'reasoning_text'].includes(payload.part.type), 'invalid_content_part');
        const field = payload.part.type === 'refusal' ? 'refusal' : 'text';
        assert(typeof payload.part[field] === 'string', 'invalid_content_part');
      }
    }
    if (ITEM_EVENTS.has(type)) {
      assert(integer(payload.output_index) && payload.output_index < limits.maxOutputItems, 'invalid_output_index');
      const item = payload.item;
      const summary = itemSummary(item, type.endsWith('.done'));
      const previous = items.get(item.id);
      const indexId = indexes.get(payload.output_index);
      assert(indexId === undefined || indexId === item.id, 'conflicting_output_index');
      assert(!previous || (previous.index === payload.output_index && previous.type === item.type), 'conflicting_output_item');
      if (type.endsWith('.added')) assert(!previous, 'duplicate_or_reordered_item');
      else assert(!doneItems.has(item.id), 'duplicate_done_item');
      assert(items.has(item.id) || items.size < limits.maxOutputItems, 'output_item_limit');
      if (payload.item_id !== undefined) assert(payload.item_id === item.id, 'conflicting_output_item');
      const record = recordFor(item, payload.output_index);
      bindIdentity(record, payload);
      itemContent(record, item, type.endsWith('.done'));
      indexes.set(payload.output_index, item.id);
      if (type.endsWith('.done')) doneItems.set(item.id, summary);
    } else if (payload.item_id !== undefined) {
      assert(identifier(payload.item_id) && items.has(payload.item_id), 'unknown_item_id');
      assert(!doneItems.has(payload.item_id), 'event_after_item_done');
      if (payload.output_index !== undefined) assert(payload.output_index === items.get(payload.item_id).index, 'conflicting_output_index');
      const record = items.get(payload.item_id);
      bindIdentity(record, payload);
      const itemType = record.type;
      const expected = type.startsWith('response.function_call_arguments.') ? 'function_call'
        : type.startsWith('response.custom_tool_call_input.') ? 'custom_tool_call'
          : type.startsWith('response.reasoning_') ? 'reasoning' : 'message';
      assert(itemType === expected, 'item_event_type_mismatch');
      const family = type.startsWith('response.function_call_arguments.') ? 'arguments'
        : type.startsWith('response.custom_tool_call_input.') ? 'input'
          : type.startsWith('response.reasoning_summary_') ? 'summary' : 'content';
      assert(family === 'summary' ? payload.content_index === undefined
        : family === 'content' ? payload.summary_index === undefined
          : payload.content_index === undefined && payload.summary_index === undefined, 'conflicting_channel_index');
      const partIndex = family === 'summary' ? payload.summary_index : family === 'content' ? payload.content_index : 0;
      assert(partIndex < limits.maxEvents, 'part_index_limit');
      const partType = PART_EVENTS.has(type) ? payload.part.type
        : family === 'summary' ? 'summary_text' : family === 'content'
          ? type.startsWith('response.refusal.') ? 'refusal' : itemType === 'reasoning' ? 'reasoning_text' : 'output_text' : undefined;
      if (PART_EVENTS.has(type)) {
        assert(family === 'summary' ? partType === 'summary_text'
          : itemType === 'message' && ['output_text', 'refusal'].includes(partType), 'invalid_content_part');
      }
      const key = channelKey(record, family, partIndex, partType);
      const partKey = JSON.stringify([payload.item_id, family, partIndex]);
      if (PART_EVENTS.has(type)) {
        assert(!closedParts.has(partKey), 'event_after_part_done');
        if (type.endsWith('.added')) {
          assert(!addedParts.has(partKey), 'duplicate_part_added'); addedParts.add(partKey);
        } else closedParts.add(partKey);
        snapshot(record, key, payload.part.type === 'refusal' ? payload.part.refusal : payload.part.text, type.endsWith('.done'));
      } else if (TEXT_EVENTS.has(type)) {
        assert(!closedParts.has(partKey), 'event_after_part_done');
        const channel = `${partKey}:${type.slice(0, type.lastIndexOf('.'))}`;
        assert(!closedChannels.has(channel), 'event_after_channel_done');
        if (type.endsWith('.done')) closedChannels.add(channel);
        if (type.endsWith('.delta')) appendDelta(record, key, payload.delta);
        else snapshot(record, key, payload[TEXT_EVENTS.get(type)], true);
      }
    }
    if (!TERMINALS.has(type)) return;
    const kind = TERMINALS.get(type), response = payload.response;
    assert(object(response) && response.id === responseId && response.status === kind, 'terminal_status_mismatch');
    if (response.end_turn !== undefined) assert(typeof response.end_turn === 'boolean', 'invalid_end_turn');
    if (kind === 'completed') {
      assert(response.error == null && response.incomplete_details == null, 'contradictory_completed_response');
      assert(Array.isArray(response.output) && response.output.length <= limits.maxOutputItems, 'invalid_terminal_output');
      const seen = new Set(), calls = new Set();
      response.output.forEach((item, index) => {
        const summary = itemSummary(item, true);
        assert(!seen.has(item.id), 'duplicate_terminal_item'); seen.add(item.id);
        if (item.call_id !== undefined) {
          assert(!calls.has(item.call_id), 'duplicate_call_id'); calls.add(item.call_id);
        }
        // An explicit completed final item suffices; prior done, when present, must agree.
        if (items.has(item.id)) assert(items.get(item.id).index === index && items.get(item.id).type === item.type, 'terminal_item_mismatch');
        if (doneItems.has(item.id)) assert(doneItems.get(item.id) === summary, 'terminal_item_mismatch');
        if (item.type === 'reasoning' && item.status === undefined) assert(doneItems.has(item.id), 'reasoning_completion_unproved');
        const record = recordFor(item, index);
        itemContent(record, item, true);
      });
      for (const id of items.keys()) assert(seen.has(id), 'missing_terminal_item');
    } else if (kind === 'failed') {
      assert(object(response.error) && typeof response.error.message === 'string' && response.error.message.length > 0, 'invalid_failure');
      if (response.error.code !== undefined && response.error.code !== null) assert(typeof response.error.code === 'string', 'invalid_failure');
      assert(response.incomplete_details == null, 'contradictory_failed_response');
    } else {
      assert(object(response.incomplete_details) && typeof response.incomplete_details.reason === 'string'
        && response.incomplete_details.reason.length > 0, 'invalid_incomplete');
      assert(response.error == null, 'contradictory_incomplete_response');
    }
    terminalCandidate = Object.freeze({ kind, responseId, status: kind,
      ...(response.end_turn === undefined ? {} : { endTurn: response.end_turn }) });
  }
  function dispatch() {
    if (!hasFields && data.length === 0) return;
    assert(data.length > 0, 'missing_event_data');
    events++;
    assert(events <= limits.maxEvents, 'event_limit');
    const text = data.join('\n');
    // A sentinel supplies no native evidence, and cannot impersonate a named event.
    if (text === '[DONE]') { assert(eventName === null, 'named_done_sentinel'); return; }
    const payload = parseUnambiguousJSON(text);
    validateEvent(payload, eventName);
  }
  function consumeLine() {
    let text;
    try { text = decoder.decode(Uint8Array.from(line)); } catch { throw new Error('invalid_utf8'); }
    line = [];
    if (text === '') {
      dispatch(); data = []; eventName = null; hasFields = false; eventBytes = 0; return;
    }
    if (text.startsWith(':')) return;
    const colon = text.indexOf(':');
    const field = colon < 0 ? text : text.slice(0, colon);
    let value = colon < 0 ? '' : text.slice(colon + 1);
    if (value.startsWith(' ')) value = value.slice(1);
    hasFields = true;
    if (field === 'data') data.push(value);
    else if (field === 'event') {
      assert(eventName === null && value.length > 0, 'invalid_event_field'); eventName = value;
    } else if (field === 'id') assert(!value.includes('\0'), 'invalid_sse_id');
    else if (field === 'retry') assert(/^\d+$/u.test(value), 'invalid_sse_retry');
    else throw new Error('unsupported_sse_field');
  }
  function state() {
    return Object.freeze({ responseId, terminalCandidate, terminal: localEnd && !invalidReason ? terminalCandidate : null,
      disposition: localEnd && !invalidReason && terminalCandidate ? 'provider_terminal' : 'unknown',
      invalidReason, localEnd, bytes, events });
  }
  return Object.freeze({
    push(chunk) {
      if (!(chunk instanceof Uint8Array)) throw new TypeError('push expects Uint8Array');
      if (localEnd) throw new Error('observer_finished');
      if (invalidReason) return state();
      bytes += chunk.byteLength;
      if (bytes > limits.maxBytes) { fail('byte_limit'); return state(); }
      try {
        for (const byte of chunk) {
          if (skipLF) { skipLF = false; if (byte === 10) continue; }
          eventBytes++;
          assert(eventBytes <= limits.maxEventBytes, 'event_byte_limit');
          if (byte === 13 || byte === 10) { consumeLine(); skipLF = byte === 13; }
          else line.push(byte);
        }
      } catch (error) { fail(error.message); }
      return state();
    },
    finish({ reason = 'eof' } = {}) {
      if (!['eof', 'cancel', 'error'].includes(reason)) throw new TypeError('Invalid finish reason');
      if (localEnd) return state();
      localEnd = reason;
      if (!invalidReason && (line.length || data.length || hasFields)) fail('truncated_frame');
      return state();
    },
    get state() { return state(); },
  });
}
