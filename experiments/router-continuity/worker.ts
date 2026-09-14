// Default worker staging only; no supervisor, private control or fault modules.
import { readFileSync } from 'node:fs';
import { createFixture } from '../harness/native/fixture.ts';
import { relay } from '../harness/native/relay.ts';
import { start, validateRequest, type Request } from './client.ts';
const bytes = readFileSync(0); if (bytes.length > 8192) throw Error('continuity_input_bytes');
const request: Request = JSON.parse(bytes.toString('utf8')); validateRequest(request);
if (createFixture('/work/repo') !== request.baseSHA) throw Error('continuity_base');
const transport = await relay(request.token,65536);
const handle = start(request); let parked = false;
for (const signal of ['SIGTERM','SIGINT'] as const) process.on(signal,() => { if (parked) process.exit(0); else void handle.cancel(); });
try {
  const result = await handle.done;
  await new Promise<void>((resolve,reject) => process.stdout.write(JSON.stringify({event:'continuity_worker_observation',result,requests:transport.observations,rejected:transport.rejected})+'\n',error => error ? reject(error) : resolve()));
} finally { await transport.close(); }
// Bound the parked artifact-inspection window independently of the binary clock.
parked = true; await new Promise(resolve => setTimeout(resolve,10000));
