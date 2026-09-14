import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { NativeEvents, classify, describe, environment, config, pins, validateWorkspaceInventory } from './adapter.ts';
import { harnessContainerArgs, assertHarnessContainer } from './isolation.ts';
const captured = (mode: string) => readFileSync(new URL(`../../tests/fixtures/harness/${mode}.native.jsonl`, import.meta.url));
const parse = (mode: string) => { const events = new NativeEvents(); for (const byte of captured(mode)) events.push(Buffer.of(byte)); events.end(); return events.values; };
test('preserve pinned native records across split UTF8/JSON and cursor sequences', () => {
  const events = parse('edit'); assert.equal(events.length, 6); assert.deepEqual(events.map(e => JSON.parse(e.raw)), events.map(e => JSON.parse(JSON.stringify(e.native))));
  assert.equal(events.at(-1)?.sequence, 6); assert.equal(events[0].nativeProtocol, 'opencode-run-json/1.18.30');
  assert.equal(classify(events, 0, null, false, false, true), 'completed_candidate');
  assert.equal(classify(events, 0, null, false, false, false), 'artifact_mismatch');
});
test('CLI rejected permission with exit zero is blocked; native error and cancellation never complete', () => {
  assert.equal(classify(parse('ask'), 0, null, false, false, false), 'approval_blocked');
  assert.equal(classify(parse('strict'), 1, null, false, false, false), 'harness_failed');
  assert.equal(classify(parse('edit'), 0, null, true, false, true), 'cancelled_unknown');
  assert.equal(classify(parse('edit'), 0, null, false, true, true), 'invalid_events');
  assert.equal(classify([], 0, null, false, false, true), 'artifact_mismatch');
});
test('reject malformed, truncated, unknown, cross-session and bounded native event violations', () => {
  for (const bytes of [Buffer.from('not json\n'), Buffer.from('{"type":"other","timestamp":1}\n'), Buffer.from([255,10])]) assert.throws(() => new NativeEvents().push(bytes));
  const cut = new NativeEvents(); cut.push(captured('edit').subarray(0, 8)); assert.throws(() => cut.end());
  assert.throws(() => new NativeEvents(1).push(captured('edit')));
  const cross = new NativeEvents(); cross.push(captured('edit')); assert.throws(() => cross.push(captured('ask')));
});
test('fresh explicit environment and config expose scoped token only and no unsupported settings claims', () => {
  const env = environment(); assert.equal(env.OPENCODE_DISABLE_PROJECT_CONFIG, '1'); assert.equal(env.OPENCODE_PURE, '1');
  assert(!Object.keys(env).some(k => /TOKEN|KEY|PROXY|OTEL/.test(k) && k !== 'OPENCODE_EXPERIMENTAL_OUTPUT_TOKEN_MAX'));
  assert.deepEqual(config('f'.repeat(64), 'ask').permission, { '*': 'deny', edit: 'ask' });
  assert.equal(describe().liveReadiness, 'blocked'); assert.equal(pins.workerMemoryMiB, 512);
});
test('512 MiB is an explicit versioned variant; gateway and all legacy controls stay exact', () => {
  const worker = harnessContainerArgs('name', 'run', '/staging', 'volume'); const gateway = harnessContainerArgs('name', 'run', '/staging', 'volume', true);
  assert.equal(worker[worker.indexOf('--memory') + 1], '512m'); assert.equal(gateway[gateway.indexOf('--memory') + 1], '128m');
  assert.equal(worker[worker.indexOf('--network') + 1], 'none'); assert(worker.includes('type=volume,source=volume,target=/router,volume-nocopy,readonly'));
  assert.throws(() => assertHarnessContainer({ HostConfig: { Memory: 128 * 1024 * 1024 } }, '/staging', 'volume'));
});

test('ambient repository config, plugins and active Git hooks are refused before harness launch', () => {
  validateWorkspaceInventory(['.git', 'greeting.txt', 'AGENTS.md'], ['pre-commit.sample']);
  for (const extra of ['opencode.json', '.opencode', '.claude', '.mcp.json', 'unexpected.txt']) assert.throws(() => validateWorkspaceInventory(['.git', 'greeting.txt', extra], []));
  assert.throws(() => validateWorkspaceInventory(['.git', 'greeting.txt'], ['pre-commit']));
});
test('compatible adapter retains its edit/write-only native events when the Responses profile is added separately', () => {
  const capture = JSON.parse(readFileSync(new URL('../../tests/fixtures/harness/native/captured.json', import.meta.url), 'utf8'));
  assert.throws(() => new NativeEvents().push(Buffer.from(capture.consumer.output)), /invalid_tool_event/);
  assert.equal(config('f'.repeat(64), 'allow').provider.scoped.npm, '@ai-sdk/openai-compatible');
  assert.equal(environment().OPENCODE_DISABLE_DEFAULT_PLUGINS, '1');
});
