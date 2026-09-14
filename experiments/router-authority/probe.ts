// Execute only in the network-disabled container documented in README.md.
// VM modules provide import adapters; the OS container provides isolation.
import assert from 'node:assert/strict';
import { readFile, lstat } from 'node:fs/promises';
import { join } from 'node:path';
import { createHash } from 'node:crypto';
import { EventEmitter } from 'node:events';
import vm from 'node:vm';

const pin = '17c4cc76877bd1755030a8414f8d0083f48dcccf';
const sources = [
  ['src/lib/db/repos/usageRepo.js', '10f8c72512dd4344d2b2a67e4dbe987114d737ae5ed36bb2e0908f01da9bcd8a'],
  ['open-sse/utils/streamHandler.js', 'a4fe07ca274f932d0bc75822951b3811243bb66f4524d5e3b4331a5962ac2d43'],
  ['src/lib/db/repos/combosRepo.js', '9895b83480aed3e2b78be71574a95791430d37585c87a43c4178d526fe4ceda0'],
  ['src/app/api/combos/[id]/route.js', 'cc2c500a11303038d072fadcd75694b8003a343e62a132dbc3cc5b1394f1c036'],
] as const;
const sha256 = (value: string | Buffer) => createHash('sha256').update(value).digest('hex');
// An accident guard, not an attestation of the container's security settings.
assert.equal(process.platform, 'linux', 'run_only_in_documented_container');
await lstat('/.dockerenv');
assert.equal(process.argv.length, 3, 'expected_public_source_directory');
const directory = process.argv[2]!;
const verified = new Map<string, string>();
// Verify every byte before evaluating any upstream module. No network, package
// installation, upstream entrypoint or arbitrary dependency resolution occurs.
for (const [path, expected] of sources) {
  const file = join(directory, path);
  const stat = await lstat(file);
  assert.ok(stat.isFile() && stat.size <= 64 * 1024, 'invalid_source_file');
  const bytes = await readFile(file);
  assert.equal(sha256(bytes), expected, `source_digest_mismatch:${path}`);
  verified.set(path, bytes.toString('utf8'));
}

let now = 0;
let next = 0;
type Timer = { id: number; unref(): void };
const timers = new Map<number, { fn: () => void; at: number }>();
const context = vm.createContext({
  AbortController,
  console: { error() { throw new Error('unexpected_source_error'); }, log() {} },
  Date: class extends Date { static override now() { return now; } },
  setTimeout(fn: () => void, ms: number): Timer {
    const id = ++next; timers.set(id, { fn, at: now + ms });
    return { id, unref() {} };
  },
  clearTimeout(token?: Timer) { if (token) timers.delete(token.id); },
});
context.global = context;
context._connectionMapCache = { map: { fixture_connection: 'fixture' }, ts: Number.MAX_SAFE_INTEGER };
context._recentRing = { items: [], initialized: true };
const stub = (exports: Record<string, unknown>) => new vm.SyntheticModule(Object.keys(exports), function() {
  for (const [name, value] of Object.entries(exports)) this.setExport(name, value);
}, { context });
const sourceModule = (path: string) => new vm.SourceTextModule(verified.get(path)!, { context, identifier: path });
const dependencies = new Map([
  ['events', stub({ EventEmitter })],
  ['../driver.js', stub({ getAdapter: async () => { throw new Error('unexpected_database_access'); } })],
  ['../helpers/jsonCol.js', stub({ parseJson: JSON.parse, stringifyJson: JSON.stringify })],
  ['../helpers/metaStore.js', stub({ getMeta() { throw new Error('unexpected_metadata_access'); }, setMeta() { throw new Error('unexpected_metadata_access'); } })],
]);
const usageModule = sourceModule(sources[0][0]);
await usageModule.link(specifier => {
  const dependency = dependencies.get(specifier);
  if (!dependency) throw new Error('unexpected_import');
  return dependency;
});
await usageModule.evaluate({ timeout: 1000 });
function advance(target: number) {
  for (let fired = 0; ; fired++) {
    assert.ok(fired < 1000, 'synthetic_timer_limit');
    const eligible = [...timers.entries()].filter(([, timer]) => timer.at <= target).sort((a, b) => a[1].at - b[1].at);
    if (!eligible.length) break;
    const [id, timer] = eligible[0]!; timers.delete(id); now = timer.at; timer.fn();
  }
  now = target;
}
const usage = usageModule.namespace as unknown as {
  trackPendingRequest(model: string, provider: string, connection: string, pending: boolean): void;
  getActiveRequests(): Promise<{ activeRequests: { count: number }[] }>;
};
const track = (pending: boolean) => usage.trackPendingRequest('fixture_model', 'fixture_provider', 'fixture_connection', pending);
const count = async () => (await usage.getActiveRequests()).activeRequests.reduce((sum, row) => sum + row.count, 0);
track(true);
const before = await count();
advance(59_999); const beforeExpiry = await count();
advance(60_000); const afterExpiry = await count();
assert.equal(before, 1); assert.equal(beforeExpiry, 1); assert.equal(afterExpiry, 0);

const streamModule = sourceModule(sources[1][0]);
await streamModule.link(specifier => {
  if (specifier === '../config/runtimeConfig.js') return stub({ STREAM_STALL_TIMEOUT_MS: 120000 });
  if (specifier === './debugLog.js') return stub({ dbg() {}, isDebugEnabled: () => false });
  throw new Error('unexpected_stream_import');
});
await streamModule.evaluate({ timeout: 1000 });
track(true);
const stream = streamModule.namespace as unknown as {
  createStreamController(options: { onDisconnect(): void }): { handleDisconnect(reason: string): void; signal: AbortSignal };
};
const controller = stream.createStreamController({ onDisconnect: () => track(false) });
controller.handleDisconnect('synthetic_client_closed');
const activeImmediately = await count();
const abortedImmediately = controller.signal.aborted;
advance(60_499); const abortedAt499ms = controller.signal.aborted;
advance(60_500); const abortedAt500ms = controller.signal.aborted;
assert.equal(activeImmediately, 0); assert.equal(abortedImmediately, false);
assert.equal(abortedAt499ms, false); assert.equal(abortedAt500ms, true);

let row = { id: 'fixture_combo', name: 'fixture_route', kind: null as string | null, models: '["fixture_old"]', createdAt: '2026-09-14', updatedAt: '2026-09-14' };
const adapter = {
  get() { return { ...row }; },
  transaction(fn: () => void) { fn(); },
  run(_sql: string, values: [string, string | null, string, string, string]) {
    row = { ...row, name: values[0], kind: values[1], models: values[2], updatedAt: values[3] };
    return { changes: 1 };
  },
};
const comboModule = sourceModule(sources[2][0]);
await comboModule.link(specifier => {
  if (specifier === 'uuid') return stub({ v4: () => 'fixture_id' });
  if (specifier === '../driver.js') return stub({ getAdapter: async () => adapter });
  if (specifier === '../helpers/jsonCol.js') return stub({ parseJson: JSON.parse, stringifyJson: JSON.stringify });
  throw new Error('unexpected_combo_import');
});
await comboModule.evaluate({ timeout: 1000 });
const routeModule = sourceModule(sources[3][0]);
await routeModule.link(specifier => {
  if (specifier === 'next/server') return stub({ NextResponse: { json: (body: unknown, options?: { status: number }) => ({ body, status: options?.status ?? 200 }) } });
  if (specifier === '@/lib/localDb') {
    const namespace = comboModule.namespace as unknown as Record<string, unknown>;
    return stub(Object.fromEntries(['getComboById', 'updateCombo', 'deleteCombo', 'getComboByName'].map(key => [key, namespace[key]])));
  }
  if (specifier === 'open-sse/services/combo.js') return stub({ resetComboRotation() {} });
  throw new Error('unexpected_combo_route_import');
});
await routeModule.evaluate({ timeout: 1000 });
track(true);
const route = routeModule.namespace as unknown as {
  PUT(request: { json(): Promise<{ models: string[] }> }, context: { params: Promise<{ id: string }> }): Promise<{ status: number; body: { models: string[] } }>;
};
const mutation = await route.PUT({ json: async () => ({ models: ['fixture_new'] }) }, { params: Promise.resolve({ id: 'fixture_combo' }) });
const write = { status: mutation.status, models: mutation.body.models, activeAfterWrite: await count() };
assert.equal(write.status, 200); assert.deepEqual(write.models, ['fixture_new']); assert.equal(write.activeAfterWrite, 1);
console.log(JSON.stringify({
  schema: 1, sourceCommit: pin, sourceDigests: Object.fromEntries(sources),
  runtime: { node: process.versions.node, platform: process.platform, arch: process.arch },
  evidence: 'executed_pinned_public_modules_with_fake_clock_and_isolated_imports',
  inferencePerformed: false, completionMarkSent: false, liveAuthorityProven: false,
  counters: { at0ms: before, at59999ms: beforeExpiry, at60000ms: afterExpiry },
  disconnect: { activeImmediately, abortedImmediately, abortedAt499ms, abortedAt500ms },
  writeWhilePending: write,
  integrationLimits: 'Synthetic clock, in-memory database and isolated imports; no real HTTP/authentication/deployment/provider-cancellation test',
  conclusion: 'zero_active_counter_is_not_authoritative_quiescence',
}, null, 2));
