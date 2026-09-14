// Untrusted worker transport. Metadata is checked here, never used as authority.
import { createServer, request, type IncomingMessage } from 'node:http';
import { parseJSON } from '../../inference-boundary/json.ts';
export interface RequestObservation { path: string; headers: Record<string, string>; body: any; rawBody: string; startedAt: number; requestId: string | null; status: number | null; firstResponseAt?: number; endedAt?: number }
export function validateHeaders(rawHeaders: string[], body: any, token: string, rawBody: string) {
  const headers: Record<string, string> = {};
  for (let i = 0; i < rawHeaders.length; i += 2) { const key = rawHeaders[i].toLowerCase(); if (Object.hasOwn(headers, key)) throw Error('duplicate_client_header'); headers[key] = rawHeaders[i + 1]; }
  const required = ['content-type', 'user-agent', 'originator', 'session-id', 'x-session-affinity', 'x-session-id', 'connection', 'accept', 'host', 'accept-encoding', 'content-length', 'authorization'];
  if (Object.keys(headers).length !== required.length || required.some(k => !Object.hasOwn(headers, k)) || headers.authorization !== 'Bearer ' + token || headers.host !== '127.0.0.1:8765' || headers['content-type'] !== 'application/json' || headers.connection !== 'keep-alive' || headers.accept !== '*/*' || headers['accept-encoding'] !== 'gzip, deflate, br, zstd' || headers['content-length'] !== String(Buffer.byteLength(rawBody))) throw Error('unsupported_client_headers');
  if (headers.originator !== 'opencode' || headers['user-agent'] !== 'opencode/1.18.30 (linux 6.12.76-linuxkit; arm64) ai-sdk/provider-utils/4.0.40 runtime/bun/1.3.14' || !/^ses_[a-zA-Z0-9]{1,64}$/.test(body.prompt_cache_key) || ['session-id', 'x-session-affinity', 'x-session-id'].some(k => headers[k] !== body.prompt_cache_key)) throw Error('native_client_metadata_mismatch');
  const { authorization, ...observed } = headers; return observed;
}
export async function relay(token: string, maxBytes: number, onRequest?: (observation: RequestObservation) => void) {
  const observations: RequestObservation[] = [], rejected: { rawBody: string; body: unknown; reason: string }[] = [];
  let pending = false;
  const server = createServer(async (req: IncomingMessage, res) => {
    let ownsRequest = false;
    try {
      if (req.method !== 'POST' || req.url !== '/v1/responses') throw Error('unsupported_client_path');
      if (pending || observations.length >= 32) throw Error('client_request_count');
      pending = true; ownsRequest = true; res.once('close', () => { pending = false; });
      const chunks: Buffer[] = []; let size = 0;
      for await (const chunk of req) { size += chunk.length; if (size > maxBytes) throw Error('client_request_limit'); chunks.push(chunk); }
      const rawBody = new TextDecoder('utf-8', { fatal: true }).decode(Buffer.concat(chunks)), body = parseJSON(rawBody);
      let headers: Record<string, string>;
      try { headers = validateHeaders(req.rawHeaders, body, token, rawBody); } catch (error) { if (rejected.length < 32) rejected.push({ rawBody, body, reason: String(error) }); throw error; }
      const observation: RequestObservation = { path: req.url, headers, body, rawBody, startedAt: Date.now(), requestId: null, status: null }; observations.push(observation);
      const upstream = request({ socketPath: '/router/inference.sock', path: req.url, method: 'POST', headers: { host: 'localhost', authorization: 'Bearer ' + token, 'content-type': 'application/json', 'content-length': Buffer.byteLength(rawBody) } });
      upstream.on('error', () => res.destroy()); res.on('close', () => upstream.destroy());
      upstream.on('response', response => {
        observation.status = response.statusCode ?? null;
        const id = response.headers['x-gaffer-request-id']; observation.requestId = typeof id === 'string' ? id : null;
        response.on('data', () => observation.firstResponseAt ??= Date.now()); response.on('end', () => observation.endedAt = Date.now()); response.on('error', () => res.destroy());
        res.writeHead(response.statusCode!, response.headers); response.pipe(res);
      });
      upstream.end(rawBody); onRequest?.(observation);
    } catch {
      if (ownsRequest) pending = false;
      if (!res.headersSent) res.writeHead(400, { 'content-type': 'application/json' });
      res.end('{"error":"native_worker_transport_denied"}');
    }
  });
  await new Promise<void>((resolve, reject) => { server.once('error', reject); server.listen(8765, '127.0.0.1', resolve); });
  return { observations, rejected, async close() { server.closeAllConnections(); await new Promise<void>((resolve, reject) => server.close(e => e ? reject(e) : resolve())); } };
}
