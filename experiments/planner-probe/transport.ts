import { request } from 'node:http';
import { ChatStream, validateRequest } from '../inference-boundary/protocol.ts';
import { CHAT_PATH, type Policy, validatePolicy } from '../inference-boundary/types.ts';
import { parseJSON } from '../inference-boundary/json.ts';
import { ProbeError } from './reader.ts';

export interface ToolCall { id: string; type: 'function'; function: { name: string; arguments: string } }
export interface Completion { text: string; calls: ToolCall[] }
export class BoundaryTransport {
  private readonly socket: string;
  private readonly token: string;
  private readonly approvedPolicy: Policy;
  get policy(): Policy { return structuredClone(this.approvedPolicy); }
  constructor(socket: string, token: string, policy: Policy) {
    this.socket = socket; this.token = token; this.approvedPolicy = validatePolicy(policy);
  }
  async complete(value: unknown, signal: AbortSignal, responseBytes = this.approvedPolicy.limits.responseBytes): Promise<Completion> {
    const body = validateRequest(value, this.policy);
    if (Buffer.byteLength(body) > this.policy.limits.requestBytes) throw new ProbeError('request_budget_exhausted');
    if (signal.aborted) throw new ProbeError('cancelled');
    return new Promise((resolve, reject) => {
      const parser = new ChatStream('planner_output', this.policy.routerModel, (value as any).tool_choice !== 'none');
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
          try { consume(parser.end()); resolve({ text, calls }); }
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
