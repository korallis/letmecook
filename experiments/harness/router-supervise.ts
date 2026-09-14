// Trusted bounded process owner. SQLite's stock SIGTERM handler requires the
// shared bridge's private cooperative SIGUSR2 stop path for result retention.
import { spawn } from 'node:child_process';
import { readFile } from 'node:fs/promises';
import assert from 'node:assert/strict';
const child = spawn(process.execPath, ['--experimental-loader', '/probe/loader.mjs', '/harness/router-gateway.ts', process.argv[2]], { env: process.env, stdio: ['ignore', 'pipe', 'pipe'] });
child.stdout!.pipe(process.stdout);
let stderr = ''; child.stderr!.on('data', b => { stderr += b; if (stderr.length > 65536) stderr = stderr.slice(-65536); });
let stopping = false;
for (const signal of ['SIGTERM', 'SIGINT'] as const) process.on(signal, () => { if (!stopping) { stopping = true; child.kill('SIGUSR2'); } });
const timer = setTimeout(() => child.kill('SIGKILL'), 40000);
try {
  const result = await new Promise<{ code: number | null; signal: string | null }>((resolve, reject) => { child.once('error', reject); child.once('exit', (code, signal) => resolve({ code, signal })); });
  clearTimeout(timer); process.stderr.write(stderr); assert.equal(result.code, 0);
  console.log(JSON.stringify({ event: 'finished', result: JSON.parse(await readFile('/work/result.json', 'utf8')) }));
} catch (error) { clearTimeout(timer); process.stderr.write(stderr); console.error(String(error)); process.exitCode = 1; }
