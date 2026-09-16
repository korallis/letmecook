import assert from 'node:assert/strict';
import { execFileSync, spawn } from 'node:child_process';
import { once } from 'node:events';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { decode, MAX_BYTES, MAX_ITEMS, type Snapshot } from '../../../schemas/readapi/types.ts';

const root = new URL('../../../', import.meta.url);
const dir = mkdtempSync(join(tmpdir(), 'gaffer-read-check-'));
const binary = join(dir, 'gafferd');
const encode = (v: unknown) => new TextEncoder().encode(JSON.stringify(v));
try {
  execFileSync('go', ['build', '-trimpath', '-buildvcs=false', '-o', binary, './cmd/gafferd'], { cwd: root, stdio: 'inherit', env: { ...process.env, CGO_ENABLED: '0' } });
  for (const mode of ['fixture-only', 'store-only']) {
  const args = mode === 'fixture-only' ? ['--fixture'] : ['--state-dir', join(dir, 'state'), '--artifacts-dir', join(dir, 'artifacts'), '--listen', '127.0.0.1:0'];
  const daemon = spawn(binary, args, { cwd: dir, stdio: ['ignore', 'pipe', 'inherit'] });
  const exited = once(daemon, 'exit');
  const timer = setTimeout(() => daemon.kill('SIGKILL'), 20000);
  try {
    let output = '';
    for await (const chunk of daemon.stdout) {
      output += chunk.toString();
      if (output.includes('\n')) break;
      assert(output.length < 1024);
    }
    const match = new RegExp(`^${mode} (http://127\\.0\\.0\\.1:\\d+)/api/v1/status\\n`).exec(output);
    assert(match, `startup failed: ${output}`);
    const base = match[1];
    const read = async (path: string) => {
      const res = await fetch(base + path, { signal: AbortSignal.timeout(3000) });
      assert.equal(res.status, 200); assert.equal(res.headers.get('access-control-allow-origin'), null);
      return new Uint8Array(await res.arrayBuffer());
    };
    const status = decode(await read('/api/v1/status'), 'status');
    const snapshot = decode(await read('/api/v1/snapshot'), 'snapshot');
    assert.equal(status.mode, mode);
    if (mode === 'store-only') {
      assert.equal(status.task_count, 0); assert.equal(status.event_count, 0);
      assert.equal(status.schema_version, 5);
      assert.equal(decode(encode({ ...status, schema_version: 2 }), 'status').schema_version, 2);
      assert.equal(decode(encode({ ...status, schema_version: 3 }), 'status').schema_version, 3);
      assert.equal(decode(encode({ ...status, schema_version: 4 }), 'status').schema_version, 4);
      assert.throws(() => decode(encode({ ...status, schema_version: 6 }), 'status'));
      assert.equal(snapshot.generation, status.generation);
      assert.deepEqual(snapshot.tasks, []); assert.deepEqual(snapshot.events, []);
      assert.throws(() => decode(encode({ ...status, mode: 'fixture-only' }), 'status'));
      assert.throws(() => decode(encode({ ...snapshot, schema_version: 1 }), 'snapshot'));
      console.log('read API: real persistent daemon, empty install and strict mode/schema decoding passed');
      continue;
    }
    assert.equal(status.task_count, 1); assert.equal(status.event_count, 2);
    assert.equal(snapshot.generation, status.generation);
    assert.equal(snapshot.tasks[0].state, 'reconciling');
    assert.equal(snapshot.tasks[0].attempt.state, 'unknown');
    assert.equal(snapshot.tasks[0].attempt.observation.confirmed_process, 'not_started');
    assert.equal(snapshot.events[0].message.kind, 'assign');
    assert.equal(snapshot.events[1].message.kind, 'transition');
    const filtered = decode(await read(`/api/v1/snapshot?task_id=${snapshot.tasks[0].task_id}&limit=1`), 'snapshot');
    assert.equal(filtered.events.length, 1);
    for (const mutate of [
      (v: any) => { v.version = 'future'; },
      (v: any) => { v.mode = 'production'; },
      (v: any) => { v.owner = 'invented'; },
      (v: any) => { delete v.missing_capabilities; },
      (v: any) => { v.tasks[0].attempt.identity.epoch = 0; },
      (v: any) => { v.tasks[0].attempt.revision = Number.MAX_SAFE_INTEGER + 1; },
      (v: any) => { v.tasks[0].state = 'accepted'; },
      (v: any) => { v.tasks[0].attempt.observation.quarantined = false; },
      (v: any) => { v.tasks[0].attempt.observation.remote_work = 'quiescent'; },
      (v: any) => { v.tasks.push(v.tasks[0]); },
      (v: any) => { v.events[1].sequence = v.events[0].sequence; },
      (v: any) => { v.events[0].message.route.route_ref = 'http://evil.example'; },
      (v: any) => { v.events[0].message.identity.generation = '00000000-0000-4000-8000-000000000099'; },
      (v: any) => { v.tasks = Array(MAX_ITEMS + 1).fill(v.tasks[0]); },
    ]) {
      const v: Snapshot = structuredClone(snapshot); mutate(v);
      assert.throws(() => decode(encode(v), 'snapshot'));
    }
    const raw = JSON.stringify(snapshot);
    for (const text of [raw + '{}', raw.replace('"mode":', '"mode":"fixture-only","mode":'), raw.replace('"epoch":1', '"epoch":1.0'), 'null']) {
      assert.throws(() => decode(new TextEncoder().encode(text), 'snapshot'));
    }
    assert.throws(() => decode(new Uint8Array(MAX_BYTES + 1), 'snapshot'));
    assert.throws(() => decode(new Uint8Array([0xff]), 'snapshot'));
    assert.throws(() => decode(encode({ ...status, event_count: -1 }), 'status'));
    assert.throws(() => decode(encode({ ...status, event_count: null }), 'status'));
    assert.throws(() => decode(encode(snapshot), 'status'));
    console.log('read API: real CGO-free daemon, store-backed Go/TypeScript schema, bounded filters and adverse wire cases passed');
  } finally {
    daemon.kill('SIGTERM');
    const [code, signal] = await exited;
    clearTimeout(timer);
    assert.equal(signal, null); assert.equal(code, 0);
  }
  }
} finally { rmSync(dir, { recursive: true, force: true }); }
