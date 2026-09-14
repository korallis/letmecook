import { request } from 'node:http';
import { ChatStream, validateRequest } from '../inference-boundary/protocol.ts';
import { CHAT_PATH, type Policy, validatePolicy } from '../inference-boundary/types.ts';
import { parseJSON } from '../inference-boundary/json.ts';
import { ProbeError } from './reader.ts';

export interface ToolCall { id: string; type: 'function'; function: { name: string; arguments: string } }
export interface Completion { text: string; calls: ToolCall[]; requestId: string; acceptedAt: number }
export class BoundaryTransport {
  private readonly socket: string;
  private readonly token: string;
  private readonly approvedPolicy: Policy;
  get policy(): Policy { return structuredClone(this.approvedPolicy); }
  constructor(socket: string, token: string, policy: Policy) {
    this.socket = socket; this.token = token; this.approvedPolicy = validatePolicy(policy);
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
