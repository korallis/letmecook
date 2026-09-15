// Trusted synthetic authority in its own network-none container. Never mount /work into a worker.
import assert from 'node:assert/strict';
import { createServer, type ServerResponse } from 'node:http';
import { once } from 'node:events';
import { randomBytes } from 'node:crypto';
import { chmod, writeFile } from 'node:fs/promises';
import { Boundary } from '../../../experiments/inference-boundary/boundary.ts';
import { PolicyGate, type FrozenAuthority } from '../../../experiments/inference-boundary/policy.ts';
import { fixturePolicy, type Binding, type Policy } from '../../../experiments/inference-boundary/types.ts';
import { OPENCODE_PROFILE } from '../../../experiments/inference-boundary/profiles.ts';
const mode = process.argv[2];
const emit = (type: string, data: unknown) => console.log(JSON.stringify({ source: 'gateway', type, data }));
const routerKey = randomBytes(32).toString('hex');
const policy: Policy = { ...fixturePolicy({ requestCount: 3, totalMs: 12000, firstOutputMs: 5000, idleMs: 2000, attemptMs: 60000 }), profile: OPENCODE_PROFILE };
const active = new Set<string>(); let requests = 0;
const held = new Set<ServerResponse>();
const authority: FrozenAuthority = { async readFrozenPolicy() { return structuredClone(policy); }, async quiescent(ids) { return ids.every(id => !active.has(id)); }, async replaceFrozenPolicy() { throw new Error('fixture_immutable'); } };
const server = createServer(async (req, res) => {
  assert.equal(req.headers.authorization, `Bearer ${routerKey}`);
  const id = String(req.headers['x-gaffer-request-id']); active.add(id);
  res.on('close', () => { active.delete(id); held.delete(res); emit('upstream-closed', { requestId: id, ended: res.writableEnded, fixtureQuiescence: true }); });
  let text = ''; for await (const bytes of req) { text += bytes; assert(text.length <= 65536); }
  const body = JSON.parse(text); requests++;
  emit('request', { number: requests, requestId: id, path: req.url, headerNames: Object.keys(req.headers), body });
  if (mode === 'cancel' || mode === 'tree' || mode === 'crash' || mode === 'ambient-timeout') { held.add(res); return; }
  if (mode === 'router-error') { res.writeHead(503, { 'content-type': 'application/json' }).end(JSON.stringify({ error: { message: 'SYNTHETIC_ROUTER_SECRET_MUST_NOT_LEAK' } })); return; }
  res.writeHead(200, { 'content-type': 'text/event-stream' });
  const frame = (delta: unknown, finish_reason: string | null = null) => `data: ${JSON.stringify({ id: 'synthetic-provider-id', model: 'fixture_model', choices: [{ index: 0, delta, finish_reason }] })}\n\n`;
  let output = frame({ role: 'assistant', content: '' });
  if (requests === 1) {
    const path = mode === 'forbidden' ? '/work/home/auth.json' : '/work/repo/greeting.txt';
    output += frame({ tool_calls: [{ index: 0, id: 'call_fixture_edit_1', type: 'function', function: { name: 'edit', arguments: `{"filePath":${JSON.stringify(path)},` } }] });
    if (mode === 'partial') { res.end(output); return; }
    output += frame({ tool_calls: [{ index: 0, function: { arguments: '"oldString":"hello","newString":"hello from harness"}' } }] }) + frame({}, 'tool_calls');
  } else output += frame({ content: 'Bounded synthetic edit complete.' }) + frame({}, 'stop');
  if (mode !== 'usage-missing') output += `data: ${JSON.stringify({ choices: [], usage: { prompt_tokens: 17, completion_tokens: 9, total_tokens: 26 } })}\n\n`;
  output += 'data: [DONE]\n\n';
  // Split the actual transport as well as the native-event unit fixtures.
  for (let i = 0; i < output.length; i += 17) res.write(output.slice(i, i + 17)); res.end();
});
server.listen('/work/upstream.sock'); await once(server, 'listening'); await chmod('/work/upstream.sock', 0o600);
const gate = await PolicyGate.open('/work/journal', authority); await gate.activate();
const boundary = new Boundary(gate, '/work/upstream.sock', routerKey);
await boundary.listen('/router/inference.sock');
const binding: Binding = { attemptId: 'fixture_attempt', grantId: 'fixture_grant', taskId: 'fixture_task', leaseId: 'fixture_lease', fence: 1, role: 'worker', routerId: policy.routerId, routeId: policy.routeId, revision: policy.revision, epoch: policy.epoch, expiresAt: Date.now() + 60000, leaseExpiresAt: Date.now() + 60000 };
await writeFile('/router/grant.json', JSON.stringify({ token: boundary.issue(binding), binding }), { mode: 0o600 });
emit('ready', { mode, profile: policy.profile, graph: policy.graph, limits: policy.limits });
process.once('SIGTERM', async () => {
  boundary.revoke(binding.attemptId); await boundary.close();
  for (const response of held) response.destroy(); server.closeAllConnections(); server.close();
  emit('finished', { requests, active: active.size, audit: boundary.audit, journal: gate.snapshot(), usage: mode === 'usage-missing' ? 'unknown' : 'synthetic fixture numbers only', liveRouterCalled: false });
});
