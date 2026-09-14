import assert from 'node:assert/strict';
import { access, readdir, readFile } from 'node:fs/promises';
import { connect } from 'node:net';
import { request } from 'node:http';
import { PlannerSession, DEFAULT_BUDGET, type Budget } from './planner.ts';
import { BoundaryTransport } from './transport.ts';
import { snapshot } from './reader.ts';
import { BRIEF, FIXTURE_ROOT, FIXTURE_HASH } from './public-fixture.ts';
let input = ''; for await (const chunk of process.stdin) input += chunk;
const grant = JSON.parse(input), scenario = grant.scenario;
const budget: { -readonly [K in keyof Budget]: number } = { ...DEFAULT_BUDGET };
if (scenario === 'read-budget') budget.readBytes = 1;
if (scenario === 'file-budget') budget.files = 0;
if (scenario === 'request-budget') budget.requests = 1;
if (scenario === 'request-bytes') budget.requestBytes = 1;
if (scenario === 'response-bytes') budget.responseBytes = 1;
if (scenario === 'hold') budget.totalMs = 200;
if (scenario === 'injection') budget.repairs = 0;
const transport = new BoundaryTransport('/router/inference.sock', grant.token, grant.policy);
const requests: any[] = [], effects: any[] = [];
const complete = transport.complete.bind(transport);
transport.complete = async (...args) => {
  const record: any = { at: Date.now(), body: structuredClone(args[0]), toolsAllowed: args[3] }; requests.push(record);
  try { const value = await complete(...args); record.completedAt = Date.now(); record.requestId = value.requestId; record.calls = value.calls; return value; }
  catch (error: any) { record.failedAt = Date.now(); record.error = error.code ?? error.reason ?? error.message; throw error; }
};
const file = await snapshot(FIXTURE_ROOT, 'fixture.txt', FIXTURE_HASH, 4096, AbortSignal.timeout(1000));
const planner = new PlannerSession(transport, file, BRIEF, budget);
let previous = '';
const observer = setInterval(() => { const usage = (planner as any).usage; const now = JSON.stringify(usage); if (now !== previous) { effects.push({ at: Date.now(), ...usage }); previous = now; } }, 1);
const result = await planner.run(); clearInterval(observer); effects.push({ at: Date.now(), ...result.usage });
assert.deepEqual(await planner.run(), result); assert.equal(requests.length, result.usage.requests);
const inaccessible: string[] = [];
for (const path of ['/router-source','/probe','/bridge','/planner-probe/router-gateway.ts','/consumer/experiments/planner-probe/router-gateway.ts','/consumer/experiments/inference-boundary/boundary.ts','/consumer/experiments/router-boundary-bridge','/work/router.sock','/work/journal/state.json','/consumer/experiments/router-authority-extension/overlay/evaluation-scope.mjs','/var/run/docker.sock']) { await assert.rejects(access(path)); inaccessible.push(path); }
assert.deepEqual(await readdir('/router'), ['inference.sock']);
const tcp = await new Promise<string>(resolve => { const socket = connect({ host: '1.1.1.1', port: 443 }); socket.setTimeout(100, () => { socket.destroy(); resolve('blocked'); }); socket.once('error', () => resolve('blocked')); socket.once('connect', () => { socket.destroy(); resolve('connected'); }); }); assert.equal(tcp, 'blocked');
const management = await new Promise<number>(resolve => { const req = request({ socketPath: '/router/inference.sock', path: '/api/providers', method: 'GET', headers: { host: 'localhost' } }, res => { res.resume(); res.on('end', () => resolve(res.statusCode!)); }); req.on('error', () => resolve(0)); req.end(); }); assert.equal(management, 404);
const resources: Record<string, string | null> = {}; for (const key of ['memory.peak','memory.events','pids.peak']) { try { resources[key] = (await readFile('/sys/fs/cgroup/' + key, 'utf8')).trim(); } catch { resources[key] = null; } }
console.log(JSON.stringify({ resources, result, requests, effects, containment: { inaccessible, directTcp: tcp, management }, runtime: { node: process.version }, repeatedRunCached: true }));
