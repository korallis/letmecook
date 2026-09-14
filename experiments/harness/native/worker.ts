import { readFileSync } from 'node:fs';
import { createFixture } from './fixture.ts';
import { start, validateRunRequest, assertExecutionEnvironment, type RunRequest } from './run.ts';
import { relay } from './relay.ts';
const bytes = readFileSync(0); if (bytes.length > 8192) throw Error('worker_grant_limit');
const request: RunRequest = JSON.parse(bytes.toString('utf8')); validateRunRequest(request);
assertExecutionEnvironment();
if (createFixture('/work/repo') !== request.baseSHA) throw Error('fixture_base_mismatch');
const transport = await relay(request.token, 65536);
try {
  const handle = start(request); for (const signal of ['SIGTERM', 'SIGINT'] as const) process.on(signal, () => { void handle.cancel(); });
  const result = await handle.done;
  // The supervisor must retain this observation even when classification fails.
  await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ event: 'native_worker_observation', result, requests: transport.observations }) + '\n', e => e ? reject(e) : resolve()));
} finally { await transport.close(); }
