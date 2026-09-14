import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { evidenceControls } from './evidence-control.mjs';
import { digest } from '../router-authority-extension/overlay/native-profile.mjs';
const fixture = JSON.parse(readFileSync(new URL('../../tests/fixtures/harness/native/captured.json', import.meta.url), 'utf8'));
function setup() {
  const f = structuredClone(fixture), p = f.gateway.policy, journal = f.gateway.journal, observed = f.gateway.observed, writes = [];
  observed.artifacts = [];
  const api = evidenceControls({ policy: () => p, gate: () => ({ snapshot: () => journal }), authority: { receipt: id => f.gateway.receipts.find(r => r.id === id), quiescent: () => true, evaluationScope: () => f.gateway.scope }, packetDigest: 'a'.repeat(64), observed, persistCandidate: value => { const file = 'candidate-' + digest(value) + '.json'; writes.push({ file, value }); return { digest: digest(value), file }; } });
  const d = journal.decisions, value = { schema: 1, consumer: p.native.protocol, kind: 'repository_change', attemptId: d[0].attemptId, bindingDigest: digest(d[0].router.binding), policyDigest: digest(p), packetDigest: 'a'.repeat(64), scopeDigest: digest(f.gateway.scope), requestIds: d.map(d => d.requestId), decisionDigests: d.map(d => digest(d)), receiptDigests: d.map(d => d.evidence.receiptDigest), artifact: { path: 'greeting.txt', content: 'hello from probe\n', metadata: { source: 'constructed artifact fixture' } } };
  return { f, p, journal, observed, writes, api, value };
}
test('private evidence and idempotent durable candidate ack preserve original receipt and decision links', () => {
  const x = setup(), evidence = x.api.evidence(x.value.attemptId); assert.equal(evidence.decisions.length, 2); assert.equal(evidence.receipts.length, 2); assert.equal(evidence.reservations.length, 0);
  const ack = x.api.candidate(x.value); assert.deepEqual(ack, { acknowledged: true, digest: digest(x.value), file: 'candidate-' + digest(x.value) + '.json' }); assert.equal(x.writes.length, 1); assert.deepEqual(x.writes[0].value, x.value); assert.equal(x.api.evidence(x.value.attemptId).artifacts[0].digest, ack.digest);
  assert.deepEqual(x.api.candidate(x.value), ack); assert.equal(x.writes.length, 2); assert.equal(x.api.evidence(x.value.attemptId).artifacts.length, 1);
});
test('candidate controls refuse unknown, changed, partial, cross-binding and unrelated artifact claims', () => {
  for (const mutate of [x => x.value.packetDigest = 'f'.repeat(64), x => x.value.scopeDigest = 'f'.repeat(64), x => x.value.requestIds.reverse(), x => x.value.bindingDigest = 'b'.repeat(64), x => x.value.policyDigest = 'c'.repeat(64), x => x.journal.reservations.push({ attemptId: x.value.attemptId, requestId: x.value.requestIds[0] }), x => x.f.gateway.receipts[0].operations[0].terminal = 'unknown', x => x.journal.decisions[0].delivery = 'unobserved', x => x.value.artifact.path = 'not-approved', x => x.value.kind = 'plan_proposal', x => x.value.extra = true]) {
    const x = setup(); mutate(x); assert.throws(() => x.api.candidate(x.value)); assert.equal(x.writes.length, 0);
  }
});
test('a failed durable artifact write returns no acknowledgement and publishes no artifact record', () => {
  const x = setup(), api = evidenceControls({ policy: () => x.p, gate: () => ({ snapshot: () => x.journal }), authority: { receipt: id => x.f.gateway.receipts.find(r => r.id === id), quiescent: () => true, evaluationScope: () => x.f.gateway.scope }, packetDigest: 'a'.repeat(64), observed: x.observed, persistCandidate: () => { throw Error('injected-fsync-failure'); } });
  assert.throws(() => api.candidate(x.value), /injected-fsync-failure/); assert.equal(x.observed.artifacts.length, 0);
});
