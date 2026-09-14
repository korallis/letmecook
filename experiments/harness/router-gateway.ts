// Trusted issue-6 bootstrap: actual pinned router + shared authority/boundary.
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { readFile, writeFile, chmod, mkdir } from 'node:fs/promises';
import { existsSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { Boundary } from '../inference-boundary/boundary.ts';
import { PolicyGate } from '../inference-boundary/policy.ts';
import { hashDocument, type RouterPolicy } from '../inference-boundary/router-policy.ts';
import { routerBinding } from '../inference-boundary/router-fixture.ts';
import { OPENCODE_ROUTER_PROFILE, openCodeTools } from '../inference-boundary/profiles.ts';
import { RouterAuthority } from '../router-boundary-bridge/authority.ts';
assert(existsSync('/.dockerenv') && process.env.GAFFER_SYNTHETIC_OPENCODE === '1');
const scenario = process.argv[2];
const moduleAt = (path: string): Promise<any> => import(path);
const { handleChat } = await moduleAt('/router-source/src/sse/handlers/chat.js');
const { getAdapter } = await moduleAt('/router-source/src/lib/db/driver.js');
const repo = await moduleAt('/router-source/src/lib/db/index.js');
const { authority, project } = await moduleAt('/router-source/gaffer-extension/authority.mjs');
await getAdapter(); const a = authority();
const events: any[] = [], sends: any[] = [], ingress: any[] = [];
const emit = (event: string, detail: any = {}) => events.push({ event, at: Date.now(), ...detail });
let boundary: Boundary, gate: PolicyGate, count = 0;
const backend = createServer(async (req, res) => {
  const chunks = []; for await (const chunk of req) chunks.push(chunk);
  const body = JSON.parse(Buffer.concat(chunks).toString());
  const id = gate.snapshot().reservations.at(-1)!.requestId;
  sends.push({ number: ++count, requestId: id, path: req.url, body }); emit('original_send', { requestId: id });
  res.on('close', () => emit('original_closed', { requestId: id, ended: res.writableEnded }));
  if (['cancel', 'crash', 'tree', 'ambient-timeout'].includes(scenario)) return;
  if (scenario === 'fence') { setTimeout(() => a.fence(), 30); return; }
  if (scenario === 'router-error') { res.writeHead(503, { 'content-type': 'application/json' }).end(JSON.stringify({ error: { message: 'SYNTHETIC_ROUTER_SECRET_MUST_NOT_LEAK' } })); return; }
  const frame = (delta: unknown, finish_reason: string | null = null) => `data: ${JSON.stringify({ id: 'synthetic_original', object: 'chat.completion.chunk', model: body.model, choices: [{ index: 0, delta, finish_reason }] })}\n\n`;
  res.writeHead(200, { 'content-type': 'text/event-stream' });
  let text = frame({ role: 'assistant', content: '' });
  if (count === 1) {
    const path = scenario === 'forbidden' ? '/work/home/auth.json' : '/work/repo/greeting.txt';
    const name = scenario === 'write' ? 'write' : 'edit';
    text += frame({ tool_calls: [{ index: 0, id: 'call_original_edit_1', type: 'function', function: { name, arguments: `{"filePath":${JSON.stringify(path)},` } }] });
    if (scenario !== 'partial') text += frame({ tool_calls: [{ index: 0, function: { arguments: name === 'write' ? '"content":"hello from harness\\n"}' : '"oldString":"hello","newString":"hello from harness"}' } }] }) + frame({}, 'tool_calls') + 'data: [DONE]\n\n';
  } else text += frame({ content: 'Bounded synthetic edit complete.' }) + frame({}, 'stop') + 'data: [DONE]\n\n';
  const bytes = Buffer.from(text); for (let i = 0; i < bytes.length; i += 17) res.write(bytes.subarray(i, i + 17));
  emit('complete_looking_original_sent', { requestId: id });
  if (scenario === 'eof-delay') { setTimeout(() => { emit('original_eof_released', { requestId: id }); res.end(); }, 300); } else res.end();
});
await new Promise<void>(resolve => backend.listen(47771, '127.0.0.1', resolve));
const provider = 'openai-compatible-synthetic';
const config = { settings: { requireApiKey: true, rtkEnabled: false, headroomEnabled: false, pxpipeEnabled: false, cavemanEnabled: false, ponytailEnabled: false, ccFilterNaming: false, capacityAdapter: Object.fromEntries(['vision', 'pdf', 'audioInput', 'videoInput'].map(k => [k, { enabled: false, models: [] }])) },
  providerNodes: [{ id: provider, type: 'openai-compatible', name: 'synthetic', prefix: 'synthetic', baseUrl: 'http://127.0.0.1:47771/v1', apiType: 'chat' }],
  providerConnections: [{ id: 'account_1', provider, authType: 'apikey', name: 'synthetic', priority: 1, isActive: true, apiKey: 'synthetic_account_1', providerSpecificData: { baseUrl: 'http://127.0.0.1:47771/v1', apiType: 'chat' } }],
  apiKeys: [{ id: 'synthetic_ingress', key: 'synthetic_gateway_key', name: 'synthetic', isActive: true }],
  combos: [{ id: 'harness_coding', name: 'gaffer-harness-edit', models: [`${provider}/synthetic-model`] }] };
assert.equal(await a.replace(a.state().generation, 'policy_1', project(config), () => repo.importDb(config)), true);
const graph = a.snapshot(), state = a.state();
const shaFile = async (path: string) => createHash('sha256').update(await readFile(path)).digest('hex');
const overlayIdentity = Object.fromEntries(await Promise.all(['overlay/authority.mjs', 'overlay/opencode-profile.mjs', 'overlay/chat-terminal.mjs', 'overlay/responses-terminal.mjs', 'patches.mjs', 'loader.mjs'].map(async path => [path, await shaFile('/probe/' + path)])));
const p: RouterPolicy = { schema: 2, routerId: 'synthetic_router', routeId: 'harness_coding', revision: state.revision, epoch: 1, routerModel: 'gaffer-harness-edit', profile: OPENCODE_ROUTER_PROFILE, evidence: 'synthetic', liveAdmission: false, graph,
  limits: { requestBytes: 65536, responseBytes: 65536, outputTokens: 128, concurrency: 1, requestCount: 3, totalMs: 6000, firstOutputMs: 4000, idleMs: 2000, attemptMs: 60000 },
  authority: { deploymentId: 'synthetic_harness_bridge', boot: state.boot, generation: state.generation, revision: state.revision, graphDigest: hashDocument(graph) },
  envelope: { consumer: 'opencode-1.18.30-usage-disabled-edit-v1', cap: 'max_tokens-preserved-v1', replacement: 'read-only', sourceCommit: '17c4cc76877bd1755030a8414f8d0083f48dcccf', sourceLock: await shaFile('/probe/source-lock.json'), overlay: hashDocument(overlayIdentity), runtime: await shaFile('/probe/runtime-identity.json') } };
await writeFile('/work/approved.json', JSON.stringify(p));
const profileNegatives: any[] = [];
const visible = new Map<string, number>();
const control = { state: () => a.state(), snapshot: () => a.snapshot(), quiescent: (ids: string[]) => a.quiescent(ids), cancel: (id: string) => a.cancel(id), receipt: (id: string) => {
  const r = a.receipt(id);
  if (scenario === 'malformed-receipt' && r.known) return { ...r, id: 'mismatch' };
  if (scenario === 'unknown') return { id, known: false, quiescent: false };
  if (['delay', 'receipt-stop'].includes(scenario) && r.quiescent) {
    if (!visible.has(id)) { visible.set(id, Date.now() + 300); emit('receipt_visibility_delayed', { requestId: id }); if (scenario === 'receipt-stop') setTimeout(() => boundary.revoke('attempt_1'), 30); }
    if (Date.now() < visible.get(id)!) return { id, known: false, quiescent: false };
    if (!events.some(e => e.event === 'receipt_visible' && e.requestId === id)) emit('receipt_visible', { requestId: id });
  }
  return r;
} };
const bridge = new RouterAuthority(control, p);
gate = await PolicyGate.open('/work/journal', bridge, 750); await gate.activate();
const finalize = gate.finalize.bind(gate);
gate.finalize = async (...args) => {
  if (scenario === 'persist-failure') { const owner = JSON.parse(await readFile('/work/journal/owner.lock', 'utf8')).owner; await mkdir(`/work/journal/state.${owner}.next`); }
  if (scenario === 'persist-after-rename') (gate as any).syncDirectory = async () => { throw new Error('synthetic_directory_sync_failure'); };
  await finalize(...args); emit('decision_persisted', { requestId: args[0] });
  if (scenario === 'post-decision-cancel') a.cancel(args[0]);
};
const upstream = createServer(async (req, res) => {
  const abort = new AbortController(); res.on('close', () => { if (!res.writableEnded) abort.abort(); });
  const chunks: Buffer[] = []; for await (const chunk of req) chunks.push(chunk);
  const body = Buffer.concat(chunks).toString();
  ingress.push({ requestId: req.headers['x-gaffer-request-id'], body: JSON.parse(body), headerNames: Object.keys(req.headers), host: req.headers.host });
  try {
    const response = await handleChat(new Request('http://127.0.0.1/v1/chat/completions', { method: 'POST', headers: req.headers as HeadersInit, body, signal: abort.signal }));
    res.writeHead(response.status, Object.fromEntries(response.headers));
    if (response.body) for await (const chunk of response.body) res.write(chunk); res.end();
  } catch { res.destroy(); }
});
upstream.listen('/work/router.sock'); await once(upstream, 'listening'); await chmod('/work/router.sock', 0o600);
boundary = new Boundary(gate, '/work/router.sock', scenario === 'zero-auth' ? 'synthetic_invalid_key' : 'synthetic_gateway_key'); await boundary.listen('/router/inference.sock');
const binding = routerBinding(p); binding.expiresAt = binding.leaseExpiresAt = Date.now() + 60000;
const token = boundary.issue(binding);
if (scenario === 'edit') {
  const base: any = { model: p.routerModel, stream: true, max_tokens: 128, tool_choice: 'auto', tools: openCodeTools(), messages: [{ role: 'user', content: 'Synthetic profile regression' }] };
  const call = { id: 'call_history', type: 'function', function: { name: 'edit', arguments: '{"filePath":"/work/repo/greeting.txt","oldString":"hello","newString":"hello from harness"}' } };
  const history = [...base.messages, { role: 'assistant', content: '', tool_calls: [call] }, { role: 'tool', tool_call_id: call.id, content: 'Synthetic result' }];
  const cases: [string, (body: any) => void][] = [
    ['required_choice', b => b.tool_choice = 'required'], ['none_choice', b => b.tool_choice = 'none'], ['missing_choice', b => delete b.tool_choice],
    ['usage_option', b => b.stream_options = { include_usage: true }], ['effort', b => b.reasoning_effort = 'xhigh'], ['temperature', b => b.temperature = 0],
    ['strict_true', b => b.tools[0].function.strict = true], ['strict_false', b => b.tools[0].function.strict = false],
    ['schema_mutation', b => b.tools[0].function.parameters.properties.filePath.enum = ['/work/repo/greeting.txt']],
    ['empty_tools', b => b.tools = []], ['missing_tools', b => delete b.tools], ['extra_cap', b => b.max_output_tokens = 128],
    ['empty_assistant', b => b.messages.push({ role: 'assistant', content: '' })], ['unmatched_call', b => b.messages = history.slice(0, -1)],
    ['orphan_result', b => { b.messages = structuredClone(history); b.messages.at(-1).tool_call_id = 'call_other'; }],
    ['forbidden_history', b => { b.messages = structuredClone(history); b.messages[1].tool_calls[0].function.arguments = '{"filePath":"/work/home/auth.json","oldString":"hello","newString":"bad"}'; }],
    ['duplicate_argument_keys', b => { b.messages = structuredClone(history); b.messages[1].tool_calls[0].function.arguments = '{"filePath":"/work/repo/greeting.txt","oldString":"a","oldString":"b","newString":"c"}'; }],
  ];
  for (const [name, change] of cases) {
    const body = structuredClone(base); change(body); const id = 'negative_' + name;
    const response = await handleChat(new Request('http://127.0.0.1/v1/chat/completions', { method: 'POST', headers: { 'content-type': 'application/json', authorization: 'Bearer synthetic_gateway_key', 'x-gaffer-request-id': id, 'x-gaffer-generation': String(state.generation), 'x-gaffer-revision': state.revision }, body: JSON.stringify(body) }));
    await response.text(); assert.equal(response.status, 503); assert.equal(sends.length, 0); assert.equal(a.receipt(id).known, false);
    profileNegatives.push({ name, status: response.status, originalSends: 0, receiptKnown: false });
  }
}

if (scenario === 'changed-graph') { const next = structuredClone(config); next.combos[0].models.push(`${provider}/fallback-model`); assert.equal(await a.replace(a.state().generation, 'policy_2', project(next), () => repo.importDb(next)), true); emit('approved_graph_changed', { graphDigest: hashDocument(a.snapshot()), generation: a.state().generation }); }
console.log(JSON.stringify({ event: 'ready', token, binding, policy: p, tools: openCodeTools() }));
let stopping = false;
process.on('SIGUSR2', async () => {
  if (stopping) return; stopping = true; let closeFailed = false;
  try { await boundary.close(); } catch { closeFailed = true; }
  upstream.closeAllConnections(); backend.closeAllConnections(); upstream.close(); backend.close();
  const journal = gate.snapshot(), ids = [...new Set([...journal.reservations.map(r => r.requestId), ...(journal.decisions ?? []).map(d => d.requestId)])];
  const resources: Record<string, string | null> = {};
  for (const k of ['memory.peak', 'memory.current', 'memory.events', 'pids.peak', 'cpu.stat']) { try { resources[k] = (await readFile('/sys/fs/cgroup/' + k, 'utf8')).trim(); } catch { resources[k] = null; } }
  const result = { scenario, runtime: { node: process.version, arch: process.arch }, policy: p, overlayIdentity, profileNegatives, ingress, sends, events, audit: boundary.audit, gateSnapshot: journal, journalOnDisk: JSON.parse(await readFile('/work/journal/state.json', 'utf8')), receipts: ids.map(id => a.receipt(id)), closeFailed, actualGlobalQuiescence: a.quiescent(ids.filter(id => a.receipt(id).known)), usage: 'unknown', resources };
  await writeFile('/work/result.json', JSON.stringify(result)); process.exit(0);
});
