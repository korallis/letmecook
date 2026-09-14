import { request } from 'node:http';
import { ChatStream, validateRequest } from '../inference-boundary/protocol.ts';
import { CHAT_PATH, TOOL, type Policy, validatePolicy } from '../inference-boundary/types.ts';
import { parseJSON } from '../inference-boundary/json.ts';
import { ProbeError } from './reader.ts';

export interface ToolCall { id: string; type: 'function'; function: { name: string; arguments: string } }
export interface Completion { text: string; calls: ToolCall[]; requestId: string; acceptedAt: number }
// This interface exposes inference only. Neither a conversation nor a planner
// receives router accounts, publication credentials, receipt storage or control APIs.
export interface PlannerPolicy { schema: 1 | 2 | 3; profile: string; routeId: string; routerModel: string }
export interface PlannerBudget {
  assessments: number; repairs: number; requests: number; files: number; readBytes: number;
  requestBytes: number; responseBytes: number; outputTokens: number | null; totalMs: number;
}
export type PlannerSettings =
  | { profile: Policy['profile']; max_completion_tokens: number; stream: true }
  | { profile: 'router-native-responses-local-v1'; protocol: 'planner-probe-responses-read-file-v1'; model: 'gpt-6-astra'; reasoning: { effort: 'xhigh'; summary: 'auto' }; store: false; stream: true; providerOutputTokens: null; providerMonetaryCap: null };
export interface PlannerConversation {
  request(toolsAllowed: boolean): unknown;
  calls(completion: Completion): void;
  read(call: ToolCall, output: string): void;
  repair(completion: Completion): void;
}
export interface PlannerTransport {
  readonly kind: 'chat' | 'native-responses';
  readonly policy: PlannerPolicy;
  modelPolicy(): object;
  settings(budget: PlannerBudget): PlannerSettings;
  conversation(messages: { role: string; content: string }[], budget: PlannerBudget): PlannerConversation;
  complete(value: unknown, signal: AbortSignal, responseBytes: number, toolsAllowed: boolean): Promise<Completion>;
}
export const REPAIR_INSTRUCTION = 'The completed proposal failed deterministic schema or reference validation. One repair is permitted for this unchanged input revision. Return only valid JSON for the original schema and evidence. No tools or new evidence are permitted.';
export class BoundaryTransport implements PlannerTransport {
  readonly kind = 'chat' as const;
  private readonly socket: string;
  private readonly token: string;
  private readonly approvedPolicy: Policy;
  get policy(): Policy { return structuredClone(this.approvedPolicy); }
  constructor(socket: string, token: string, policy: Policy) {
    this.socket = socket; this.token = token; this.approvedPolicy = validatePolicy(policy);
  }
  modelPolicy(): object {
    const policy = this.policy;
    return policy.schema === 1 ? policy : { schema: 2, profile: policy.profile, evidence: 'synthetic', liveAdmission: false, authority: 'proposal_only' };
  }
  settings(budget: PlannerBudget): PlannerSettings {
    if (budget.outputTokens === null) throw new ProbeError('invalid_budget');
    return { profile: this.policy.profile, max_completion_tokens: budget.outputTokens, stream: true };
  }
  conversation(initial: { role: string; content: string }[], budget: PlannerBudget): PlannerConversation {
    const messages: any[] = structuredClone(initial), policy = this.policy;
    return {
      request: tools => ({ model: policy.routerModel, stream: true, max_completion_tokens: budget.outputTokens, messages: structuredClone(messages),
        ...(policy.schema === 1 ? { tools: [TOOL], tool_choice: tools ? 'auto' : 'none' } : tools ? { tools: [TOOL] } : {}) }),
      calls: completion => { messages.push({ role: 'assistant', content: null, tool_calls: completion.calls }); },
      read: (call, output) => { messages.push({ role: 'tool', tool_call_id: call.id, content: output }); },
      repair: completion => {
        // Preserve the historical Chat normalization: schema 2 omits blank text.
        if (completion.text.trim()) messages.push({ role: 'assistant', content: completion.text });
        messages.push({ role: 'user', content: REPAIR_INSTRUCTION });
      },
    };
  }
  async complete(value: unknown, signal: AbortSignal, responseBytes = this.approvedPolicy.limits.responseBytes, toolsAllowed = !!(value as any)?.tools && (value as any)?.tool_choice !== 'none'): Promise<Completion> {
    validateRequest(value, this.policy);
    // The boundary alone maps the reviewed consumer cap to router ingress.
    const body = JSON.stringify(value);
    if (Buffer.byteLength(body) > this.policy.limits.requestBytes) throw new ProbeError('request_budget_exhausted');
    if (signal.aborted) throw new ProbeError('cancelled');
    return new Promise((resolve, reject) => {
      let parser: ChatStream;
      let bytes = 0; let semantic = false; let text = ''; const calls: ToolCall[] = [];
      const consume = (chunks: string[]) => {
        for (const chunk of chunks) {
          if (chunk === 'data: [DONE]\n\n') continue;
          const value = parseJSON(chunk.slice(6).trim()) as any;
          const delta = value.choices[0].delta;
          if (delta.content) text += delta.content;
          if (delta.tool_calls) for (const { index: _index, ...call } of delta.tool_calls) calls.push(call);
        }
      };
      const req = request({ socketPath: this.socket, path: CHAT_PATH, method: 'POST', agent: false, signal, headers: {
        host: 'localhost', authorization: `Bearer ${this.token}`, 'content-type': 'application/json', 'content-length': Buffer.byteLength(body),
      } }, res => {
        if (res.statusCode !== 200 || res.headers['content-type']?.split(';')[0] !== 'text/event-stream') {
          res.destroy(); reject(new ProbeError(res.statusCode === 429 ? 'request_budget_exhausted' : 'route_unavailable')); return;
        }
        const requestId = res.headers['x-gaffer-request-id'];
        if (typeof requestId !== 'string' || !/^[a-f0-9]{32}$/.test(requestId)) { res.destroy(); reject(new ProbeError('invalid_request_identity')); return; }
        parser = new ChatStream(requestId, this.policy.routerModel, toolsAllowed);
        res.on('data', (chunk: Buffer) => {
          try {
            bytes += chunk.length;
            if (bytes > Math.min(responseBytes, this.approvedPolicy.limits.responseBytes)) throw new ProbeError('response_budget_exhausted');
            const next = parser.push(chunk); semantic ||= next.semantic;
            consume(next.output);
            if (next.error) throw new ProbeError(semantic ? 'partial_failure' : 'invalid_stream');
          } catch (error) { res.destroy(); req.destroy(); reject(error); }
        });
        res.on('end', () => {
          if (signal.aborted) { reject(new ProbeError('cancelled_unknown')); return; }
          try { consume(parser.end()); resolve({ text, calls, requestId, acceptedAt: Date.now() }); }
          catch { reject(new ProbeError(semantic ? 'partial_failure' : 'invalid_stream')); }
        });
        res.on('error', () => reject(new ProbeError(signal.aborted ? 'cancelled_unknown' : semantic ? 'partial_failure' : 'route_unavailable')));
      });
      // Transport close is not evidence that provider work stopped. The boundary
      // retains uncertain admission reservations; this caller never retries them.
      req.on('error', () => reject(new ProbeError(signal.aborted ? 'cancelled_unknown' : 'route_unavailable')));
      req.end(body);
    });
  }
}
