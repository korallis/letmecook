import { createServer, type ServerResponse } from 'node:http';
import { once } from 'node:events';
import { mkdtemp, realpath, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { randomBytes } from 'node:crypto';
import { Boundary } from '../inference-boundary/boundary.ts';
import { PolicyGate } from '../inference-boundary/policy.ts';
import { fixturePolicy, type Binding, type Policy } from '../inference-boundary/types.ts';
import { event, toolStream } from '../inference-boundary/fixture.ts';
import { BoundaryTransport } from './transport.ts';
import { PlannerSession, DEFAULT_BUDGET, type Budget } from './planner.ts';
import { snapshot } from './reader.ts';

import { FIXTURE_HASH, FIXTURE_ROOT, BRIEF } from './public-fixture.ts';
export { FIXTURE_HASH, FIXTURE_ROOT, BRIEF, proposal } from './public-fixture.ts';
export const finishText = (text: string) => event({ content: text }) + event({}, 'stop') + 'data: [DONE]\n\n';
export type Reply = string | 'hold' | 'unavailable' | ((body: any) => string);

export async function setup(replies: Reply[] = [], budget: Budget = DEFAULT_BUDGET, locations: { temporaryParent?: string; fixtureRoot?: string } = {}) {
  const directory = await mkdtemp(join(await realpath(locations.temporaryParent ?? tmpdir()), 'gaffer-planner-'));
  const routerSocket = join(directory, 'router.sock'); const boundarySocket = join(directory, 'planner.sock');
  const policy = fixturePolicy({ requestBytes: 32768, responseBytes: 32768, outputTokens: 1024, requestCount: 3, totalMs: 4000, firstOutputMs: 2000, idleMs: 1000, attemptMs: 10000 });
  policy.routeId = 'fixture_planner'; policy.routerModel = 'gaffer-planner-fixture';
  const key = randomBytes(32).toString('hex');
  const calls: any[] = []; const active = new Set<ServerResponse>();
  const server = createServer(async (req, res) => {
    const chunks: Buffer[] = []; for await (const chunk of req) chunks.push(chunk);
    const body = JSON.parse(Buffer.concat(chunks).toString());
    if (req.headers.authorization !== `Bearer ${key}` || req.url !== '/v1/chat/completions') { res.writeHead(403); res.end(); return; }
    calls.push(body); active.add(res); res.on('close', () => active.delete(res));
    const reply = replies.shift() ?? toolStream();
    if (reply === 'hold') return;
    if (reply === 'unavailable') { res.writeHead(503); res.end('SYNTHETIC_SECRET'); return; }
    res.writeHead(200, { 'content-type': 'text/event-stream' });
    const bytes = Buffer.from(typeof reply === 'function' ? reply(body) : reply);
    for (let i = 0; i < bytes.length; i += 11) res.write(bytes.subarray(i, i + 11));
    res.end();
  });
  let gate: PolicyGate | undefined; let boundary: Boundary | undefined; let closing: Promise<void> | undefined;
  const close = (): Promise<void> => closing ??= (async () => {
    const errors: unknown[] = [];
    try { if (boundary) await boundary.close(); else if (gate) await gate.close(); }
    catch (error) { errors.push(error); }
    try {
      server.closeAllConnections();
      if (server.listening) await new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve()));
    } catch (error) { errors.push(error); }
    // A failed boundary close may retain an owner lock for uncertain work. Keep
    // its state for inspection; closing the other listener still proceeds.
    if (!errors.length) {
      try { await rm(directory, { recursive: true, force: true }); } catch (error) { errors.push(error); }
    }
    if (errors.length) throw new AggregateError(errors, 'fixture_cleanup_failed');
  })();
  try {
    server.listen(routerSocket); await once(server, 'listening');
    const authority = { readFrozenPolicy: async () => structuredClone(policy), quiescent: async () => active.size === 0,
      replaceFrozenPolicy: async (_next: Policy): Promise<Policy> => { throw new Error('fixture_writer_disabled'); } };
    gate = await PolicyGate.open(join(directory, 'policy'), authority); await gate.activate();
    boundary = new Boundary(gate, routerSocket, key); await boundary.listen(boundarySocket);
    const now = Date.now();
    const binding: Binding = { attemptId: 'planner_attempt', taskId: 'planner_task', grantId: 'planner_grant', leaseId: 'planner_lease', fence: 1, role: 'planner', routerId: policy.routerId, routeId: policy.routeId, revision: policy.revision, epoch: policy.epoch, expiresAt: now + 10000, leaseExpiresAt: now + 10000 };
    const transport = new BoundaryTransport(boundarySocket, boundary.issue(binding), policy);
    const file = await snapshot(locations.fixtureRoot ?? FIXTURE_ROOT, 'fixture.txt', FIXTURE_HASH, 4096, AbortSignal.timeout(1000));
    const planner = new PlannerSession(transport, file, BRIEF, budget);
    return { planner, transport, boundary, gate, calls, policy, directory, replies, close };
  } catch (error) {
    try { await close(); }
    catch (cleanupError) { throw new AggregateError([error, cleanupError], 'fixture_setup_and_cleanup_failed', { cause: error }); }
    throw error;
  }
}
