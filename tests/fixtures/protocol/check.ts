import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import * as p from '../../../schemas/execution/protocol.ts';

// Fixture payloads deliberately include malformed values. This is test data, not
// an application API; production checks consume the actual wire/typed boundaries.
const suite = JSON.parse(readFileSync(new URL('./data/cases.json', import.meta.url), 'utf8'));
const load = (name: string): p.Message => {
  assert.ok(Object.hasOwn(suite.messages, name), `missing fixture message ${name}`);
  return p.decode(Buffer.from(JSON.stringify(suite.messages[name])));
};
function evaluate(c: any): unknown {
  const current = c.current ?? suite.current;
  try {
    switch (c.op) {
      case 'trace': return c.steps.map(evaluate);
      case 'decode': {
        let wire = Buffer.from(c.wire ?? JSON.stringify(suite.messages[c.message]) ?? '');
        if (c.hex) wire = Buffer.from(c.hex, 'hex');
        wire = Buffer.concat([wire, Buffer.from(' '.repeat(c.padding ?? 0))]);
        p.decode(wire); return 'ok';
      }
      case 'current': return p.checkCurrent(load(c.message), current);
      case 'replay': return p.checkReplay(load(c.message), load(c.previous), current);
      case 'transition': return p.checkTransition(load(c.message) as p.Transition, current, c.state, c.revision);
      case 'lease': return p.checkLease(load(c.request) as p.LeaseRequest, load(c.reply) as p.LeaseReply, current, c.timing);
      case 'ack': return p.checkAck(load(c.message) as p.Result, load(c.reply) as p.ResultAck, current, c.receipt);
      case 'observe': {
        let state = c.observation;
        return c.events.map((event: p.RecoveryEvent) => { state = p.observe(state, event); return state; });
      }
      default: throw new Error(`unknown fixture op ${c.op}`);
    }
  } catch (error) {
    if (!(error instanceof Error) || !['malformed', 'oversized', 'unknown_version'].includes(error.message)) throw error;
    return c.op === 'lease' ? { reason: error.message } : error.message;
  }
}
const actual = suite.cases.map(evaluate);
assert.equal(new Set(suite.cases.map((c: any) => c.name)).size, suite.cases.length, 'unique case names');
for (let i = 0; i < suite.cases.length; i++) assert.deepEqual(actual[i], suite.cases[i].expected, `TypeScript: ${suite.cases[i].name}`);
const go = spawnSync('go', ['run', './tests/fixtures/protocol'], {
  cwd: fileURLToPath(new URL('../../../', import.meta.url)), input: JSON.stringify(suite), encoding: 'utf8', timeout: 120000,
});
assert.ifError(go.error); assert.equal(go.status, 0, go.stderr);
const goActual = JSON.parse(go.stdout);
assert.equal(goActual.length, actual.length);
for (let i = 0; i < actual.length; i++) {
  assert.deepEqual(goActual[i], suite.cases[i].expected, `Go: ${suite.cases[i].name}`);
  assert.deepEqual(goActual[i], actual[i], `agreement: ${suite.cases[i].name}`);
}
for (const check of [
  () => p.checkCurrent({} as p.Message, suite.current),
  () => p.checkReplay({} as p.Message, load('assign'), suite.current),
  () => p.checkTransition({} as p.Transition, suite.current, 'assigned', 1),
  () => p.checkLease({} as p.LeaseRequest, load('reply') as p.LeaseReply, suite.current, {} as p.Timing),
  () => p.checkAck({} as p.Result, load('ack') as p.ResultAck, suite.current, {} as p.Receipt),
  () => p.observe({} as p.Observation, 'partition'),
]) assert.throws(check, /^Error: malformed$/);
console.log(`${actual.length} shared cases: Go/TypeScript expected outputs agree; typed boundaries reject invalid input`);
