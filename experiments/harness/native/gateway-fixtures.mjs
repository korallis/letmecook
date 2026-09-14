// Faults for the synthetic integration launcher only; never a deployment mode.
import { mkdirSync, readFileSync } from 'node:fs';
import { digest } from '../../router-authority-extension/overlay/native-profile.mjs';
import { proof as persistenceProof } from '../../native-evaluation/faults.mjs';
export const fault = { mode: process.env.GAFFER_HARNESS_NATIVE_FAULT ?? 'edit', finalizationAttempts: 0, originalEnds: [], persistence: persistenceProof };
export async function sendFixture(res, events, frames, ordinal, body) {
  const mode = fault.mode;
  if (mode === 'router-error') { res.writeHead(503, { 'content-type': 'application/json' }).end('{"error":{"message":"synthetic router failure"}}'); return; }
  const first = !body.input.some(item => item.type === 'function_call_output');
  const output = events(first);
  if (mode === 'forbidden' && ordinal === 1) {
    for (const event of output) {
      if (event.item?.arguments) event.item.arguments = event.item.arguments.replace('greeting.txt', '../escape.txt');
      if (event.arguments) event.arguments = event.arguments.replace('greeting.txt', '../escape.txt');
      if (event.delta) event.delta = event.delta.replace('greeting.txt', '../escape.txt');
      for (const item of event.response?.output ?? []) if (item.arguments) item.arguments = item.arguments.replace('greeting.txt', '../escape.txt');
    }
  }
  const incomplete = ['cancel', 'tree', 'crash', 'partial'].includes(mode);
  res.writeHead(200, { 'content-type': 'text/event-stream' });
  const bytes = Buffer.from(frames(incomplete ? output.slice(0, 5) : output));
  for (let i = 0; i < bytes.length; i += 17) res.write(bytes.subarray(i, i + 17));
  if (['cancel', 'tree', 'crash'].includes(mode)) { await new Promise(resolve => res.once('close', resolve)); return; }
  if (mode === 'delay' && ordinal === 1) await new Promise(resolve => setTimeout(resolve, 300));
  fault.originalEnds.push({ ordinal, at: Date.now() }); res.end();
}
export async function beforeFinalize() {
  fault.finalizationAttempts++;
  if (fault.mode === 'decision-write' && fault.finalizationAttempts === 1) {
    const owner = JSON.parse(readFileSync('/state/boundary/owner.lock', 'utf8')).owner;
    mkdirSync('/state/boundary/state.' + owner + '.next');
  }
}
export function beforeArtifact(value) {
  if (fault.mode === 'artifact-write') mkdirSync('/state/candidate-' + digest(value) + '.json');
  return value;
}
