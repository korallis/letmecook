// Preserve exact receipts, decisions, wire and candidate records without duplicate
// logs, control invocations or host paths. Run only after the frozen proof exits.
import assert from 'node:assert/strict';
import { readFileSync, writeFileSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { resolve } from 'node:path';
import { digest } from '../../experiments/harness/native/client.ts';
const root = resolve(import.meta.dirname, '../..'), sha = (bytes: Uint8Array) => createHash('sha256').update(bytes).digest('hex');
const bytes = readFileSync(process.argv[2]), r = JSON.parse(bytes.toString()), commit = process.argv[4];
assert(/^[a-f0-9]{40}$/.test(commit)); assert(['passed', 'selected-cases-passed'].includes(r.result)); assert.equal(r.cleanup, true); assert.equal(r.sourcesVerifiedAfterRun, true);
assert.equal(r.live, false); assert.equal(r.realProviderCalled, false); assert.equal(r.realEvaluationScopeStarted, false);
for (const [path, expected] of Object.entries(r.sources)) {
  assert.equal(sha(readFileSync(resolve(root, path))), expected, 'current source: ' + path);
  assert.equal(sha(execFileSync('git', ['show', commit + ':' + path], { cwd: root, maxBuffer: 8 * 1048576 })), expected, 'committed source: ' + path);
}
const state = s => ({ running: s.Running, pid: s.Pid, exitCode: s.ExitCode, oomKilled: s.OOMKilled });
const cases = r.cases.map(c => {
  assert.equal(c.result, 'passed'); assert.equal(c.gatewayState.Pid, 0);
  const records = Object.fromEntries(Object.entries(c.retained.records).filter(([name]) => /^(candidate-|deployment-packet-)/.test(name) || c.scenario === 'decision-write' && name === 'boundary/state.json'));
  for (const w of c.workers) {
    assert.equal(w.state.Pid, 0);
    if (w.qualified) { assert.deepEqual(records[w.acknowledgement.body.file], w.candidate); assert.equal(digest(w.candidate), w.qualified.artifactDigest); assert.deepEqual(w.retryAcknowledgement, w.acknowledgement.body); }
  }
  return { scenario: c.scenario, result: c.result, packetDigest: c.packetDigest, scopeStarted: c.scopeStarted, selectedScope: c.selection?.scope,
    staleGrantStatus: c.staleGrantStatus, staleBindingGrantStatus: c.staleBindingGrantStatus,
    gateway: { measured: c.gatewayMeasured, state: state(c.gatewayState), closure: c.closure ?? 'stopped', scope: c.gateway?.scope ?? null, physicalSyntheticSends: c.gateway?.observed.sends.length ?? null, fault: c.gateway?.observed.harnessFault ?? null },
    workers: c.workers.map(w => ({ mode: w.mode, outcome: w.outcome, binding: w.binding, policy: w.policy, measured: w.measured, state: state(w.state),
      observation: w.observation, observedArtifact: w.observedArtifact, evidence: w.evidence, candidate: w.candidate, acknowledgement: w.acknowledgement, retryAcknowledgement: w.retryAcknowledgement, qualified: w.qualified, treeBeforeStop: w.treeBeforeStop })),
    retainedRecords: records, retainedInventory: c.retained.inventory };
});
const packet = { schema: 1, evidence: 'actual-native-adapter-shared-router-synthetic', live: false, issueComplete: false, sourceCommit: commit,
  sourceHashes: r.sources, rawInputSha256: sha(bytes), collectorSha256: sha(readFileSync(import.meta.filename)), runtime: r.runtime,
  baseSHA: r.baseSHA, lifecycle: r.lifecycle, variant: r.variant, workerStaging: r.workerStaging, cases, cleanup: true, sourceIdentityVerified: true };
const text = JSON.stringify(packet, null, 2) + '\n';
assert(!/\/Users\/|\/var\/folders\/|synthetic_access_[0-9]|Bearer [a-f0-9]{64}|"accessToken"|"refreshToken"|"apiKey"/.test(text), 'private data in public projection');
writeFileSync(process.argv[3], text); console.log(JSON.stringify({ sourceCommit: commit, cases: cases.length, bytes: Buffer.byteLength(text), sha256: sha(Buffer.from(text)) }));
