// Retain complete legacy observations and exact source identity after execution.
// The explicit review-failure mode preserves a historical failure without
// pretending its source is the current checkout or its zero cases passed.
import assert from 'node:assert/strict';
import { readFileSync, writeFileSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { resolve } from 'node:path';
const root = resolve(import.meta.dirname, '../..');
const sha = (bytes: Uint8Array) => createHash('sha256').update(bytes).digest('hex');
const [input, output, commit, kind] = process.argv.slice(2);
assert(/^[a-f0-9]{40}$/.test(commit));
assert(['router', 'fixture', 'review-failure'].includes(kind));
const bytes = readFileSync(input), observation = JSON.parse(bytes.toString());
assert.equal(observation.cleanup.verified, true);
if (kind === 'review-failure') {
  assert.equal(observation.result, 'blocked'); assert.equal(observation.cases.length, 0);
  assert.match(observation.error, /ERR_MODULE_NOT_FOUND.*Cannot find module '\/router-authority-extension\/overlay\/native-profile\.mjs'/s);
} else {
  assert(['selected-cases-passed', 'synthetic-harness-and-containment-passed', 'passed'].includes(observation.result));
  assert(observation.cases.length > 0); assert.equal(observation.error, undefined);
}
if (kind === 'fixture') {
  assert.equal(observation.syntheticInferenceOnly, true); assert.equal(observation.liveRouterCalled, false);
} else {
  assert.equal(observation.realProviderCalled, false); assert.equal(observation.privateDataUsed, false); assert.equal(observation.liveAdmission, false);
}
const hashes = observation.sources ?? observation.sourceDigests;
assert(hashes && Object.keys(hashes).length);
for (const [path, expected] of Object.entries(hashes)) {
  assert.equal(sha(execFileSync('git', ['show', commit + ':' + path], { cwd: root, maxBuffer: 8 * 1048576 })), expected, 'committed source: ' + path);
  if (kind !== 'review-failure') assert.equal(sha(readFileSync(resolve(root, path))), expected, 'source after run: ' + path);
}
const packet = { schema: 1, evidence: 'legacy-harness-closure-' + kind, sourceCommit: commit,
  sourceIdentityVerified: true, currentSourceVerifiedAfterRun: kind !== 'review-failure',
  rawInputSha256: sha(bytes), collectorSha256: sha(readFileSync(import.meta.filename)),
  live: false, issueComplete: false, observation };
const text = JSON.stringify(packet, null, 2) + '\n';
assert(!/\/Users\/|\/var\/folders\/|synthetic_access_[0-9]|Bearer [a-f0-9]{64}|"accessToken"|"refreshToken"|"apiKey"/.test(text), 'private data in public projection');
writeFileSync(output, text);
console.log(JSON.stringify({ kind, sourceCommit: commit, cases: observation.cases.length, sources: Object.keys(hashes).length, bytes: Buffer.byteLength(text), sha256: sha(Buffer.from(text)) }));
