// Synthetic-only failure harness; not staged by the default worker packager.
import assert from 'node:assert/strict';
import { readFileSync, writeFileSync, mkdirSync, rmSync, existsSync } from 'node:fs';
import { connect } from 'node:net';
import { execFileSync, spawn } from 'node:child_process';
import { createFixture } from './fixture.ts';
import { start, validateRunRequest, assertExecutionEnvironment, type RunRequest, type RunHandle } from './run.ts';
import { environment, config } from './client.ts';
import { relay } from './relay.ts';
import { containment } from '../../../tests/fixtures/harness/containment.ts';
const bytes = readFileSync(0); if (bytes.length > 8192) throw Error('worker_grant_limit');
const request: RunRequest = JSON.parse(bytes.toString('utf8')), mode = process.argv[2]; validateRunRequest(request); assertExecutionEnvironment();
assert.equal(createFixture('/work/repo'), request.baseSHA);
const emit = (value: any) => new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify(value) + '\n', e => e ? reject(e) : resolve()));
let handle: RunHandle | undefined, parked = false;
process.on('SIGTERM', () => { if (parked) process.exit(0); else void handle?.cancel(); });
process.on('SIGINT', () => { if (parked) process.exit(0); else void handle?.cancel(); });
if (mode === 'oom') { const buffers: Buffer[] = []; setInterval(() => buffers.push(Buffer.alloc(16 * 1048576, 97)), 20); }
else if (mode === 'containment') {
  const result = await containment(true, 768); await emit({ event: 'native_worker_observation', containment: result }); parked = true;
} else if (mode === 'transport') {
  const transport = [];
  for (const [name, path, extra, body, token, expected] of [
    ['management', '/api/providers', '', '{}', request.token, 404],
    ['control', '/control', '', '{}', request.token, 404],
    ['alternate', '/responses', '', '{}', request.token, 404],
    ['query', '/v1/responses?model=other', '', '{}', request.token, 404],
    ['absolute', 'http://localhost/v1/responses', '', '{}', request.token, 404],
    ['spoof-originator', '/v1/responses', 'originator: codex\r\n', '{}', request.token, 400],
    ['cookie', '/v1/responses', 'cookie: synthetic\r\n', '{}', request.token, 400],
    ['duplicate-auth', '/v1/responses', 'authorization: Bearer ' + request.token + '\r\n', '{}', request.token, 400],
    ['schema', '/v1/responses', '', '{"max_output_tokens":128}', request.token, 502],
    ['invalid-auth', '/v1/responses', '', '{}', '0'.repeat(64), 401],
  ] as const) {
    const response = await new Promise<string>((resolve, reject) => {
      const socket = connect('/router/inference.sock'); let text = '';
      socket.setTimeout(2000, () => socket.destroy(Error('transport_timeout')));
      socket.on('error', reject); socket.on('data', chunk => { text += chunk; if (text.length > 8192) socket.destroy(Error('transport_output_limit')); }); socket.on('end', () => resolve(text));
      socket.on('connect', () => socket.write('POST ' + path + ' HTTP/1.1\r\nHost: localhost\r\nAuthorization: Bearer ' + token + '\r\nContent-Type: application/json\r\nContent-Length: ' + Buffer.byteLength(body) + '\r\n' + extra + 'Connection: close\r\n\r\n' + body));
    });
    const status = Number(response.match(/^HTTP\/1.1 (\d+)/)?.[1]); assert.equal(status, expected, name); transport.push({ name, status });
  }
  assert.equal(existsSync('/control/gateway.sock'), false); await emit({ event: 'native_worker_observation', transport, privateControlMounted: false }); parked = true;
} else if (['ambient', 'auth'].includes(mode)) {
  let admitted = false;
  if (mode === 'ambient') { mkdirSync('/work/repo/.opencode'); writeFileSync('/work/repo/opencode.json', '{"plugin":["file:///work/repo/.opencode/probe.mjs"]}'); writeFileSync('/work/repo/.opencode/probe.mjs', 'import{writeFileSync}from"node:fs";writeFileSync("/work/plugin-ran","executed");export default async()=>({});'); }
  else { mkdirSync('/work/data/opencode', { recursive: true }); writeFileSync('/work/data/opencode/auth.json', '{"openai":{"type":"oauth","access":"SYNTHETIC_AUTH_CANARY","refresh":"SYNTHETIC_REFRESH_CANARY","expires":9999999999999}}'); }
  try { handle = start(request); admitted = true; await handle.cancel(); } catch (error: any) { await emit({ event: 'admission-refused', reason: error.message }); }
  assert.equal(admitted, false);
  if (mode === 'ambient') { rmSync('/work/repo/.opencode', { recursive: true }); rmSync('/work/repo/opencode.json'); }
  else rmSync('/work/data/opencode/auth.json');
  writeFileSync('/work/repo/.git/hooks/post-commit', '#!/bin/sh\necho forbidden\n', { mode: 0o700 }); assert.throws(() => start(request), /ambient_repository_configuration/); rmSync('/work/repo/.git/hooks/post-commit');
  await emit({ event: 'native_worker_observation', admission: 'refused', mode, originalBinaryStarted: false, pluginExecuted: existsSync('/work/plugin-ran') }); parked = true;
} else {
  process.env.OPENAI_API_KEY = 'SYNTHETIC_PARENT_AUTH_CANARY'; process.env.HTTPS_PROXY = 'http://127.0.0.1:1'; process.env.OTEL_EXPORTER_OTLP_ENDPOINT = 'http://127.0.0.1:1';
  if (mode === 'tree') { const child = spawn('/bin/sh', ['-c', "trap '' TERM; setsid /bin/sh -c 'trap \"\" TERM; while :; do sleep 1; done' & while :; do sleep 1; done"], { detached: true, stdio: 'ignore' }); child.unref(); }
  const transport = await relay(request.token, 65536, r => {
    if (transport.observations.length === 1 && ['cancel', 'tree'].includes(mode)) setTimeout(() => { void handle?.cancel(); }, 200);
    if (transport.observations.length === 1 && mode === 'crash') setTimeout(() => execFileSync('pkill', ['-KILL', '-x', 'opencode']), 200);
    void r;
  });
  if (mode === 'disabled-hook') {
    writeFileSync('/work/config.json', JSON.stringify(config(request.token, request.approval)), { mode: 0o600 });
    const child = spawn('/fixture/opencode', ['run', '--format', 'json', '--model', 'openai/gpt-6-astra', '--title', 'Disabled-hook negative', request.brief], { cwd: '/work/repo', env: { ...environment(), OPENCODE_DISABLE_DEFAULT_PLUGINS: 'true' }, stdio: ['ignore', 'pipe', 'pipe'] });
    let output = '', stderr = ''; child.stdout.on('data', b => output += b); child.stderr.on('data', b => stderr += b);
    const timer = setTimeout(() => child.kill('SIGKILL'), 30000); const exit = await new Promise(r => child.once('close', (code, signal) => r({ code, signal }))); clearTimeout(timer);
    await emit({ event: 'native_worker_observation', disabledHook: true, exit, output, stderr, requests: transport.observations });
  } else {
    handle = start(request); const result = await handle.done;
    await emit({ event: 'native_worker_observation', result, requests: transport.observations, childEnvironment: environment(), processes: execFileSync('ps', ['-eo', 'pid,ppid,comm'], { encoding: 'utf8' }) });
  }
  await transport.close(); parked = true;
}
// Hold the tmpfs after observation for a separately invoked trusted artifact read.
await new Promise(() => { setInterval(() => {}, 1000); });
