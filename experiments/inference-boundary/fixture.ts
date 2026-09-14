import { createServer, type ServerResponse } from 'node:http';
import { once } from 'node:events';
import { chmod, mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { randomBytes } from 'node:crypto';
import { Boundary } from './boundary.ts';
import { PolicyGate, type FrozenAuthority } from './policy.ts';
import { fixturePolicy, type Binding, type Policy } from './types.ts';

export const SYNTHETIC_SECRET = 'SYNTHETIC_PROVIDER_SECRET_MUST_NOT_LEAK';
export type Mode = 'text' | 'tools' | 'partial_tool' | 'error' | 'error_after' | 'hold' | 'hidden_cancel' | 'hidden_drop' | 'heartbeat' | 'idle' | 'large' | 'redirect';
export const event = (delta: unknown, finish_reason: string | null = null) => `data: ${JSON.stringify({ id: SYNTHETIC_SECRET, choices: [{ index: 0, delta, finish_reason }], providerSpecificData: { refreshToken: SYNTHETIC_SECRET }, usage: { apiKey: SYNTHETIC_SECRET } })}\n\n`;
export const textStream = () => event({ content: 'fixture café' }) + event({}, 'stop') + 'data: [DONE]\n\n';
export const toolStream = () => event({ tool_calls: [{ index: 0, id: 'call_fixture_1', type: 'function', function: { name: 'read_file', arguments: '{"path":' } }] }) + event({ tool_calls: [{ index: 0, function: { arguments: '"fixture.txt"}' } }] }) + event({}, 'tool_calls') + 'data: [DONE]\n\n';

export class SyntheticRouter implements FrozenAuthority {
  readonly key = 'synthetic-router-' + randomBytes(24).toString('hex');
  readonly calls: { requestId: string; taskId: string; attemptId: string; revision: string; model: string; authenticated: boolean; headerNames: string[]; body: any }[] = [];
  readonly active = new Set<string>();
  readonly hidden = new Set<string>();
  readonly pending = new Map<string, ServerResponse>();
  private modes: Mode[] = [];
  failReadback = false;
  mutations = 0;
  private policy: Policy;
  readonly server = createServer(async (req, res) => {
    const chunks: Buffer[] = [];
    for await (const chunk of req) chunks.push(chunk);
    const body = JSON.parse(Buffer.concat(chunks).toString());
    const requestId = String(req.headers['x-gaffer-request-id']);
    const authenticated = req.headers.authorization === `Bearer ${this.key}`;
    this.calls.push({ requestId, taskId: String(req.headers['x-gaffer-task-id']), attemptId: String(req.headers['x-gaffer-attempt-id']), revision: String(req.headers['x-gaffer-policy-revision']), model: body.model, authenticated, headerNames: Object.keys(req.headers), body });
    if (!authenticated || req.url !== '/v1/chat/completions' || body.model !== this.policy.routerModel) { res.writeHead(403); res.end(SYNTHETIC_SECRET); return; }
    this.active.add(requestId);
    const mode = this.modes.shift() ?? 'text';
    res.once('close', () => { this.active.delete(requestId); this.pending.delete(requestId); });
    if (mode === 'hidden_cancel') { this.hidden.add(requestId); this.pending.set(requestId, res); return; }
    if (mode === 'hidden_drop') { this.hidden.add(requestId); res.destroy(); return; }
    if (mode === 'hold') { this.pending.set(requestId, res); return; }
    if (mode === 'redirect') { res.writeHead(302, { location: 'http://unapproved.invalid', 'x-secret': SYNTHETIC_SECRET }); res.end(SYNTHETIC_SECRET); return; }
    if (mode === 'error') { res.writeHead(503, { 'x-secret': SYNTHETIC_SECRET }); res.end(JSON.stringify({ error: { message: SYNTHETIC_SECRET } })); return; }
    res.writeHead(200, { 'content-type': 'text/event-stream', 'x-upstream-secret': SYNTHETIC_SECRET, 'set-cookie': `secret=${SYNTHETIC_SECRET}` });
    if (mode === 'heartbeat' || mode === 'idle') {
      if (mode === 'idle') res.write(event({ content: 'first' }));
      const timer = setInterval(() => res.write(': heartbeat\n\n'), 10); res.once('close', () => clearInterval(timer)); return;
    }
    if (mode === 'large') { res.end(event({ content: 'x'.repeat(100000) })); return; }
    if (mode === 'error_after') { res.end(event({ content: 'partial' }) + `event: error\ndata: {"error":{"message":"${SYNTHETIC_SECRET}"}}\n\n`); return; }
    if (mode === 'partial_tool') { res.end(event({ tool_calls: [{ index: 0, id: 'call_fixture_1', type: 'function', function: { name: 'read_file', arguments: '{"path":' } }] }) + 'data: [DONE]\n\n'); return; }
    // Use real HTTP streaming with multibyte UTF-8 and tool-argument fragmentation.
    const bytes = Buffer.from(mode === 'tools' ? toolStream() : textStream());
    for (let i = 0; i < bytes.length; i += 7) res.write(bytes.subarray(i, i + 7));
    res.end();
  });
  constructor(policy = fixturePolicy()) { this.policy = structuredClone(policy); }
  enqueue(...modes: Mode[]) { this.modes.push(...modes); }
  async listen(socket: string) { this.server.listen(socket); await once(this.server, 'listening'); await chmod(socket, 0o600); }
  async readFrozenPolicy() { return structuredClone(this.policy); }
  async quiescent(ids: string[]) {
    if (!ids.length) return this.active.size === 0 && this.hidden.size === 0;
    return ids.every(id => !this.active.has(id) && !this.hidden.has(id));
  }
  async replaceFrozenPolicy(next: Policy) {
    if (this.active.size || this.hidden.size) throw new Error('fixture_not_quiescent');
    this.mutations++; this.policy = structuredClone(next);
    return this.failReadback ? { ...this.policy, routerModel: 'unapproved' } : structuredClone(this.policy);
  }
  completeHeld() { for (const res of this.pending.values()) { res.writeHead(200, { 'content-type': 'text/event-stream' }); res.end(textStream()); } }
  stopHidden() { for (const id of this.hidden) { this.pending.get(id)?.destroy(); this.hidden.delete(id); } }
  async close() { this.stopHidden(); this.server.closeAllConnections(); await new Promise<void>(resolve => this.server.close(() => resolve())); }
}

export async function setup(policy = fixturePolicy(), authorityMs = 250) {
  const directory = await mkdtemp(join(tmpdir(), 'gaffer-boundary-'));
  const routerSocket = join(directory, 'router.sock'); const workerSocket = join(directory, 'worker.sock');
  const router = new SyntheticRouter(policy); await router.listen(routerSocket);
  const gate = await PolicyGate.open(join(directory, 'policy'), router, authorityMs); await gate.activate();
  const boundary = new Boundary(gate, routerSocket, router.key); await boundary.listen(workerSocket);
  let next = 0;
  const grant = (overrides: Partial<Binding> = {}) => {
    const n = ++next; const now = Date.now();
    const binding: Binding = { attemptId: `attempt_${n}`, taskId: `task_${n}`, grantId: `grant_${n}`, leaseId: `lease_${n}`, fence: 1, role: 'worker', routerId: policy.routerId, routeId: policy.routeId, revision: policy.revision, epoch: policy.epoch, expiresAt: now + 10000, leaseExpiresAt: now + 10000, ...overrides };
    return { binding, token: boundary.issue(binding) };
  };
  return { directory, routerSocket, workerSocket, router, gate, boundary, grant, async close() { await boundary.close(); await router.close(); await rm(directory, { recursive: true, force: true }); } };
}
