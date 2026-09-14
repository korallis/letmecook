import { canonical, keys, object, parseJSON } from './json.ts';
import { OPENCODE_PROFILE, OPENCODE_ROUTER_PROFILE, openCodeTools, validateArguments, type ProtocolProfile } from './profiles.ts';
import { Denial, TOOL, type Policy } from './types.ts';

const callId = (id: unknown): id is string => typeof id === 'string' && /^call_[a-zA-Z0-9_-]{1,64}$/.test(id);
export function validateRequest(value: unknown, policy: Policy): string {
  try {
    const native = policy.profile === OPENCODE_PROFILE;
    const opencodeRouter = policy.schema === 2 && policy.profile === OPENCODE_ROUTER_PROFILE;
    const edits = native || opencodeRouter;
    const cap = edits ? 'max_tokens' : 'max_completion_tokens';
    if (policy.schema === 2 && !opencodeRouter) {
      object(value);
      if (Object.hasOwn(value, 'tool_choice')) throw new Denial('unsupported_request', 400);
    }
    keys(value, ['model', 'messages', 'stream', cap, 'tools', 'tool_choice', ...(native ? ['stream_options'] : [])], ['model', 'messages', 'stream', cap, ...(native ? ['stream_options'] : [])]);
    if (native && canonical((value as any).stream_options) !== canonical({ include_usage: true })) throw new Error();
    object(value);
    if (value.model !== policy.routerModel) throw new Denial('policy_denied');
    if (value.stream !== true || !Number.isInteger(value[cap]) || value[cap] < 1 || value[cap] > policy.limits.outputTokens) throw new Denial('request_limit', 422);
    if (!Array.isArray(value.messages) || !value.messages.length || value.messages.length > 64) throw new Error();
    let systemSeen = false;
    const pending = new Set<string>();
    const seen = new Set<string>();
    for (const message of value.messages) {
      object(message);
      if (policy.schema === 2 && message.role === 'assistant' && (pending.size || message.content !== null && (typeof message.content !== 'string' || !message.content.trim() && !(opencodeRouter && message.content === '' && message.tool_calls)) || message.content === null && !message.tool_calls)) throw new Error();
      if (policy.schema === 2 && ['system', 'user'].includes(message.role)) {
        if (typeof message.content !== 'string' || !message.content.trim() && !(opencodeRouter && message.content === '' && message.tool_calls)) throw new Error();
        if (message.role === 'system') { if (systemSeen || message !== value.messages[0] || value.messages.length < 2) throw new Error(); systemSeen = true; }
      }
      if (message.role === 'assistant') {
        keys(message, ['role', 'content', 'tool_calls'], ['role', 'content']);
        if (message.content !== null && typeof message.content !== 'string') throw new Error();
        if (message.tool_calls !== undefined) {
          if (!Array.isArray(message.tool_calls) || !message.tool_calls.length || message.tool_calls.length > 8) throw new Error();
          for (const call of message.tool_calls) {
            keys(call, ['id', 'type', 'function'], ['id', 'type', 'function']);
            keys(call.function, ['name', 'arguments'], ['name', 'arguments']);
            if (!callId(call.id) || policy.schema === 2 && call.id.length > 64 || seen.has(call.id) || call.type !== 'function') throw new Error();
            validateArguments(call.function.name, call.function.arguments, policy.profile); pending.add(call.id); seen.add(call.id);
          }
        }
      } else if (message.role === 'tool') {
        keys(message, ['role', 'content', 'tool_call_id'], ['role', 'content', 'tool_call_id']);
        if (typeof message.content !== 'string' || !pending.delete(message.tool_call_id)) throw new Error();
      } else {
        keys(message, ['role', 'content'], ['role', 'content']);
        if (!['system', 'user'].includes(message.role) || typeof message.content !== 'string' || pending.size) throw new Error();
      }
    }
    if (pending.size) throw new Error();
    if (value.tools !== undefined && canonical(value.tools) !== canonical(edits ? openCodeTools() : [TOOL])) throw new Error();
    if (opencodeRouter && (value.tool_choice !== 'auto' || !value.tools)) throw new Error();
    if (value.tool_choice !== undefined && (!value.tools || !['auto', 'required', 'none'].includes(value.tool_choice))) throw new Error();
    if (policy.schema === 2 && !opencodeRouter) { const { max_completion_tokens, ...rest } = value; return JSON.stringify({ ...rest, max_tokens: max_completion_tokens }); }
    return JSON.stringify(value);
  } catch (error) {
    if (error instanceof Denial) throw error;
    throw new Denial('unsupported_request', 400);
  }
}

// Only the separately versioned tested Chat profiles are implemented. Metadata is reconstructed.
export class ChatStream {
  private decoder = new TextDecoder('utf-8', { fatal: true });
  private buffer = '';
  private finish: 'stop' | 'tool_calls' | undefined;
  private tools = new Map<number, { id: string; name: string; arguments: string }>();
  private terminal = false;
  private duplicateDone = false;
  private pendingOutput: string[] = [];
  private completionOutput: string[] = [];
  semanticOutput = false;
  private readonly requestId: string;
  private readonly model: string;
  private readonly toolsAllowed: boolean;
  private readonly profile: ProtocolProfile;
  private readonly terminalProfile: 'done' | 'router-done' | 'receipt-gated-eof';
  private usage: { prompt_tokens: number; completion_tokens: number; total_tokens: number } | undefined;
  constructor(requestId: string, model: string, toolsAllowed: boolean, mode: ProtocolProfile | 'done' | 'router-done' | 'receipt-gated-eof' = 'done', profile: ProtocolProfile = 'chat-text-tools-v1') {
    this.terminalProfile = mode === 'router-done' || mode === 'receipt-gated-eof' ? mode : 'done';
    this.profile = mode === OPENCODE_PROFILE || mode === OPENCODE_ROUTER_PROFILE ? mode : profile;
    this.requestId = requestId; this.model = model; this.toolsAllowed = toolsAllowed;
  }
  private chunk(delta: unknown, finish_reason: string | null = null) {
    return `data: ${JSON.stringify({ id: this.requestId, object: 'chat.completion.chunk', model: this.model, choices: [{ index: 0, delta, finish_reason }] })}\n\n`;
  }
  push(bytes: Uint8Array): { output: string[]; semantic: boolean; error?: Denial } {
    this.pendingOutput = [];
    try { return this.consume(bytes); }
    catch (error) { return { output: this.pendingOutput, semantic: this.semanticOutput, error: error instanceof Denial ? error : new Denial('invalid_stream', 502) }; }
  }
  private consume(bytes: Uint8Array): { output: string[]; semantic: boolean } {
    try { this.buffer += this.decoder.decode(bytes, { stream: true }); }
    catch { throw new Denial('invalid_stream', 502); }
    const output = this.pendingOutput; let semantic = false;
    let end: number;
    // Normalize CRLF only after complete lines; a split CRLF remains buffered correctly.
    this.buffer = this.buffer.replace(/\r\n/g, '\n');
    while ((end = this.buffer.indexOf('\n\n')) >= 0) {
      const block = this.buffer.slice(0, end); this.buffer = this.buffer.slice(end + 2);
      const data: string[] = []; let event = '';
      for (const line of block.split('\n')) {
        if (!line || line.startsWith(':')) continue;
        const colon = line.indexOf(':');
        const field = colon < 0 ? line : line.slice(0, colon);
        const value = colon < 0 ? '' : line.slice(colon + 1).replace(/^ /, '');
        if (field === 'data') data.push(value);
        else if (field === 'event') event = value;
        else throw new Denial('invalid_stream', 502);
      }
      if (!data.length) continue;
      if (event && event !== 'message') throw new Denial('upstream_failure', 502);
      const text = data.join('\n');
      if (this.terminal) {
        if (this.terminalProfile === 'router-done' && text === '[DONE]' && !this.duplicateDone) { this.duplicateDone = true; continue; }
        throw new Denial('invalid_stream', 502);
      }
      if (text === '[DONE]') {
        if (!this.finish) throw new Denial('invalid_stream', 502);
        if (this.finish === 'tool_calls') {
          if (!this.tools.size) throw new Denial('invalid_stream', 502);
          if (new Set([...this.tools.values()].map(call => call.id)).size !== this.tools.size) throw new Denial('invalid_stream', 502);
          const calls = [...this.tools].sort(([a], [b]) => a - b).map(([index, call], position) => {
            if (index !== position || !callId(call.id)) throw new Denial('invalid_stream', 502);
            try { validateArguments(call.name, call.arguments, this.profile); } catch { throw new Denial('invalid_stream', 502); }
            // Local IDs avoid exposing unchecked upstream identifiers and retain continuation support.
            return { index, id: `call_${this.requestId}_${index}`, type: 'function', function: { name: call.name, arguments: call.arguments } };
          });
          this.completionOutput.push(this.chunk({ tool_calls: calls }));
        } else if (this.tools.size) throw new Denial('invalid_stream', 502);
        this.completionOutput.push(this.chunk({}, this.finish));
        if (this.usage) this.completionOutput.push(`data: ${JSON.stringify({ id: this.requestId, object: 'chat.completion.chunk', model: this.model, choices: [], usage: this.usage })}\n\n`);
        this.completionOutput.push('data: [DONE]\n\n'); this.terminal = true; continue;
      }
      let value: any;
      try { value = parseJSON(text); object(value); } catch { throw new Denial('invalid_stream', 502); }
      if (value.error) throw new Denial('upstream_failure', 502);
      if (this.profile === OPENCODE_PROFILE && this.finish && Array.isArray(value.choices) && value.choices.length === 0) {
        if (this.usage) throw new Denial('invalid_stream', 502);
        try {
          keys(value.usage, ['prompt_tokens', 'completion_tokens', 'total_tokens'], ['prompt_tokens', 'completion_tokens', 'total_tokens']);
          for (const n of Object.values(value.usage)) if (!Number.isSafeInteger(n) || (n as number) < 0 || (n as number) > 10000000) throw new Error();
          if (value.usage.total_tokens !== value.usage.prompt_tokens + value.usage.completion_tokens) throw new Error();
          this.usage = { prompt_tokens: value.usage.prompt_tokens, completion_tokens: value.usage.completion_tokens, total_tokens: value.usage.total_tokens };
        } catch { throw new Denial('invalid_stream', 502); }
        continue;
      }
      if (this.finish) throw new Denial('invalid_stream', 502);
      if (!Array.isArray(value.choices) || value.choices.length !== 1) throw new Denial('invalid_stream', 502);
      const choice = value.choices[0];
      if (choice.index !== 0 || !choice.delta || typeof choice.delta !== 'object' || Array.isArray(choice.delta)) throw new Denial('invalid_stream', 502);
      const delta = choice.delta;
      // Unknown semantic extensions fail closed; unrelated top-level metadata is discarded.
      try { keys(delta, ['role', 'content', 'tool_calls']); } catch { throw new Denial('invalid_stream', 502); }
      if (delta.role !== undefined && delta.role !== 'assistant') throw new Denial('invalid_stream', 502);
      if (delta.content !== undefined && delta.content !== null) {
        if (typeof delta.content !== 'string') throw new Denial('invalid_stream', 502);
        if (delta.content) { output.push(this.chunk({ content: delta.content })); semantic = true; this.semanticOutput = true; }
      }
      if (delta.tool_calls !== undefined) {
        if (!this.toolsAllowed || !Array.isArray(delta.tool_calls) || delta.tool_calls.length > 8) throw new Denial('invalid_stream', 502);
        for (const part of delta.tool_calls) {
          try { keys(part, ['index', 'id', 'type', 'function'], ['index', 'function']); keys(part.function, ['name', 'arguments']); } catch { throw new Denial('invalid_stream', 502); }
          if (!Number.isInteger(part.index) || part.index < 0 || part.index > 7 || (part.type !== undefined && part.type !== 'function')) throw new Denial('invalid_stream', 502);
          let call = this.tools.get(part.index);
          if (!call) {
            if (!callId(part.id) || typeof part.function.name !== 'string') throw new Denial('invalid_stream', 502);
            call = { id: part.id, name: part.function.name, arguments: '' }; this.tools.set(part.index, call);
          } else if (part.id !== undefined || part.function.name !== undefined) throw new Denial('invalid_stream', 502);
          if (part.function.arguments !== undefined) {
            if (typeof part.function.arguments !== 'string') throw new Denial('invalid_stream', 502);
            call.arguments += part.function.arguments; semantic ||= part.function.arguments.length > 0; this.semanticOutput ||= semantic;
          }
        }
      }
      if (choice.finish_reason !== null && choice.finish_reason !== undefined) {
        if (!['stop', 'tool_calls'].includes(choice.finish_reason)) throw new Denial('invalid_stream', 502);
        this.finish = choice.finish_reason;
      }
    }
    this.semanticOutput ||= semantic;
    return { output, semantic };
  }
  end() {
    try { this.buffer += this.decoder.decode(); } catch { throw new Denial('invalid_stream', 502); }
    // The pinned native Responses→Chat translator has no [DONE]. This profile
    // only prepares canonical output; the caller must still require a receipt.
    if (this.terminalProfile === 'receipt-gated-eof' && !this.terminal && this.finish && !this.buffer.trim()) this.consume(new TextEncoder().encode('data: [DONE]\n\n'));
    if (!this.terminal || this.buffer.trim()) throw new Denial('invalid_stream', 502);
    return this.completionOutput;
  }
}
