import { canonical, parseJSON } from '../inference-boundary/json.ts';
import { ProbeError } from './reader.ts';
import { DEFAULT_BUDGET } from './planner.ts';
import { REPAIR_INSTRUCTION, type Completion, type PlannerBudget, type PlannerConversation, type PlannerPolicy, type PlannerSettings, type PlannerTransport, type ToolCall } from './transport.ts';

export const NATIVE_PLANNER_PROTOCOL = 'planner-probe-responses-read-file-v1' as const;
const immutable = <T>(value: T): T => {
  if (value && typeof value === 'object') { for (const child of Object.values(value)) immutable(child); Object.freeze(value); }
  return value;
};
export const NATIVE_PLANNER_TOOL = immutable({
  type: 'function', name: 'read_file', description: 'Read one authorized fixture path', strict: false,
  parameters: { type: 'object', properties: { path: { type: 'string', enum: ['fixture.txt'] } }, required: ['path'], additionalProperties: false },
} as const);
// Explicit consumer timing, unchanged from the old fixture. This is not the
// aggregate evaluation clock, a provider cap, or permission to start a live scope.
export const NATIVE_PLANNER_BUDGET = Object.freeze({ ...DEFAULT_BUDGET, outputTokens: null });
export type NativePhase = 'assessment' | 'final' | 'repair';
export interface NativePlannerPolicy extends PlannerPolicy {
  schema: 3; profile: 'router-native-responses-local-v1'; evidence: 'synthetic' | 'reviewed-deployment'; liveAdmission: boolean;
  limits: { requestBytes: number; responseBytes: number; outputTokens: null };
  native: {
    protocol: string; tools: unknown[]; toolPaths: string[];
    authorization: { model: string; effort: string; providerOutput: { requirement: string; capability: string }; providerMonetaryCap: { requirement: string; capability: string } };
    [key: string]: unknown;
  };
}
export interface ReleasedNativeCompletion {
  requestId: string; acceptedAt: number;
  response: { model: string; status: string; error: unknown; incomplete_details: unknown; output: unknown[] };
}
// Integration seam, not a new receipt gate. No production implementation exists
// here yet. The shared boundary adapter must validate the FULL schema-3 policy,
// binding/native role and scope, parse the bounded original stream, join its exact
// output/request to the original receipt, persist success and recheck release.
// It must reject uncertainty and never resolve with provisional output. It owns
// the scoped socket/grant, not provider credentials or another routing mechanism.
export interface NativePlannerBoundaryPort {
  readonly policy: NativePlannerPolicy;
  readonly binding: { role: 'planner'; protocol: typeof NATIVE_PLANNER_PROTOCOL; sessionId: string };
  complete(request: unknown, signal: AbortSignal, responseBytes: number): Promise<ReleasedNativeCompletion>;
}
const deny = (allowed: unknown, code = 'native_consumer_denied'): void => { if (!allowed) throw new ProbeError(code); };
const exact = (value: any, required: string[], optional: string[] = []) => deny(value && typeof value === 'object' && !Array.isArray(value) && required.every(key => Object.hasOwn(value, key)) && Object.keys(value).every(key => [...required, ...optional].includes(key)), 'invalid_native_output');
const id = (value: unknown) => typeof value === 'string' && /^[a-zA-Z0-9_-]{1,128}$/.test(value);
const text = (value: unknown) => typeof value === 'string' && Buffer.byteLength(value) <= 32768;
const safeFailures = new Set([
  'route_unavailable', 'invalid_stream', 'partial_failure', 'receipt_unverified', 'receipt_mismatched',
  'persistence_failed', 'lease_expired', 'scope_expired', 'unknown_original_work', 'policy_denied',
  'native_session_ended', 'native_history_denied', 'native_profile_drift', 'native_consumer_denied',
  'request_budget_exhausted', 'response_budget_exhausted', 'invalid_request_identity', 'invalid_native_output',
  'discovery_denied', 'cancelled', 'cancelled_unknown', 'native_boundary_denied',
]);
function awaitRelease(port: NativePlannerBoundaryPort, request: unknown, signal: AbortSignal, responseBytes: number): Promise<ReleasedNativeCompletion> {
  return new Promise((resolve, reject) => {
    const stop = () => reject(new ProbeError('cancelled_unknown'));
    signal.addEventListener('abort', stop, { once: true });
    if (signal.aborted) { signal.removeEventListener('abort', stop); stop(); return; }
    // Ending the consumer wait does not classify original work or replenish its
    // scope. The shared boundary retains uncertainty even if this port is late.
    Promise.resolve().then(() => {
      deny(!signal.aborted, 'cancelled_unknown');
      return port.complete(request, signal, responseBytes);
    }).then(resolve, reject)
      .finally(() => signal.removeEventListener('abort', stop));
  });
}

// Planner-owned defensive projection of a RELEASED completed output. Raw output
// IDs are optional in this port contract: stateless replay omits item IDs/status,
// while preserving ordered encrypted reasoning and exact native call_id/arguments.
// Shared admission/output validation must implement this same versioned subset.
function projectOutput(output: unknown[], toolsAllowed: boolean): { text: string; calls: ToolCall[]; continuation: any[] } {
  deny(Array.isArray(output) && output.length <= 16, 'invalid_native_output');
  const calls: ToolCall[] = [], continuation: any[] = [], ids = new Set<string>(); let content = '';
  for (const candidate of output) {
    const item = candidate as any;
    deny(item && typeof item === 'object' && !Array.isArray(item), 'invalid_native_output');
    if (Object.hasOwn(item, 'id')) { deny(id(item.id) && !ids.has(item.id), 'invalid_native_output'); ids.add(item.id); }
    if (item.type === 'reasoning') {
      exact(item, ['type', 'summary', 'encrypted_content'], ['id', 'status']);
      deny((item.status === undefined || item.status === 'completed') && text(item.encrypted_content) && item.encrypted_content.length > 0 && Array.isArray(item.summary) && item.summary.length <= 16, 'invalid_native_output');
      for (const part of item.summary) { exact(part, ['type', 'text']); deny(part.type === 'summary_text' && text(part.text), 'invalid_native_output'); }
      continuation.push({ type: 'reasoning', encrypted_content: item.encrypted_content, summary: structuredClone(item.summary) });
    } else if (item.type === 'function_call') {
      exact(item, ['type', 'call_id', 'name', 'arguments'], ['id', 'status']);
      deny(toolsAllowed && calls.length === 0 && item.name === 'read_file' && id(item.call_id) && (item.status === undefined || item.status === 'completed'), 'discovery_denied');
      deny(text(item.arguments), 'discovery_denied');
      let args: unknown; try { args = parseJSON(item.arguments); } catch { throw new ProbeError('discovery_denied'); }
      deny(canonical(args) === canonical({ path: 'fixture.txt' }), 'discovery_denied');
      calls.push({ id: item.call_id, type: 'function', function: { name: 'read_file', arguments: item.arguments } });
      continuation.push({ type: 'function_call', call_id: item.call_id, name: item.name, arguments: item.arguments });
    } else {
      exact(item, ['type', 'role', 'content'], ['id', 'status']);
      deny(item.type === 'message' && item.role === 'assistant' && (item.status === undefined || item.status === 'completed') && Array.isArray(item.content) && item.content.length <= 16, 'invalid_native_output');
      const parts = item.content.map((part: any) => {
        exact(part, ['type', 'text'], ['annotations', 'logprobs']);
        deny(part.type === 'output_text' && text(part.text) && (part.annotations === undefined || Array.isArray(part.annotations) && part.annotations.length === 0) && (part.logprobs === undefined || Array.isArray(part.logprobs) && part.logprobs.length === 0), 'invalid_native_output');
        content += part.text; return { type: 'output_text', text: part.text };
      });
      continuation.push({ role: 'assistant', content: parts });
    }
  }
  deny(!(calls.length && content), 'invalid_native_output');
  return { text: content, calls, continuation };
}

class NativeConversation implements PlannerConversation {
  private readonly model: string; private readonly sessionId: string;
  private input: any[]; private phase: NativePhase = 'assessment';
  private ready = true; private expected?: unknown; private toolsAllowed = false;
  private released?: { completion: Completion; continuation: any[] };
  private pendingRead?: ToolCall;
  constructor(model: string, sessionId: string, messages: { role: string; content: string }[]) {
    deny(messages.length === 2 && messages[0].role === 'system' && messages[1].role === 'user');
    this.model = model; this.sessionId = sessionId;
    this.input = [{ role: 'developer', content: messages[0].content }, { role: 'user', content: messages[1].content }];
  }
  request(toolsAllowed: boolean): unknown {
    deny(this.ready && !this.expected && (this.phase === 'assessment' || !toolsAllowed), 'native_history_denied');
    this.toolsAllowed = toolsAllowed; this.ready = false;
    this.expected = { model: this.model, input: structuredClone(this.input), stream: true, store: false,
      reasoning: { effort: 'xhigh', summary: 'auto' }, include: ['reasoning.encrypted_content'],
      tools: [structuredClone(NATIVE_PLANNER_TOOL)], tool_choice: toolsAllowed ? 'auto' : 'none', prompt_cache_key: this.sessionId };
    return structuredClone(this.expected);
  }
  assertRequest(value: unknown, toolsAllowed: boolean) {
    deny(this.expected && this.toolsAllowed === toolsAllowed && canonical(value) === canonical(this.expected), 'native_history_denied');
  }
  accept(completion: Completion, continuation: any[]) { this.expected = undefined; this.released = { completion, continuation }; }
  calls(completion: Completion) {
    deny(this.released?.completion === completion && this.phase === 'assessment' && completion.calls.length === 1 && this.toolsAllowed && !completion.text, 'native_history_denied');
    this.input.push(...structuredClone(this.released!.continuation)); this.released = undefined;
    this.pendingRead = structuredClone(completion.calls[0]);
  }
  read(call: ToolCall, output: string) {
    deny(this.pendingRead && canonical(this.pendingRead) === canonical(call) && text(output), 'native_history_denied');
    this.input.push({ type: 'function_call_output', call_id: call.id, output });
    this.pendingRead = undefined; this.phase = 'final'; this.ready = true;
  }
  repair(completion: Completion) {
    deny(this.released?.completion === completion && this.phase !== 'repair' && !completion.calls.length, 'native_history_denied');
    // Includes blank terminal output and opaque reasoning; never reuse Chat's
    // omission or generate a substitute call ID. The repair instruction is fixed.
    this.input.push(...structuredClone(this.released!.continuation), { role: 'user', content: REPAIR_INSTRUCTION });
    this.released = undefined; this.phase = 'repair'; this.ready = true;
  }
}

export class NativePlannerTransport implements PlannerTransport {
  readonly kind = 'native-responses' as const;
  private readonly port: NativePlannerBoundaryPort; private readonly approved: NativePlannerPolicy;
  private readonly binding: NativePlannerBoundaryPort['binding']; private active?: NativeConversation;
  private sending = false; private terminalFailure = false; private readonly requestIds = new Set<string>();
  constructor(port: NativePlannerBoundaryPort) {
    const policy = structuredClone(port.policy), binding = structuredClone(port.binding), native = policy.native;
    // These checks select the consumer contract only. They do NOT replace the
    // shared full-policy/profile validation or confer live authority on a port.
    deny(policy.schema === 3 && policy.profile === 'router-native-responses-local-v1' && native?.protocol === NATIVE_PLANNER_PROTOCOL && binding.role === 'planner' && binding.protocol === NATIVE_PLANNER_PROTOCOL, 'native_profile_denied');
    deny(/^ses_planner_[a-zA-Z0-9]{1,64}$/.test(binding.sessionId) && /^[a-z][a-z0-9_-]{0,63}$/.test(policy.routerModel), 'native_profile_denied');
    deny(['synthetic', 'reviewed-deployment'].includes(policy.evidence) && policy.liveAdmission === (policy.evidence === 'reviewed-deployment'), 'native_profile_denied');
    deny(canonical(native.tools) === canonical([NATIVE_PLANNER_TOOL]) && canonical(native.toolPaths) === canonical(['fixture.txt']), 'native_profile_denied');
    deny(native.authorization?.model === 'gpt-6-astra' && native.authorization.effort === 'xhigh', 'native_profile_denied');
    for (const cap of [native.authorization.providerOutput, native.authorization.providerMonetaryCap]) deny(canonical(cap) === canonical({ requirement: 'not_required', capability: 'unavailable' }), 'unsupported_provider_output_cap');
    deny(policy.limits.outputTokens === null && [policy.limits.requestBytes, policy.limits.responseBytes].every(n => Number.isSafeInteger(n) && n > 0), 'native_profile_denied');
    this.port = port; this.approved = policy; this.binding = binding;
  }
  get policy(): NativePlannerPolicy { return structuredClone(this.approved); }
  modelPolicy(): object {
    return { schema: 3, profile: this.approved.profile, protocol: NATIVE_PLANNER_PROTOCOL, evidence: this.approved.evidence,
      liveAdmission: this.approved.liveAdmission, authority: 'proposal_only', providerOutputTokens: null, providerMonetaryCap: null };
  }
  settings(budget: PlannerBudget): PlannerSettings {
    deny(budget.outputTokens === null, 'unsupported_provider_output_cap');
    return { profile: 'router-native-responses-local-v1', protocol: NATIVE_PLANNER_PROTOCOL, model: 'gpt-6-astra',
      reasoning: { effort: 'xhigh', summary: 'auto' }, store: false, stream: true, providerOutputTokens: null, providerMonetaryCap: null };
  }
  conversation(messages: { role: string; content: string }[], budget: PlannerBudget): PlannerConversation {
    deny(!this.active, 'native_session_already_started'); this.settings(budget);
    this.active = new NativeConversation(this.approved.routerModel, this.binding.sessionId, messages); return this.active;
  }
  async complete(value: unknown, signal: AbortSignal, responseBytes: number, toolsAllowed: boolean): Promise<Completion> {
    try {
      deny(!this.sending && !this.terminalFailure && this.active, 'native_session_ended');
      deny(Number.isSafeInteger(responseBytes) && responseBytes > 0 && responseBytes <= 32768, 'response_budget_exhausted');
      this.active!.assertRequest(value, toolsAllowed);
      deny(canonical(this.port.policy) === canonical(this.approved) && canonical(this.port.binding) === canonical(this.binding), 'native_profile_drift');
      deny(Buffer.byteLength(JSON.stringify(value)) <= this.approved.limits.requestBytes, 'request_budget_exhausted');
      deny(!signal.aborted, 'cancelled'); this.sending = true;
      const released = await awaitRelease(this.port, structuredClone(value), signal, Math.min(responseBytes, this.approved.limits.responseBytes));
      deny(!signal.aborted, 'cancelled_unknown');
      deny(canonical(this.port.policy) === canonical(this.approved) && canonical(this.port.binding) === canonical(this.binding), 'native_profile_drift');
      deny(released && /^[a-f0-9]{32}$/.test(released.requestId) && !this.requestIds.has(released.requestId) && Number.isSafeInteger(released.acceptedAt) && released.acceptedAt > 0 && released.acceptedAt <= Date.now(), 'invalid_request_identity');
      const response = released.response;
      deny(response?.model === 'gpt-6-astra' && response.status === 'completed' && response.error === null && response.incomplete_details === null, 'invalid_native_output');
      deny(Buffer.byteLength(JSON.stringify(response)) <= Math.min(responseBytes, this.approved.limits.responseBytes), 'response_budget_exhausted');
      const output = projectOutput(response.output, toolsAllowed);
      const completion = { text: output.text, calls: output.calls, requestId: released.requestId, acceptedAt: released.acceptedAt };
      this.requestIds.add(released.requestId); this.active!.accept(completion, output.continuation); return completion;
    } catch (error) {
      this.terminalFailure = true;
      throw error instanceof ProbeError && safeFailures.has(error.code) ? error : new ProbeError('native_boundary_denied');
    } finally { this.sending = false; }
  }
}
