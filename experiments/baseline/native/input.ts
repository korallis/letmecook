import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { keys } from '../../inference-boundary/json.ts';
import { settingsDigest, digest } from '../../harness/native/client.ts';
import { makeManifest, validateSnapshotBytes } from '../artifacts/manifest.ts';
import type { SnapshotFile } from '../execution-contract.ts';
export type WorkerInput = {
  schema: 1; kind: 'synthetic-baseline-worker'; caseId: 'synthetic-case01';
  registrationDigest: string; packetDigest: string; profileDigest: string; bindingDigest: string; settingsDigest: string;
  baseTreeDigest: string; context: string; contextDigest: string; prompt: string; promptDigest: string;
  files: SnapshotFile[]; token: string; deadline: number; outputBytes: number;
};
export const bytesDigest = (text: string) => createHash('sha256').update(text).digest('hex');
export function validateWorkerInput(input: WorkerInput, now = Date.now()) {
  const names = ['schema','kind','caseId','registrationDigest','packetDigest','profileDigest','bindingDigest','settingsDigest','baseTreeDigest','context','contextDigest','prompt','promptDigest','files','token','deadline','outputBytes'];
  keys(input, names, names);
  assert.equal(input.schema, 1); assert.equal(input.kind, 'synthetic-baseline-worker'); assert.equal(input.caseId, 'synthetic-case01');
  for (const k of ['registrationDigest','packetDigest','profileDigest','bindingDigest','settingsDigest','baseTreeDigest','contextDigest','promptDigest','token'] as const) assert.match(input[k], /^[a-f0-9]{64}$/);
  assert.equal(input.settingsDigest, settingsDigest('allow'));
  for (const k of ['context', 'prompt'] as const) assert(typeof input[k] === 'string' && Buffer.byteLength(input[k]) > 0 && Buffer.byteLength(input[k]) <= 16384);
  assert.equal(bytesDigest(input.context), input.contextDigest); assert.equal(bytesDigest(input.prompt), input.promptDigest);
  assert(input.prompt.endsWith('\n\nFrozen public context:\n' + input.context), 'worker_context_not_supplied');
  validateSnapshotBytes({ schema: 1, kind: 'baseline-snapshot-bytes', files: input.files });
  assert.equal(makeManifest(input.files).treeDigest, input.baseTreeDigest, 'worker_base_mismatch');
  assert(input.files.some(f => f.path === 'ci/first.yml') && input.files.some(f => f.path === 'ci/second.yml'));
  assert(Number.isSafeInteger(input.deadline) && input.deadline > now && input.deadline <= now + 600000, 'worker_deadline');
  assert(Number.isSafeInteger(input.outputBytes) && input.outputBytes > 0 && input.outputBytes <= 262144);
  return digest({ ...input, token: '<scoped-grant>' });
}
