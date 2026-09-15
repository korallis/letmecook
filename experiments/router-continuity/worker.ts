// Default worker staging only; no supervisor, private control or fault modules.
import { readFileSync } from 'node:fs';
import { createFixture } from '../harness/native/fixture.ts';
import { relay } from '../harness/native/relay.ts';
import { start, validateRequest, type Request } from './client.ts';
const bytes = readFileSync(0); if (bytes.length > 8192) throw Error('continuity_input_bytes');
const request: Request = JSON.parse(bytes.toString('utf8')); validateRequest(request);
if (createFixture('/work/repo') !== request.baseSHA) throw Error('continuity_base');
const transport = await relay(request.token,65536);
const handle = start(request); let parked = false, stopRequested = false;
for (const signal of ['SIGTERM','SIGINT'] as const) process.on(signal,() => { stopRequested = true; if (parked) process.exit(0); else void handle.cancel(); });
let result;
try {
  result = await handle.done;
} finally { await transport.close(); }
// Ready means relay closed and parked: immediate supervisor stop is now safe.
parked = true;
await new Promise<void>((resolve,reject) => process.stdout.write(JSON.stringify({event:'continuity_worker_observation',result,requests:transport.observations,rejected:transport.rejected})+'\n',error => error ? reject(error) : resolve()));
// Active cancellation must not add a new ten-second hold after the binary exits.
if (!stopRequested) await new Promise(resolve => setTimeout(resolve,10000));
