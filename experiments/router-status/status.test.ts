import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import type { Server, RequestListener } from 'node:http';
import { test } from 'node:test';
import { createStatusAdapter, INSPECTED_COMMIT, DEPLOYED_COMMIT } from './status.ts';
import type { Config } from './status.ts';

const TIME = Date.parse('2026-09-14T12:00:00.000Z');
const SECRET = 'synthetic-provider-secret-never-project';
function config(overrides: Partial<Config> = {}): Config {
  return {
    routerId: 'router-fixture', origin: 'https://router.invalid', managementCredential: 'synthetic-management-token',
    inspectedCommit: INSPECTED_COMMIT,
    connections: [{ ref: 'connection-a', providerRef: 'provider-a', upstreamId: 'upstream-a', upstreamProvider: 'codex' }],
    routes: [
      { routeId: 'worker', policyRevision: 'policy-v1', connectionRefs: ['connection-a'] },
      { routeId: 'reviewer', policyRevision: 'policy-v1', connectionRefs: ['connection-a'] },
    ], ...overrides,
  };
}
function payload(patch: Record<string, unknown> = {}) {
  return { connections: [{ id: 'upstream-a', provider: 'codex', isActive: true, routingStatus: 'eligible',
    authState: 'ok', healthStatus: 'healthy', quotaState: 'ok', lastCheckedAt: '2026-09-14T12:00:00.000Z',
    nextRetryAt: null, resetAt: null, name: SECRET, email: SECRET, accessToken: SECRET,
    providerSpecificData: { credentials: { accessToken: SECRET, refreshToken: SECRET } },
    reasonDetail: SECRET, ...patch }], byApiKey: { [SECRET]: { apiKey: SECRET } }, providerSummaries: { secret: SECRET } };
}
function fixtureFetch(value: unknown): typeof fetch {
  return async () => Response.json(value);
}
async function read(value: unknown = payload(), overrides: Partial<Config> = {}) {
  return createStatusAdapter(config(overrides), { fetch: fixtureFetch(value), now: () => TIME }).snapshot('worker');
}
async function listen(listener: RequestListener): Promise<{ server: Server; origin: string; close: () => Promise<void> }> {
  const server = createServer(listener);
  await new Promise<void>(resolve => server.listen(0, '127.0.0.1', resolve));
  const address = server.address();
  assert(address && typeof address === 'object');
  return { server, origin: `http://127.0.0.1:${address.port}`, close: async () => {
    server.closeAllConnections();
    await new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve()));
  } };
}

test('constructs a closed, secret-free projection; passive eligibility is not route readiness', async () => {
  const result = await read();
  assert.deepEqual(result, {
    schema_version: 1, router_id: 'router-fixture', route_id: 'worker', policy_revision: 'policy-v1', projected_at: '2026-09-14T12:00:00.000Z',
    route: { state: 'unknown', readiness: 'unknown', reason: 'no_observation', source: 'none', observed_at: null, validity_until: null },
    connections: [{ connection_ref: 'connection-a', provider_ref: 'provider-a', state: 'eligible', source: 'router_snapshot',
      observed_at: '2026-09-14T12:00:00.000Z', validity_until: '2026-09-14T12:00:30.000Z', retry_after: null }],
    capabilities: { state: 'unknown', source: 'none', observed_at: null, validity_until: null },
  });
  assert(!JSON.stringify(result).includes(SECRET));
  assert(!JSON.stringify(result).includes('upstream-a'));
});

test('aliases retain one connection identity and never create balances or capacity totals', async () => {
  const adapter = createStatusAdapter(config(), { fetch: fixtureFetch(payload()), now: () => TIME });
  const worker = await adapter.snapshot('worker');
  const reviewer = await adapter.snapshot('reviewer');
  assert.deepEqual(worker.connections, reviewer.connections);
  const unique = new Set([...worker.connections, ...reviewer.connections].map(row => row.connection_ref));
  assert.equal(unique.size, 1);
  assert.throws(() => createStatusAdapter(config({ connections: [config().connections[0]!, { ...config().connections[0]!, ref: 'alias' }] })), /invalid_configuration/);
});

test('0.5.75 has its own conservative profile; canonical fork fields cannot imply health', async () => {
  const result = await read(payload({ testStatus: 'active' }), { inspectedCommit: DEPLOYED_COMMIT });
  assert.equal(result.connections[0]?.state, 'unknown');
  assert.equal(result.connections[0]?.source, 'none');
  assert.equal(result.route.readiness, 'unknown');
  const disabled = await read(payload({ isActive: false, lastCheckedAt: SECRET, authState: SECRET }), { inspectedCommit: DEPLOYED_COMMIT });
  assert.equal(disabled.connections[0]?.state, 'disabled');
  assert.equal(disabled.connections[0]?.observed_at, '2026-09-14T12:00:00.000Z');
  assert.equal(disabled.connections[0]?.retry_after, null);
  assert(!JSON.stringify(disabled).includes(SECRET));
  const malformed = await read(payload({ isActive: SECRET }), { inspectedCommit: DEPLOYED_COMMIT });
  assert.equal(malformed.route.reason, 'malformed_observation');
});

test('preserves two distinct subscriptions without choosing or summing them', async () => {
  const value = payload();
  value.connections.push({ ...value.connections[0]!, id: 'upstream-b', routingStatus: 'exhausted', quotaState: 'exhausted' });
  const result = await read(value, {
    connections: [...config().connections, { ref: 'connection-b', providerRef: 'provider-a', upstreamId: 'upstream-b', upstreamProvider: 'codex' }],
    routes: [{ routeId: 'worker', policyRevision: 'policy-v1', connectionRefs: ['connection-a', 'connection-b'] }],
  });
  assert.deepEqual(result.connections.map(row => [row.connection_ref, row.state]), [['connection-a', 'eligible'], ['connection-b', 'exhausted']]);
  assert.equal(result.route.readiness, 'unknown');
});

test('missing, stale and future provenance cannot become fresh by polling', async () => {
  for (const lastCheckedAt of [undefined, null, '2026-09-14T11:59:30.000Z', '2026-09-14T12:00:01.000Z']) {
    const result = await read(payload({ lastCheckedAt }));
    assert.equal(result.connections[0]?.state, 'unknown');
    assert.equal(result.route.readiness, 'unknown');
  }
  const empty = await read({ connections: [] });
  assert.equal(empty.connections[0]?.source, 'none');
  assert.equal(empty.connections[0]?.retry_after, null);
});

test('uses response completion time and respects a shorter freshness limit', async () => {
  let tick = 0;
  const adapter = createStatusAdapter(config({ freshnessMs: 1000 }), { fetch: fixtureFetch(payload()), now: () => TIME + tick++ * 1000 });
  const result = await adapter.snapshot('worker');
  assert.equal(result.connections[0]?.state, 'unknown');
  assert.equal(result.connections[0]?.validity_until, '2026-09-14T12:00:01.000Z');
  assert.throws(() => createStatusAdapter(config({ freshnessMs: 30_001 })), /invalid_configuration/);
});

test('preserves negative and conflicting canonical observations conservatively', async () => {
  for (const [patch, expected] of [
    [{ isActive: false }, 'disabled'], [{ authState: 'expired' }, 'blocked'], [{ healthStatus: 'down' }, 'blocked'],
    [{ quotaState: 'exhausted' }, 'exhausted'], [{ routingStatus: 'blocked' }, 'blocked'], [{ authState: undefined }, 'unknown'],
  ] as const) {
    assert.equal((await read(payload(patch))).connections[0]?.state, expected);
  }
  const result = await read(payload({ quotaState: 'exhausted', nextRetryAt: '2026-09-14T12:01:00Z', resetAt: '2026-09-14T12:02:00Z' }));
  assert.equal(result.connections[0]?.retry_after, '2026-09-14T12:02:00.000Z');
});

test('schema drift, duplicate identity and invalid inspected fields fail closed without raw errors', async () => {
  const bad: unknown[] = [null, [], {}, { connections: {} }, payload({ routingStatus: SECRET }), payload({ authState: { token: SECRET } }),
    payload({ lastCheckedAt: SECRET }), payload({ lastCheckedAt: '2026-02-30T12:00:00Z' }), payload({ provider: SECRET }), payload({ isActive: 'true' }),
    { connections: [payload().connections[0], payload().connections[0]] }];
  for (const value of bad) {
    const diagnostics: string[] = [];
    const result = await createStatusAdapter(config(), { fetch: fixtureFetch(value), now: () => TIME, diagnostic: code => diagnostics.push(code) }).snapshot('worker');
    assert.equal(result.route.reason, 'malformed_observation');
    assert(result.connections.every(row => row.state === 'unknown' && row.source === 'none'));
    assert.deepEqual(diagnostics, ['schema_mismatch']);
    assert(!JSON.stringify({ result, diagnostics }).includes(SECRET));
  }
});

test('unsupported pin and unconfigured routes are refused before network access', async () => {
  let calls = 0;
  const adapter = createStatusAdapter(config({ inspectedCommit: 'unknown' }), { fetch: async () => { calls++; throw new Error(SECRET); } });
  assert.equal((await adapter.snapshot('worker')).route.reason, 'policy_unverified');
  await assert.rejects(adapter.snapshot(SECRET), { message: 'unconfigured_route' });
  assert.equal(calls, 0);
});

test('trusted origin and bindings cannot be changed through a caller-owned config object', async () => {
  const initial = config();
  const seen: string[] = [];
  const adapter = createStatusAdapter(initial, { fetch: async url => { seen.push(String(url)); return Response.json(payload()); }, now: () => TIME });
  initial.origin = 'https://unexpected.invalid';
  initial.connections[0]!.ref = 'changed';
  initial.routes[0]!.connectionRefs.length = 0;
  const result = await adapter.snapshot('worker');
  assert.deepEqual(seen, ['https://router.invalid/api/providers']);
  assert.equal(result.connections[0]?.connection_ref, 'connection-a');
});

test('only fixed passive GET reaches a real HTTP server; no usage refresh or management writes', async () => {
  const requests: Array<[string | undefined, string | undefined, string | undefined]> = [];
  const server = await listen((request, response) => {
    requests.push([request.method, request.url, request.headers.authorization]);
    response.setHeader('content-type', 'application/json'); response.end(JSON.stringify(payload()));
  });
  try {
    const adapter = createStatusAdapter(config({ origin: server.origin, syntheticLoopback: true }), { now: () => TIME });
    await adapter.snapshot('worker'); await adapter.snapshot('reviewer');
    assert.deepEqual(requests, Array.from({ length: 2 }, () => ['GET', '/api/providers', 'Bearer synthetic-management-token']));
  } finally { await server.close(); }
});

test('redirects are not followed and server errors never copy response secrets into diagnostics', async () => {
  for (const status of [301, 302, 307, 401, 403, 429, 500]) {
    let calls = 0;
    const server = await listen((_request, response) => {
      calls++; response.writeHead(status, { location: `/api/usage/${SECRET}`, 'content-type': 'text/plain' }); response.end(SECRET);
    });
    try {
      const diagnostics: string[] = [];
      const result = await createStatusAdapter(config({ origin: server.origin, syntheticLoopback: true }), { diagnostic: code => diagnostics.push(code) }).snapshot('worker');
      assert.equal(result.route.reason, 'router_unreachable'); assert.equal(calls, 1);
      assert.deepEqual(diagnostics, ['unavailable']); assert(!JSON.stringify(result).includes(SECRET));
    } finally { await server.close(); }
  }
});

test('response bounds apply to chunked bodies and cancellation closes a stalled connection', async () => {
  const oversized = await listen((_request, response) => {
    response.writeHead(200, { 'content-type': 'application/json' }); response.write('{"secret":"'); response.end(SECRET.repeat(100));
  });
  try {
    const result = await createStatusAdapter(config({ origin: oversized.origin, syntheticLoopback: true, maxBytes: 50 })).snapshot('worker');
    assert.equal(result.route.reason, 'malformed_observation');
  } finally { await oversized.close(); }
  let disconnected!: () => void;
  const closed = new Promise<void>(resolve => { disconnected = resolve; });
  const stalled = await listen((request, response) => {
    request.on('close', disconnected);
    response.writeHead(200, { 'content-type': 'application/json' }); response.write('{');
  });
  try {
    const start = performance.now();
    const result = await createStatusAdapter(config({ origin: stalled.origin, syntheticLoopback: true, timeoutMs: 100 })).snapshot('worker');
    assert.equal(result.route.reason, 'router_unreachable'); assert(performance.now() - start < 1500);
    await Promise.race([closed, new Promise((_, reject) => { const timer = setTimeout(() => reject(new Error('abort_did_not_close_connection')), 1000); timer.unref(); })]);
  } finally { await stalled.close(); }
});

test('transport/parser/logger exceptions cannot escape with credentials', async () => {
  for (const transport of [async () => { throw new Error(SECRET); }, async () => new Response(SECRET, { headers: { 'content-type': 'application/json' } })]) {
    const result = await createStatusAdapter(config(), { fetch: transport, diagnostic: () => { throw new Error(SECRET); } }).snapshot('worker');
    assert.equal(result.route.readiness, 'unknown'); assert(!JSON.stringify(result).includes(SECRET));
  }
  for (const origin of ['http://router.invalid', `https://${SECRET}@router.invalid`, `https://router.invalid/${SECRET}`, 'ftp://router.invalid']) {
    assert.throws(() => createStatusAdapter(config({ origin, syntheticLoopback: true })), { message: 'invalid_configuration' });
  }
  for (const routerId of [undefined, null, {}, SECRET]) {
    if (typeof routerId === 'string') continue;
    assert.throws(() => createStatusAdapter(config({ routerId } as unknown as Config)), { message: 'invalid_configuration' });
  }
});
