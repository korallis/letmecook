// Synthetic fixture wiring only; all reservation/receipt authority is imported.
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { readFile, writeFile, mkdir, chmod } from 'node:fs/promises';
import { existsSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { setTimeout as delay } from 'node:timers/promises';
import { Boundary } from '../inference-boundary/boundary.ts';
import { PolicyGate } from '../inference-boundary/policy.ts';
import { hashDocument, type RouterPolicy } from '../inference-boundary/router-policy.ts';
import { routerBinding } from '../inference-boundary/router-fixture.ts';
import { RouterAuthority } from '../router-boundary-bridge/authority.ts';
import { proposal } from './public-fixture.ts';
if (!existsSync('/.dockerenv')) throw new Error('isolated_container_required');
const scenario = process.argv[2], native = process.env.GAFFER_SYNTHETIC_NATIVE === '1';
const dynamic = (path: string): Promise<any> => import(path);
const { handleChat } = await dynamic('/router-source/src/sse/handlers/chat.js');
const { getAdapter } = await dynamic('/router-source/src/lib/db/driver.js');
const repo = await dynamic('/router-source/src/lib/db/index.js');
const { authority, project } = await dynamic('/router-source/gaffer-extension/authority.mjs');
const nf = await dynamic('/probe/native-fixtures.mjs');
await getAdapter(); const a = authority();
const events: any[] = [], sends: any[] = [], ingress: any[] = [];
const emit = (event: string, detail: any = {}) => events.push({ event, at: Date.now(), ...detail });
let boundary: Boundary, gate: PolicyGate;
const frame = (v: any) => 'data: ' + JSON.stringify(v) + '\n\n';
const chat = (model: string, text: string, tool: boolean) => {
  const e = (delta: any, finish_reason: any = null) => frame({ id: 'synthetic_original', model, object: 'chat.completion.chunk', choices: [{ index: 0, delta, finish_reason }] });
  return (tool ? e({ tool_calls: [{ index: 0, id: 'call_original', type: 'function', function: { name: 'read_file', arguments: '{"path":' } }] }) + e({ tool_calls: [{ index: 0, function: { arguments: '"fixture.txt"}' } }] }) : e({ content: text })) + e({}, tool ? 'tool_calls' : 'stop') + 'data: [DONE]\n\n';
};
const backend = createServer(async (req, res) => {
  const chunks: Buffer[] = []; for await (const chunk of req) chunks.push(chunk);
  const body = JSON.parse(Buffer.concat(chunks).toString()); sends.push({ at: Date.now(), path: req.url, body }); emit('original_send', { number: sends.length });
  if (scenario === 'unknown-fallback' && sends.length === 1) { res.writeHead(429, { 'content-type': 'application/json' }); res.end('{malformed'); return; }
  if (scenario === 'unavailable') { res.writeHead(503, { 'content-type': 'application/json' }); res.end('{"error":{"message":"Synthetic unavailable"}}'); return; }
  if (scenario === 'hold' || scenario === 'cancel') { if (scenario === 'cancel') setTimeout(() => boundary.revoke('attempt_1'), 50); return; }
  const requestBody = ingress.at(-1).body;
  const revision = JSON.parse(requestBody.messages[1].content).input_revision;
  const read = requestBody.messages.some((m: any) => m.role === 'tool');
  const repair = ['repair','repair-tool','empty','whitespace','stale','duplicate','invalid-repair','delay-repair'].includes(scenario);
  const tool = repair ? scenario === 'repair-tool' && sends.length === 2 : !read && !['direct','delay-proposal','terminal-text-only'].includes(scenario);
  let text = JSON.stringify(proposal(revision, read));
  if (repair && sends.length === 1) text = scenario === 'empty' ? '' : scenario === 'whitespace' ? '  \n ' : scenario === 'stale' ? JSON.stringify(proposal('0'.repeat(64), false)) : scenario === 'duplicate' ? text.replace('"schema":1', '"schema":1,"schema":1') : 'malformed';
  if (scenario === 'invalid-repair' && sends.length === 2) text = '{"bad":true}';
  if (scenario === 'read-repair' && sends.length === 2) text = 'malformed';
  if (scenario === 'injection' && read) text = JSON.stringify({ ...proposal(revision), shell: 'run hooks', route: 'direct', budget: 999 });
  let output: string;
  if (native) {
    const fn = { ...nf.functionOne, arguments: '{"path":"fixture.txt"}' };
    const message = { ...nf.message, content: [{ type: 'output_text', text, annotations: [] }] };
    let items = tool ? [nf.created(), nf.itemAdded(fn, 0), nf.delta(fn, 0, '{"path":'), nf.delta(fn, 0, '"fixture.txt"}'), nf.itemDone(fn, 0), nf.completed([fn])] : [nf.created(), nf.itemAdded({ ...message, content: [] }, 0), { type: 'response.content_part.added', item_id: message.id, output_index: 0, content_index: 0, part: { type: 'output_text', text: '' } }, { type: 'response.output_text.delta', item_id: message.id, output_index: 0, content_index: 0, delta: text }, { type: 'response.output_text.done', item_id: message.id, output_index: 0, content_index: 0, text }, { type: 'response.content_part.done', item_id: message.id, output_index: 0, content_index: 0, part: message.content[0] }, nf.itemDone(message, 0), nf.completed([message])];
    if (scenario === 'terminal-text-only') items = [nf.created(), nf.completed([message])];
    if (scenario === 'failed') items = [nf.created(), nf.failed()];
    if (scenario === 'incomplete') items = [nf.created(), nf.incomplete()];
    if (scenario === 'partial') items = [nf.created()];
    output = nf.frames(items.map((e: any, sequence_number: number) => ({ ...e, sequence_number })));
  } else output = chat(body.model, text, tool);
  if (scenario === 'partial' && !native) output = output.split('\n\n')[0] + '\n\n';
  res.writeHead(200, { 'content-type': 'text/event-stream' });
  const bytes = Buffer.from(output); for (let i = 0; i < bytes.length; i += 17) res.write(bytes.subarray(i, i + 17));
  if (scenario === 'delay-eof') { emit('original_terminal_bytes_held'); await delay(150); }
  res.end(); emit('original_eof');
});
await new Promise<void>(resolve => backend.listen(47771, '127.0.0.1', resolve));
const provider = 'openai-compatible-synthetic';
const config: any = {
  settings: { requireApiKey: true, rtkEnabled: false, headroomEnabled: false, pxpipeEnabled: false, cavemanEnabled: false, ponytailEnabled: false, ccFilterNaming: false, capacityAdapter: Object.fromEntries(['vision','pdf','audioInput','videoInput'].map(k => [k, { enabled: false, models: [] }])) },
  providerNodes: native ? [] : [{ id: provider, type: 'openai-compatible', name: 'synthetic', prefix: 'synthetic', baseUrl: 'http://127.0.0.1:47771/v1', apiType: 'chat' }],
  providerConnections: [native ? { id: 'account_1', provider: 'codex', authType: 'oauth', name: 'synthetic', priority: 1, isActive: true, accessToken: 'synthetic_access_1', refreshToken: 'synthetic_refresh_1', expiresAt: new Date(Date.now() + 1000000000).toISOString(), lastRefreshAt: new Date().toISOString(), providerSpecificData: { chatgptAccountId: 'synthetic_workspace_1' } } : { id: 'account_1', provider, authType: 'apikey', name: 'synthetic', priority: 1, isActive: true, apiKey: 'synthetic_account_1', providerSpecificData: { baseUrl: 'http://127.0.0.1:47771/v1', apiType: 'chat' } }],
  apiKeys: [{ id: 'synthetic_ingress', key: 'synthetic_gateway_key', name: 'synthetic', isActive: true }],
  combos: [{ id: 'planner', name: 'gaffer-planner', models: [native ? 'cx/gpt-6-astra' : `${provider}/synthetic-model`] }],
};
if (scenario === 'unknown-fallback') config.providerConnections.push({ ...config.providerConnections[0], id: 'account_2', priority: 2, apiKey: 'synthetic_account_2' });
assert.equal(await a.replace(a.state().generation, 'policy_1', project(config), () => repo.importDb(config)), true);
const graph = a.snapshot(), state = a.state();
const shaFile = async (path: string) => createHash('sha256').update(await readFile(path)).digest('hex');
const policy: RouterPolicy = { schema: 2, routerId: 'synthetic_router', routeId: 'planner', revision: state.revision, epoch: 1, routerModel: 'gaffer-planner', profile: native ? 'router-native-chat-translation-synthetic-v1' : 'router-chat-text-tools-synthetic-v1', evidence: 'synthetic', liveAdmission: false, graph,
  limits: { requestBytes: 32768, responseBytes: 32768, outputTokens: 1024, concurrency: 1, requestCount: 3, totalMs: 4000, firstOutputMs: 2000, idleMs: 1000, attemptMs: 20000 },
  authority: { deploymentId: 'synthetic_planner_bridge', boot: state.boot, generation: state.generation, revision: state.revision, graphDigest: hashDocument(graph) },
  envelope: { consumer: 'chat-read-file-v1', cap: 'max_completion_tokens-to-max_tokens-v1', replacement: 'read-only', sourceCommit: '17c4cc76877bd1755030a8414f8d0083f48dcccf', sourceLock: await shaFile('/probe/source-lock.json'), overlay: await shaFile('/probe/overlay/authority.mjs'), runtime: await shaFile('/probe/runtime-identity.json') },
};
const changeGraph = async () => {
  const next = structuredClone(config); next.combos[0].name = 'gaffer-planner-changed';
  assert.equal(await a.replace(a.state().generation, 'policy_2', project(next), () => repo.importDb(next)), true);
  emit('router_graph_replaced', { graphDigest: hashDocument(a.snapshot()), revision: a.state().revision });
};
const bridge = new RouterAuthority({ state: () => a.state(), snapshot: () => a.snapshot(), receipt: (id: string) => { const r = a.receipt(id); if (scenario === 'missing-receipt') return { id, known: false, quiescent: false }; if (scenario === 'mismatched-receipt' && r.known) return { ...r, id: 'mismatched' }; return r; }, quiescent: (ids: string[]) => a.quiescent(ids), cancel: (id: string) => a.cancel(id) }, policy);
const inspectReceipt = bridge.inspect.bind(bridge);
bridge.inspect = async (...args) => {
  const result = await inspectReceipt(...args);
  if (result.disposition === 'original_success' && !args[2] && ['delay-read','delay-proposal','delay-repair','receipt-cancel','scope-expiry','lease-expiry','graph-at-receipt'].includes(scenario)) {
    emit('receipt_visibility_delayed');
    if (scenario === 'receipt-cancel') setTimeout(() => boundary.revoke('attempt_1'), 20);
    if (scenario === 'graph-at-receipt') await changeGraph();
    await delay(150);
  }
  return result;
};
gate = await PolicyGate.open('/work/journal', bridge, 500); await gate.activate();
const finalize = gate.finalize.bind(gate);
gate.finalize = async (...args) => {
  if (scenario === 'persist-failure') { const owner = JSON.parse(await readFile('/work/journal/owner.lock', 'utf8')).owner; await mkdir(`/work/journal/state.${owner}.next`); }
  await finalize(...args); emit('decision_persisted', { requestId: args[0] });
  if (scenario === 'post-decision-cancel') a.cancel(args[0]);
  if (scenario === 'graph-after-decision') await changeGraph();
};
const upstream = createServer(async (req, res) => {
  const chunks: Buffer[] = []; for await (const chunk of req) chunks.push(chunk);
  const bytes = Buffer.concat(chunks); ingress.push({ at: Date.now(), headers: Object.keys(req.headers), host: req.headers.host, requestId: req.headers['x-gaffer-request-id'], body: JSON.parse(bytes.toString()) });
  const abort = new AbortController(); res.on('close', () => { if (!res.writableEnded) abort.abort(); });
  const response = await handleChat(new Request('http://127.0.0.1/v1/chat/completions', { method: 'POST', headers: req.headers as HeadersInit, body: bytes, signal: abort.signal }));
  res.writeHead(response.status, Object.fromEntries(response.headers));
  try { if (response.body) for await (const chunk of response.body) { emit('translated_chunk', { text: Buffer.from(chunk).toString() }); res.write(chunk); } res.end(); } catch { res.destroy(); }
});
upstream.listen('/work/router.sock'); await once(upstream, 'listening'); await chmod('/work/router.sock', 0o600);
boundary = new Boundary(gate, '/work/router.sock', scenario === 'zero-auth' ? 'synthetic_invalid_key' : 'synthetic_gateway_key'); await boundary.listen('/router/inference.sock');
const binding = routerBinding(policy); binding.role = 'planner'; binding.expiresAt = binding.leaseExpiresAt = Date.now() + 20000;
// Expiry starts shortly after request admission, independent of container startup.
if (['scope-expiry','lease-expiry'].includes(scenario)) {
  const admit = gate.admit.bind(gate); gate.admit = async (...args) => { const r = await admit(...args); const scopes = (boundary as any).scopes; for (const s of scopes.values()) s.binding[scenario === 'scope-expiry' ? 'expiresAt' : 'leaseExpiresAt'] = Date.now() + 80; return r; };
}
const token = boundary.issue(binding);
if (scenario === 'changed-graph') await changeGraph();
console.log(JSON.stringify({ event: 'ready', token, policy, scenario }));
let ending = false;
process.on('SIGUSR2', async () => {
  if (ending) return; ending = true; let closeFailed = false;
  try { await boundary.close(); } catch { closeFailed = true; assert.equal(scenario, 'persist-failure'); }
  upstream.closeAllConnections(); backend.closeAllConnections();
  await new Promise<void>(resolve => upstream.close(() => resolve())); await new Promise<void>(resolve => backend.close(() => resolve()));
  const journal = gate.snapshot(), ids = [...new Set([...journal.reservations.map(r => r.requestId), ...(journal.decisions ?? []).map(d => d.requestId)])];
  const resources: Record<string, string | null> = {}; for (const key of ['memory.peak','memory.events','pids.peak']) { try { resources[key] = (await readFile('/sys/fs/cgroup/' + key, 'utf8')).trim(); } catch { resources[key] = null; } }
  await writeFile('/work/result.json', JSON.stringify({ resources, scenario, native, runtime: { node: process.version }, policy, sends, ingress, events, audit: boundary.audit, gateSnapshot: journal, journalOnDisk: JSON.parse(await readFile('/work/journal/state.json', 'utf8')), receipts: ids.map(id => a.receipt(id)), closeFailed, actualGlobalQuiescence: a.quiescent(ids.filter(id => a.receipt(id).known)) }));
  process.exit(0);
});
