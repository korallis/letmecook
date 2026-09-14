import { test } from 'node:test';
import assert from 'node:assert/strict';
import { request } from 'node:http';
import { readFile, rm, mkdtemp, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { spawnSync } from 'node:child_process';
import { setup, SYNTHETIC_SECRET, textStream, toolStream, event } from './fixture.ts';
import { CHAT_PATH, TOOL, fixturePolicy } from './types.ts';
import { ChatStream } from './protocol.ts';
import { parseJSON } from './json.ts';
import { PolicyGate } from './policy.ts';

const pause = (ms: number) => new Promise(resolve => setTimeout(resolve, ms));
async function until(check: () => boolean, ms = 1500) {
  const end = Date.now() + ms;
  while (!check()) { if (Date.now() > end) throw new Error('observation_timeout'); await pause(5); }
}
const body = (tools = false, model = 'gaffer-coding') => ({ model, stream: true, max_completion_tokens: 32, messages: [{ role: 'user', content: 'Read fixture.txt' }], ...(tools ? { tools: [TOOL], tool_choice: 'required' } : {}) });
function start(socket: string, token: string | undefined, options: { path?: string; method?: string; body?: unknown; headers?: Record<string, string> } = {}) {
  const raw = typeof options.body === 'string' ? options.body : JSON.stringify(options.body ?? body());
  let req: ReturnType<typeof request>;
  const result = new Promise<{ status: number; text: string; headers: Record<string, unknown> }>((resolve, reject) => {
    req = request({ socketPath: socket, path: options.path ?? CHAT_PATH, method: options.method ?? 'POST', agent: false, headers: {
      host: 'localhost', 'content-type': 'application/json', 'content-length': Buffer.byteLength(raw), ...(token ? { authorization: `Bearer ${token}` } : {}), ...options.headers,
    } }, res => {
      let text = ''; res.setEncoding('utf8'); res.on('data', chunk => text += chunk);
      res.on('end', () => resolve({ status: res.statusCode!, text, headers: res.headers })); res.on('error', reject);
    });
    req.on('error', reject); req.end(raw);
  });
  return { result, cancel: () => req!.destroy() };
}
const call = (socket: string, token: string | undefined, options: Parameters<typeof start>[2] = {}) => start(socket, token, options).result;

test('strict JSON rejects duplicate/escaped duplicate keys, nonfinite numbers and depth abuse', () => {
  for (const raw of ['{"model":1,"model":2}', '{"model":1,"mo\\u0064el":2}', '{"x":{"a":1,"a":2}}', '{"x":1e999}', '['.repeat(40) + '0' + ']'.repeat(40), '{"__proto__":1}', '{"x":1} trailing']) assert.throws(() => parseJSON(raw));
  assert.equal((parseJSON('{"x":"a\\\"b"}') as any).x, 'a"b');
});

test('worker/planner calls stream through one authenticated route with safe attribution', async () => {
  const s = await setup();
  try {
    for (const role of ['worker', 'planner', 'reviewer'] as const) {
      const g = s.grant({ role }); const response = await call(s.workerSocket, g.token);
      assert.equal(response.status, 200); assert.match(response.text, /fixture café/); assert.match(response.text, /\[DONE\]/);
      assert.doesNotMatch(JSON.stringify(response), new RegExp(SYNTHETIC_SECRET));
      assert(!JSON.stringify(response).includes(s.router.key)); assert(!JSON.stringify(response).includes(g.token));
    }
    await until(() => s.boundary.audit.length === 3);
    assert.equal(s.router.calls.length, 3);
    assert(s.router.calls.every(c => c.authenticated && c.model === 'gaffer-coding'));
    assert(s.boundary.audit.every(a => a.outcome === 'completed' && a.providerAttribution === 'unavailable'));
    assert.equal(s.gate.snapshot().reservations.length, 0);
  } finally { await s.close(); }
});

test('missing, wrong, expired and lease-expired tokens never reach the router', async () => {
  const s = await setup();
  try {
    const expired = s.grant({ expiresAt: Date.now() - 1 });
    const lease = s.grant({ leaseExpiresAt: Date.now() - 1 });
    for (const token of [undefined, 'f'.repeat(64), expired.token, lease.token]) assert.equal((await call(s.workerSocket, token)).status, 401);
    const revoked = s.grant(); s.boundary.revoke(revoked.binding.attemptId); assert.equal((await call(s.workerSocket, revoked.token)).status, 401);
    const fenced = s.grant(); s.boundary.replaceFence(fenced.binding.attemptId); assert.equal((await call(s.workerSocket, fenced.token)).status, 401);
    assert.equal(s.router.calls.length, 0);
  } finally { await s.close(); }
});

test('noncanonical paths, other protocols, management and methods are denied', async () => {
  const s = await setup();
  try {
    const { token } = s.grant();
    for (const path of ['/api/providers', '/api/combos', '/v1/models', '/api/v1/chat/completions', '/v1/v1/chat/completions', '/codex/responses', '/responses', '/v1/responses', '/v1/messages', CHAT_PATH + '?model=other', '/v1/../api/providers', '/v1/%63hat/completions', 'http://localhost' + CHAT_PATH]) assert.equal((await call(s.workerSocket, token, { path })).status, 404);
    for (const method of ['GET', 'PUT', 'DELETE', 'OPTIONS']) assert.equal((await call(s.workerSocket, token, { method })).status, 404);
    assert.equal(s.router.calls.length, 0);
  } finally { await s.close(); }
});

test('route identity, model, strict-schema, media, account and header overrides are denied', async () => {
  const s = await setup();
  try {
    const { token } = s.grant();
    assert.equal((await call(s.workerSocket, token, { body: { ...body(), model: 'unapproved' } })).status, 403);
    for (const extra of [{ provider: 'paid' }, { response_format: { type: 'json_schema' } }, { reasoning_effort: 'high' }, { tools: [{ type: 'web_search' }] }, { messages: [{ role: 'user', content: [{ type: 'image_url', image_url: { url: 'https://unapproved.invalid' } }] }] }]) assert.equal((await call(s.workerSocket, token, { body: { ...body(), ...extra } })).status, 400);
    for (const headers of [{ 'x-api-key': SYNTHETIC_SECRET }, { cookie: SYNTHETIC_SECRET }, { host: 'unapproved.invalid' }, { 'x-forwarded-host': 'unapproved.invalid' }, { 'content-encoding': 'gzip' }] as Record<string, string>[]) assert.equal((await call(s.workerSocket, token, { headers })).status, 400);
    for (const override of [{ routeId: 'other' }, { routerId: 'other' }, { epoch: 2 }, { revision: 'other' }]) assert.equal((await call(s.workerSocket, s.grant(override).token)).status, 403);
    assert.equal(s.router.calls.length, 0);
  } finally { await s.close(); }
});

test('request bytes, output tokens, duplicate keys and unsupported fields reject before forwarding', async () => {
  const s = await setup(fixturePolicy({ requestBytes: 512 }));
  try {
    const { token } = s.grant();
    assert.equal((await call(s.workerSocket, token, { body: 'x'.repeat(513) })).status, 413);
    for (const max of [undefined, null, 0, -1, 257, 1.5]) assert.equal((await call(s.workerSocket, token, { body: { ...body(), max_completion_tokens: max } })).status, max === undefined ? 400 : 422);
    assert.equal((await call(s.workerSocket, token, { body: '{"model":"bad","model":"gaffer-coding"}' })).status, 400);
    assert.equal((await call(s.workerSocket, token, { body: body(), headers: { 'content-type': 'text/plain' } })).status, 415);
    assert.equal(s.router.calls.length, 0);
  } finally { await s.close(); }
});

test('complete tools are withheld until valid terminal/EOF and support tool-result continuation', async () => {
  const s = await setup();
  try {
    s.router.enqueue('tools'); const { token } = s.grant();
    const result = await call(s.workerSocket, token, { body: body(true) });
    assert.equal(result.status, 200);
    const packets = result.text.split('\n').filter(l => l.startsWith('data: {')).map(l => JSON.parse(l.slice(6)));
    const tool = packets.find(p => p.choices[0].delta.tool_calls).choices[0].delta.tool_calls[0];
    assert.deepEqual(JSON.parse(tool.function.arguments), { path: 'fixture.txt' });
    assert(tool.id.startsWith('call_')); assert(!tool.id.includes('fixture_1'));
    await until(() => s.boundary.audit.length === 1);
    const next = { ...body(), messages: [...body().messages, { role: 'assistant', content: null, tool_calls: [{ id: tool.id, type: 'function', function: tool.function }] }, { role: 'tool', tool_call_id: tool.id, content: 'fixture content' }] };
    assert.equal((await call(s.workerSocket, token, { body: next })).status, 200);
    assert.equal(s.router.calls.length, 2); assert.equal(s.router.calls[1].body.messages[2].tool_call_id, tool.id);
  } finally { await s.close(); }
});

test('each UTF-8/SSE split boundary preserves text/tools and refuses trailing data or false DONE', () => {
  for (const stream of [textStream(), toolStream()]) {
    const bytes = Buffer.from(stream);
    for (let split = 0; split <= bytes.length; split++) {
      const parser = new ChatStream('request_local', 'gaffer-coding', true);
      const a = parser.push(bytes.subarray(0, split)); const b = parser.push(bytes.subarray(split));
      assert(!a.error && !b.error); const output = a.output.concat(b.output, parser.end()).join('');
      assert.match(output, /\[DONE\]/); assert(!output.includes(SYNTHETIC_SECRET));
    }
  }
  for (const stream of ['data: [DONE]\n\n', toolStream().split('data: [DONE]')[0], toolStream() + event({ content: 'late' }), event({ content: 'x' }, 'length') + 'data: [DONE]\n\n']) {
    const parser = new ChatStream('request_local', 'gaffer-coding', true); const result = parser.push(Buffer.from(stream));
    assert(result.error || assert.throws(() => parser.end()) === undefined);
    assert(!result.output.join('').includes('tool_calls'));
  }
});

test('partial tool and error streams never emit executable tools or raw errors', async () => {
  const s = await setup();
  try {
    for (const mode of ['partial_tool', 'error_after', 'error', 'redirect'] as const) {
      s.router.enqueue(mode); const g = s.grant(); const result = await call(s.workerSocket, g.token, { body: body(true) });
      assert(!result.text.includes(SYNTHETIC_SECRET)); assert(!result.text.includes('"tool_calls"')); assert(!result.text.includes('[DONE]'));
    }
    await until(() => s.boundary.audit.length === 4);
    assert.equal(s.router.calls.length, 4); assert(s.boundary.audit.some(a => a.outcome === 'partial_failure'));
    assert(!JSON.stringify(s.boundary.audit).includes(SYNTHETIC_SECRET));
  } finally { await s.close(); }
});

test('response byte limits abort without forwarding an oversized chunk', async () => {
  const s = await setup(fixturePolicy({ responseBytes: 1024 }));
  try { s.router.enqueue('large'); const result = await call(s.workerSocket, s.grant().token); assert(result.text.length < 1024); assert.match(result.text, /response_limit/); }
  finally { await s.close(); }
});

test('request count and concurrency reservations bound each attempt with no router retries', async () => {
  const s = await setup(fixturePolicy({ requestCount: 1 }));
  try {
    const g = s.grant(); s.router.enqueue('hold'); const first = call(s.workerSocket, g.token);
    await until(() => s.router.calls.length === 1);
    assert.equal((await call(s.workerSocket, g.token)).status, 429);
    s.router.completeHeld(); assert.equal((await first).status, 200); await until(() => s.boundary.audit.length === 1);
    assert.equal((await call(s.workerSocket, g.token)).status, 429); assert.equal(s.router.calls.length, 1);
  } finally { await s.close(); }
});

test('heartbeats do not extend first-output or semantic-idle deadlines', async () => {
  const s = await setup(fixturePolicy({ firstOutputMs: 80, idleMs: 60, totalMs: 1000 }));
  try {
    for (const mode of ['heartbeat', 'idle'] as const) {
      s.router.enqueue(mode); const began = Date.now(); const result = await call(s.workerSocket, s.grant().token);
      assert.match(result.text, /deadline/); assert(Date.now() - began < 700);
    }
    await until(() => s.boundary.audit.length === 2); assert(s.boundary.audit.every(a => a.outcome === 'cancelled_unknown'));
  } finally { await s.close(); }
});

test('total deadline and attempt-wide expiry terminate active transport', async () => {
  const s = await setup(fixturePolicy({ totalMs: 70, firstOutputMs: 500, attemptMs: 110 }));
  try {
    const g = s.grant(); s.router.enqueue('hold'); const result = await call(s.workerSocket, g.token); assert.match(result.text, /deadline/);
    await pause(50); assert.equal((await call(s.workerSocket, g.token)).status, 401);
    assert.equal(s.router.calls.length, 1);
  } finally { await s.close(); }
});

test('revocation during durable admission forwards zero requests', async () => {
  const s = await setup();
  try {
    const g = s.grant(); const gate = s.gate as any; const original = gate.persist.bind(gate);
    let entered = false; let release!: () => void;
    const held = new Promise<void>(resolve => release = resolve);
    gate.persist = async () => { entered = true; await held; await original(); };
    const pending = call(s.workerSocket, g.token); await until(() => entered);
    s.boundary.revoke(g.binding.attemptId); release();
    assert.equal((await pending).status, 409); await until(() => s.boundary.audit.length === 1);
    assert.equal(s.router.calls.length, 0); assert.equal(s.gate.snapshot().reservations.length, 0);
    gate.persist = original;
  } finally { await s.close(); }
});

test('client cancellation and stop abort upstream but hidden work keeps the drain blocked', async () => {
  const s = await setup();
  try {
    const g = s.grant(); s.router.enqueue('hidden_cancel'); const pending = start(s.workerSocket, g.token); void pending.result.catch(() => {});
    await until(() => s.router.calls.length === 1); pending.cancel();
    await until(() => s.boundary.audit.length === 1 && s.router.active.size === 0);
    assert.equal(s.boundary.audit[0].outcome, 'cancelled_unknown'); assert.equal(s.gate.snapshot().reservations.length, 1);
    const next = { ...fixturePolicy(), epoch: 2, revision: 'policy_2', routerModel: 'new-coding' };
    assert.equal(await s.gate.drainAndReplace(next), false); assert.equal(s.router.mutations, 0);
    assert.equal((await call(s.workerSocket, g.token)).status, 503);
    s.router.stopHidden(); assert.equal(await s.gate.drainAndReplace(next), true); assert.equal(s.router.mutations, 1);
    assert.equal((await call(s.workerSocket, g.token)).status, 403);
  } finally { await s.close(); }
});

test('freeze/drain serializes active requests, blocks admission, increments epoch and needs fresh grants', async () => {
  const s = await setup();
  try {
    const g = s.grant(); s.router.enqueue('hold'); const active = call(s.workerSocket, g.token); await until(() => s.router.calls.length === 1);
    const next = { ...fixturePolicy(), epoch: 2, revision: 'policy_2', routerModel: 'new-coding' };
    assert.equal(await s.gate.drainAndReplace(next), false); assert.equal(s.router.mutations, 0);
    assert.equal((await call(s.workerSocket, s.grant().token)).status, 503);
    s.router.completeHeld(); await active; await until(() => s.boundary.audit.length === 1);
    assert.equal(await s.gate.drainAndReplace(next), true);
    assert.equal((await call(s.workerSocket, g.token)).status, 403);
    const fresh = s.grant({ epoch: 2, revision: 'policy_2' }); assert.equal((await call(s.workerSocket, fresh.token, { body: body(false, 'new-coding') })).status, 200);
    assert.deepEqual(s.router.calls.map(c => [c.model, c.revision]), [['gaffer-coding', 'policy_1'], ['new-coding', 'policy_2']]);
  } finally { await s.close(); }
});

test('failed policy readback stays closed and unsupported graph widening is rejected', async () => {
  const s = await setup();
  try {
    const next = { ...fixturePolicy(), epoch: 2, revision: 'policy_2' };
    await assert.rejects(s.gate.drainAndReplace({ ...next, graph: { ...next.graph, fusion: true } } as any));
    s.router.failReadback = true; await assert.rejects(s.gate.drainAndReplace(next));
    assert.equal(s.gate.snapshot().phase, 'closed'); assert.equal((await call(s.workerSocket, s.grant().token)).status, 503);
  } finally { await s.close(); }
});

test('journal persists uncertainty, rejects a second writer and restart requires quiescence', async () => {
  const s = await setup(); let reopened: PolicyGate | undefined;
  try {
    await assert.rejects(PolicyGate.open(join(s.directory, 'policy'), s.router), /EEXIST/);
    s.router.enqueue('hidden_cancel'); const g = s.grant(); const pending = call(s.workerSocket, g.token);
    await until(() => s.router.calls.length === 1); s.boundary.revoke(g.binding.attemptId); await pending;
    await until(() => s.boundary.audit.length === 1);
    const persisted = await readFile(join(s.directory, 'policy/state.json'), 'utf8');
    assert(!persisted.includes(g.token)); assert(!persisted.includes(s.router.key)); assert.match(persisted, /cancelled_unknown/);
    await s.boundary.close();
    reopened = await PolicyGate.open(join(s.directory, 'policy'), s.router);
    assert.equal(reopened.snapshot().phase, 'closed'); await assert.rejects(reopened.activate());
    s.router.stopHidden(); await until(() => s.router.active.size === 0); await reopened.activate();
    await reopened.close(); reopened = undefined;
  } finally { if (reopened) await reopened.close(); await s.router.close(); await rm(s.directory, { recursive: true, force: true }); }
});

test('accepted Chat corpus fixtures distinguish complete tools, partial output and synthetic DONE', async () => {
  const fixtures = [['chat-tool-complete.sse', true], ['chat-tool-partial.sse', false], ['chat-synthetic-done.sse', false], ['chat-error-after-text.sse', false]] as const;
  for (const [name, completed] of fixtures) {
    const stream = await readFile(new URL(`../../tests/fixtures/9router/streams/${name}`, import.meta.url));
    const parser = new ChatStream('request_fixture', 'gaffer-coding', true);
    const parsed = parser.push(stream);
    if (completed) { assert(!parsed.error); assert.match(parser.end().join(''), /tool_calls/); }
    else { assert(parsed.error || assert.throws(() => parser.end()) === undefined); assert(!parsed.output.join('').includes('tool_calls')); }
    assert(!parsed.output.join('').includes('SENTINEL'));
  }
});

test('multiple tool IDs are assembled independently and duplicate/invalid calls never complete', () => {
  const first = event({ tool_calls: [0, 1].map(index => ({ index, id: `call_fixture_${index}`, type: 'function', function: { name: 'read_file', arguments: '{"path":' } })) });
  const last = event({ tool_calls: [1, 0].map(index => ({ index, function: { arguments: '"fixture.txt"}' } })) }) + event({}, 'tool_calls') + 'data: [DONE]\n\n';
  const parser = new ChatStream('request_fixture', 'gaffer-coding', true);
  assert(!parser.push(Buffer.from(first + last)).error);
  const output = parser.end().join(''); assert.match(output, /call_request_fixture_0/); assert.match(output, /call_request_fixture_1/);
  for (const stream of [(first + last).replace('call_fixture_1', 'call_fixture_0'), (first + last).replaceAll('fixture.txt', 'host-secret'), (first + last).replaceAll('read_file', 'web_search')]) {
    const bad = new ChatStream('request_fixture', 'gaffer-coding', true);
    assert(bad.push(Buffer.from(stream)).error || assert.throws(() => bad.end()) === undefined);
  }
});

test('stop and lease expiry cancel midstream without a successful terminal', async () => {
  const s = await setup(fixturePolicy({ firstOutputMs: 500, idleMs: 500, totalMs: 1000 }));
  try {
    s.router.enqueue('idle'); const stopped = s.grant(); const pending = call(s.workerSocket, stopped.token);
    await until(() => s.router.calls.length === 1); s.boundary.revoke(stopped.binding.attemptId);
    const result = await pending; assert.match(result.text, /cancelled/); assert(!result.text.includes('[DONE]'));
    s.router.enqueue('idle'); const expired = s.grant({ leaseExpiresAt: Date.now() + 70 });
    const expiry = await call(s.workerSocket, expired.token); assert.match(expiry.text, /deadline/); assert(!expiry.text.includes('[DONE]'));
    await until(() => s.boundary.audit.length === 2 && s.router.active.size === 0);
    assert(s.boundary.audit.every(a => a.outcome === 'cancelled_unknown'));
  } finally { await s.close(); }
});

test('abrupt supervisor exit leaves a lock and durable reservation; offline recovery remains closed', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'gaffer-crash-proof-'));
  let gate: PolicyGate | undefined;
  try {
    const policyUrl = new URL('./policy.ts', import.meta.url).href;
    const typesUrl = new URL('./types.ts', import.meta.url).href;
    const code = `import {PolicyGate} from ${JSON.stringify(policyUrl)}; import {fixturePolicy} from ${JSON.stringify(typesUrl)};
      const authority={readFrozenPolicy:async()=>fixturePolicy(),quiescent:async()=>true,replaceFrozenPolicy:async p=>p};
      const gate=await PolicyGate.open(${JSON.stringify(directory)},authority); await gate.activate();
      await gate.admit({attemptId:'attempt_crash',taskId:'task_crash',grantId:'grant_crash',leaseId:'lease_crash',fence:1,role:'worker',routerId:'fixture_router',routeId:'coding',epoch:1,revision:'policy_1',expiresAt:Date.now()+10000,leaseExpiresAt:Date.now()+10000},()=>true);
      process.exit(17);`;
    const child = spawnSync(process.execPath, ['--input-type=module', '-e', code], { timeout: 3000, encoding: 'utf8' });
    assert.equal(child.status, 17);
    const authority = { readFrozenPolicy: async () => fixturePolicy(), quiescent: async () => false, replaceFrozenPolicy: async (p: ReturnType<typeof fixturePolicy>) => p };
    await assert.rejects(PolicyGate.open(directory, authority), /EEXIST/);
    // This is a task-owned crash fixture: process exit was observed above. Production recovery is an operator action.
    await rm(join(directory, 'owner.lock'));
    gate = await PolicyGate.open(directory, authority);
    assert.equal(gate.snapshot().reservations.length, 1); assert.equal(gate.snapshot().phase, 'closed');
    await assert.rejects(gate.activate()); await gate.close(); gate = undefined;
    await writeFile(join(directory, 'state.json'), '{"schema":1,"counts":null}');
    await assert.rejects(PolicyGate.open(directory, authority), /boundary_closed/);
  } finally { if (gate) await gate.close(); await rm(directory, { recursive: true, force: true }); }
});

test('OpenCode-style max_tokens/stream_options are denied by this explicitly narrower profile', async () => {
  const s = await setup();
  try {
    const { max_completion_tokens, ...request } = body();
    const result = await call(s.workerSocket, s.grant().token, { body: { ...request, max_tokens: max_completion_tokens, stream_options: { include_usage: true } } });
    assert.equal(result.status, 400); assert.equal(s.router.calls.length, 0);
  } finally { await s.close(); }
});

test('transport loss before headers retains unknown work and does not retry or refund the reservation', async () => {
  const s = await setup();
  try {
    const g = s.grant(); s.router.enqueue('hidden_drop');
    const result = await call(s.workerSocket, g.token); assert.equal(result.status, 502); assert.match(result.text, /upstream_failure/);
    await until(() => s.boundary.audit.length === 1);
    assert.equal(s.boundary.audit[0].quiescence, 'unknown'); assert.equal(s.gate.snapshot().reservations.length, 1);
    assert.equal((await call(s.workerSocket, g.token)).status, 429); assert.equal(s.router.calls.length, 1);
  } finally { await s.close(); }
});

test('closed owners are terminal/idempotent and cannot modify or unlock a newer owner', async () => {
  const s = await setup(); let newer: PolicyGate | undefined;
  try {
    const old = s.gate; const binding = s.grant().binding;
    await s.boundary.close();
    newer = await PolicyGate.open(join(s.directory, 'policy'), s.router); await newer.activate();
    const journal = await readFile(join(s.directory, 'policy/state.json'), 'utf8');
    const owner = await readFile(join(s.directory, 'policy/owner.lock'), 'utf8');
    await Promise.all([old.close(), old.close()]);
    await assert.rejects(old.activate(), /boundary_closed/);
    await assert.rejects(old.admit(binding, () => true), /boundary_closed/);
    await assert.rejects(old.finish('not-a-request', 'completed'), /boundary_closed/);
    await assert.rejects(old.drainAndReplace({ ...fixturePolicy(), epoch: 2, revision: 'policy_2' }), /boundary_closed/);
    assert.equal(await readFile(join(s.directory, 'policy/state.json'), 'utf8'), journal);
    assert.equal(await readFile(join(s.directory, 'policy/owner.lock'), 'utf8'), owner);
    assert.equal(newer.snapshot().phase, 'active');
    await assert.rejects(PolicyGate.open(join(s.directory, 'policy'), s.router), /EEXIST/);
    await newer.close(); newer = undefined;
  } finally { if (newer) await newer.close(); await s.close(); }
});

test('an owner whose lock identity changed cannot write the journal or remove that lock', async () => {
  const s = await setup();
  try {
    const before = await readFile(join(s.directory, 'policy/state.json'), 'utf8');
    const replacement = JSON.stringify({ pid: process.pid, owner: 'replacement-owner' });
    await writeFile(join(s.directory, 'policy/owner.lock'), replacement);
    await assert.rejects(s.gate.admit(s.grant().binding, () => true), /boundary_closed/);
    await assert.rejects(s.boundary.close(), /boundary_closed/);
    assert.equal(await readFile(join(s.directory, 'policy/state.json'), 'utf8'), before);
    assert.equal(await readFile(join(s.directory, 'policy/owner.lock'), 'utf8'), replacement);
  } finally { await s.boundary.close().catch(() => {}); await s.router.close(); await rm(s.directory, { recursive: true, force: true }); }
});

test('reserved JavaScript property IDs have numeric own counters and remain bounded after reopening', async () => {
  const s = await setup(fixturePolicy({ requestCount: 1 })); let reopened: PolicyGate | undefined;
  try {
    const bindings = [];
    for (const attemptId of ['constructor', 'prototype', 'tostring', 'hasownproperty']) {
      const g = s.grant({ attemptId }); bindings.push(g.binding);
      assert.equal((await call(s.workerSocket, g.token)).status, 200);
      await until(() => s.boundary.audit.length === bindings.length);
      assert.equal((await call(s.workerSocket, g.token)).status, 429);
      const counts = s.gate.snapshot().counts; assert(Object.hasOwn(counts, attemptId)); assert.equal(counts[attemptId], 1);
    }
    assert.throws(() => s.grant({ attemptId: '__proto__' }));
    assert.equal(s.router.calls.length, bindings.length);
    await s.boundary.close();
    reopened = await PolicyGate.open(join(s.directory, 'policy'), s.router); await reopened.activate();
    for (const binding of bindings) await assert.rejects(reopened.admit(binding, () => true), /attempt_limit/);
    const journal = JSON.parse(await readFile(join(s.directory, 'policy/state.json'), 'utf8'));
    assert(Object.values(journal.counts).every(count => count === 1));
    await reopened.close(); reopened = undefined;
  } finally { if (reopened) await reopened.close(); await s.close(); }
});

test('journal reservations require a matching own request counter', async () => {
  const s = await setup();
  try {
    const g = s.grant({ attemptId: 'constructor' }); s.router.enqueue('hidden_drop');
    await call(s.workerSocket, g.token); await until(() => s.boundary.audit.length === 1);
    await s.boundary.close();
    const path = join(s.directory, 'policy/state.json'); const journal = JSON.parse(await readFile(path, 'utf8'));
    journal.counts = {}; await writeFile(path, JSON.stringify(journal));
    await assert.rejects(PolicyGate.open(join(s.directory, 'policy'), s.router), /boundary_closed/);
  } finally { await s.close(); }
});

test('stalled admission honors the caller deadline and its late read cannot change a newer owner', async () => {
  const s = await setup(fixturePolicy({ totalMs: 60 })); let newer: PolicyGate | undefined;
  let release!: (value: boolean) => void;
  try {
    const g = s.grant(); s.router.enqueue('hidden_drop'); await call(s.workerSocket, g.token);
    await until(() => s.boundary.audit.length === 1);
    const original = s.router.quiescent.bind(s.router);
    let entered = false; const held = new Promise<boolean>(resolve => release = resolve);
    s.router.quiescent = async () => { entered = true; return held; };
    const began = Date.now(); const pending = call(s.workerSocket, g.token);
    await until(() => entered);
    const result = await Promise.race([pending, pause(220).then(() => { throw new Error('admission_deadline_overrun'); })]);
    assert.equal(result.status, 408); assert(Date.now() - began < 220); assert.equal(s.router.calls.length, 1);
    await s.boundary.close();
    const oldState = JSON.parse(await readFile(join(s.directory, 'policy/state.json'), 'utf8'));
    assert.equal(oldState.phase, 'closed'); assert.equal(oldState.reservations.length, 1);
    s.router.quiescent = original; s.router.stopHidden();
    newer = await PolicyGate.open(join(s.directory, 'policy'), s.router); await newer.activate();
    const journal = await readFile(join(s.directory, 'policy/state.json'), 'utf8');
    const owner = await readFile(join(s.directory, 'policy/owner.lock'), 'utf8');
    release(true); await pause(30);
    assert.equal(await readFile(join(s.directory, 'policy/state.json'), 'utf8'), journal);
    assert.equal(await readFile(join(s.directory, 'policy/owner.lock'), 'utf8'), owner);
    assert.equal(s.router.calls.length, 1); await assert.rejects(s.gate.activate(), /boundary_closed/);
    await assert.rejects(PolicyGate.open(join(s.directory, 'policy'), s.router), /EEXIST/);
    await newer.close(); newer = undefined;
  } finally { release?.(false); if (newer) await newer.close(); await s.close(); }
});

test('stop and shutdown interrupt a stalled completion authority without losing unknown reservations', async () => {
  const s = await setup(); let release!: (value: boolean) => void;
  try {
    let entered = false; const held = new Promise<boolean>(resolve => release = resolve);
    s.router.quiescent = async () => { entered = true; return held; };
    const result = await call(s.workerSocket, s.grant().token); assert.equal(result.status, 200);
    await until(() => entered); const began = Date.now();
    await Promise.race([s.boundary.close(), pause(150).then(() => { throw new Error('shutdown_authority_wait'); })]);
    assert(Date.now() - began < 150);
    const journal = await readFile(join(s.directory, 'policy/state.json'), 'utf8');
    assert.equal(JSON.parse(journal).reservations.length, 1); assert.equal(JSON.parse(journal).phase, 'closed');
    release(true); await pause(30);
    assert.equal(await readFile(join(s.directory, 'policy/state.json'), 'utf8'), journal);
    assert.equal(s.boundary.audit[0].quiescence, 'unknown');
  } finally { release?.(false); await s.close(); }
});

test('authority deadline bounds trusted policy reads even without an HTTP caller', async () => {
  const s = await setup(fixturePolicy(), 40); let release!: (value: boolean) => void;
  try {
    const held = new Promise<boolean>(resolve => release = resolve);
    s.router.quiescent = async () => held;
    const began = Date.now();
    await assert.rejects(s.gate.drainAndReplace({ ...fixturePolicy(), epoch: 2, revision: 'policy_2' }), /deadline/);
    assert(Date.now() - began < 180); assert.equal(s.gate.snapshot().phase, 'closed');
    await s.boundary.close();
    const journal = await readFile(join(s.directory, 'policy/state.json'), 'utf8');
    release(true); await pause(30);
    assert.equal(await readFile(join(s.directory, 'policy/state.json'), 'utf8'), journal); assert.equal(s.router.mutations, 0);
  } finally { release?.(false); await s.close(); }
});

test('a cancelled non-cooperative policy writer keeps its lock quarantined across late completion', async () => {
  const s = await setup(); let release!: () => void;
  try {
    const original = s.router.replaceFrozenPolicy.bind(s.router);
    let entered = false; const held = new Promise<void>(resolve => release = resolve);
    s.router.replaceFrozenPolicy = async next => { entered = true; await held; return original(next); };
    const mutation = s.gate.drainAndReplace({ ...fixturePolicy(), epoch: 2, revision: 'policy_2' });
    void mutation.catch(() => {}); await until(() => entered);
    await assert.rejects(s.boundary.close(), /boundary_closed/); await assert.rejects(mutation, /boundary_closed/);
    const journal = await readFile(join(s.directory, 'policy/state.json'), 'utf8');
    const owner = await readFile(join(s.directory, 'policy/owner.lock'), 'utf8');
    assert.equal(JSON.parse(journal).phase, 'closed');
    await assert.rejects(PolicyGate.open(join(s.directory, 'policy'), s.router), /EEXIST/);
    release(); await pause(30); assert.equal(s.router.mutations, 1);
    assert.equal(await readFile(join(s.directory, 'policy/state.json'), 'utf8'), journal);
    assert.equal(await readFile(join(s.directory, 'policy/owner.lock'), 'utf8'), owner);
    await assert.rejects(s.gate.activate(), /boundary_closed/);
    await assert.rejects(PolicyGate.open(join(s.directory, 'policy'), s.router), /EEXIST/);
  } finally { release?.(); await s.boundary.close().catch(() => {}); await s.router.close(); await rm(s.directory, { recursive: true, force: true }); }
});

test('a timed-out policy writer poisons its existing owner before shutdown or late completion', async () => {
  const s = await setup(fixturePolicy(), 40); let release!: () => void;
  try {
    const g = s.grant(); const original = s.router.replaceFrozenPolicy.bind(s.router);
    const held = new Promise<void>(resolve => release = resolve);
    s.router.replaceFrozenPolicy = async next => { await held; return original(next); };
    const next = { ...fixturePolicy(), epoch: 2, revision: 'policy_2' };
    await assert.rejects(s.gate.drainAndReplace(next), /boundary_closed/);
    assert.equal(s.gate.snapshot().phase, 'closed');
    await assert.rejects(s.gate.activate(), /boundary_closed/);
    await assert.rejects(s.gate.admit(g.binding, () => true), /boundary_closed/);
    await assert.rejects(s.gate.drainAndReplace(next), /boundary_closed/);
    const result = await call(s.workerSocket, g.token); assert.equal(result.status, 503); assert.equal(s.router.calls.length, 0);
    release(); await pause(30); assert.equal(s.router.mutations, 1);
    await assert.rejects(s.gate.activate(), /boundary_closed/);
    await assert.rejects(s.boundary.close(), /boundary_closed/);
    await assert.rejects(PolicyGate.open(join(s.directory, 'policy'), s.router), /EEXIST/);
  } finally { release?.(); await s.boundary.close().catch(() => {}); await s.router.close(); await rm(s.directory, { recursive: true, force: true }); }
});
