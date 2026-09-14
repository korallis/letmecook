import { readFileSync } from 'node:fs';
import { createFixture } from './fixture.ts';
import { start, validateRunRequest, assertExecutionEnvironment, type RunRequest } from './run.ts';
import { relay } from './relay.ts';
import { setTimeout as delay } from 'node:timers/promises';
const bytes = readFileSync(0); if (bytes.length > 8192) throw Error('worker_grant_limit');
const request: RunRequest = JSON.parse(bytes.toString('utf8')); validateRunRequest(request);
assertExecutionEnvironment();
if (createFixture('/work/repo') !== request.baseSHA) throw Error('fixture_base_mismatch');
const transport = await relay(request.token, 65536);
let parked = false, stopRequested = false;
let result: Awaited<ReturnType<typeof start>["done"]>;
try {
  const handle = start(request); for (const signal of ['SIGTERM', 'SIGINT'] as const) process.on(signal, () => { stopRequested = true; if (parked) process.exit(0); void handle.cancel(); });
  result = await handle.done;
} finally { await transport.close(); }
// Observation ready also means the relay has closed and immediate stop is safe.
parked = true;
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ event: 'native_worker_observation', result, requests: transport.observations }) + '\n', e => e ? reject(e) : resolve()));
// Retain tmpfs for the trusted read, unless an active cancellation requested stop.
if (!stopRequested) await delay(10000);
