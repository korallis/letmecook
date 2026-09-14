import { createServer, type ServerResponse } from 'node:http';
import { once } from 'node:events';
import { mkdtemp, realpath, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { randomBytes } from 'node:crypto';
import { Boundary } from '../inference-boundary/boundary.ts';
import { PolicyGate } from '../inference-boundary/policy.ts';
import { fixturePolicy, type Binding, type Policy } from '../inference-boundary/types.ts';
import { event, toolStream } from '../inference-boundary/fixture.ts';
import { BoundaryTransport } from './transport.ts';
import { PlannerSession, DEFAULT_BUDGET, type Budget } from './planner.ts';
import { snapshot } from './reader.ts';

export const FIXTURE_HASH = 'cfbd4f521e876bf7c58582c01f8e6d79b81f8203ba2d98f8fd2258853bffe4e5';
export const FIXTURE_ROOT = fileURLToPath(new URL('../../tests/fixtures/planner', import.meta.url));
export const BRIEF = 'Propose correcting the greeting spelling in fixture.txt. Preserve other text. Do not implement or publish the change.';
export function proposal(revision: string, fileRead = true) {
  const refs = fileRead ? ['capture', 'fixture.txt'] : ['capture'];
  return { schema: 1, kind: 'plan_proposal', input_revision: revision, outcome: 'Correct the greeting spelling.', constraints: ['Preserve other text.'], exclusions: ['Execution and delivery require separate authority.'],
    criteria: [{ id: 'c1', statement: 'The greeting spells world correctly.', evidence_required: 'Review the proposed one-word diff.' }], allowed_paths: ['fixture.txt'], allowed_systems: [], assumptions: [], unresolved_questions: [],
    steps: [{ id: 's1', description: 'Propose replacing wrld with world in the greeting.', criterion_ids: ['c1'], source_refs: refs }],
    assessment: { work_class: 'text_edit', consequence: 'low', unknowns: [], source_refs: refs } };
}
export const finishText = (text: string) => event({ content: text }) + event({}, 'stop') + 'data: [DONE]\n\n';
export type Reply = string | 'hold' | 'unavailable' | ((body: any) => string);

export async function setup(replies: Reply[] = [], budget: Budget = DEFAULT_BUDGET) {
  const directory = await mkdtemp(join(await realpath(tmpdir()), 'gaffer-planner-'));
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
  server.listen(routerSocket); await once(server, 'listening');
  const authority = { readFrozenPolicy: async () => structuredClone(policy), quiescent: async () => active.size === 0,
    replaceFrozenPolicy: async (_next: Policy): Promise<Policy> => { throw new Error('fixture_writer_disabled'); } };
  const gate = await PolicyGate.open(join(directory, 'policy'), authority); await gate.activate();
  const boundary = new Boundary(gate, routerSocket, key); await boundary.listen(boundarySocket);
  const now = Date.now();
  const binding: Binding = { attemptId: 'planner_attempt', taskId: 'planner_task', grantId: 'planner_grant', leaseId: 'planner_lease', fence: 1, role: 'planner', routerId: policy.routerId, routeId: policy.routeId, revision: policy.revision, epoch: policy.epoch, expiresAt: now + 10000, leaseExpiresAt: now + 10000 };
  const transport = new BoundaryTransport(boundarySocket, boundary.issue(binding), policy);
  const file = await snapshot(FIXTURE_ROOT, 'fixture.txt', FIXTURE_HASH, 4096, AbortSignal.timeout(1000));
  const planner = new PlannerSession(transport, file, BRIEF, budget);
  return { planner, transport, boundary, gate, calls, policy, directory, replies, async close() {
    await boundary.close(); server.closeAllConnections(); await new Promise<void>(resolve => server.close(() => resolve()));
    await rm(directory, { recursive: true, force: true });
  } };
}
