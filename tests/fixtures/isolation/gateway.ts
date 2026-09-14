// Synthetic inference boundary only. No provider credentials, account logic, or upstream calls.
import { createServer } from 'node:http';
import { chmodSync } from 'node:fs';

const paths = new Set(['/v1/chat/completions', '/v1/messages', '/v1/responses']);
let accepted = 0;
const server = createServer((req, res) => {
  const reply = (status: number, value: string) => {
    res.writeHead(status, { 'content-type': 'application/json' });
    res.end(JSON.stringify({ fixture: true, result: value }));
    req.resume();
  };
  if (req.method !== 'POST' || !paths.has(req.url ?? '')) return reply(404, 'not_inference');
  if (req.headers.authorization !== `Bearer ${process.env.FIXTURE_TOKEN}`) return reply(401, 'scope');
  if (req.headers['x-gaffer-policy-epoch'] !== '7') return reply(403, 'epoch');
  let body = '';
  req.setTimeout(1000, () => req.destroy());
  req.on('data', chunk => {
    body += chunk;
    if (Buffer.byteLength(body) > 4096) { reply(413, 'body_limit'); req.removeAllListeners('data'); }
  });
  req.on('end', () => {
    if (res.writableEnded) return;
    let value: any;
    try { value = JSON.parse(body); } catch { return reply(400, 'invalid_json'); }
    if (value?.model !== 'fixture/route') return reply(403, 'route');
    // Fixed minimal envelope so the fixture cannot silently accept a routing/control field.
    const allowed = new Set(['model', 'messages', 'input', 'max_tokens', 'max_output_tokens']);
    if (Object.keys(value).some(key => !allowed.has(key))) return reply(400, 'unsupported_field');
    const max = value.max_output_tokens ?? value.max_tokens;
    if (!Number.isInteger(max) || max < 1 || max > 64) return reply(422, 'output_limit');
    if (accepted >= 3) return reply(429, 'attempt_budget');
    accepted++;
    reply(200, 'synthetic inference accepted');
  });
});
server.maxConnections = 4;
server.headersTimeout = 2000;
server.requestTimeout = 2000;
server.keepAliveTimeout = 100;
server.listen('/router/inference.sock', () => {
  chmodSync('/router/inference.sock', 0o600);
  console.log(JSON.stringify({ ready: true, synthetic: true }));
});
