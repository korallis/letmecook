import assert from 'node:assert/strict';
import type { CandidateBundle, CaseRules, EvidenceStore, Revision, SnapshotBytes, SnapshotManifest, SnapshotRef } from '../execution-contract.ts';
import { canonicalJSON, exact, readEvidence, retainEvidence, validateEvidenceRef } from './store.ts';
import { changedPaths, makeManifest, validateManifest, validateRules, validateSnapshotBytes } from './manifest.ts';
import { parseArchive } from './archive.ts';

export { retainEvidence, readEvidence } from './store.ts';
export { makeManifest } from './manifest.ts';

function copyRef(ref: SnapshotRef): SnapshotRef {
  exact(ref, 'manifest bytes treeDigest'); validateEvidenceRef(ref.manifest); validateEvidenceRef(ref.bytes);
  assert(typeof ref.treeDigest === 'string' && /^[a-f0-9]{64}$/.test(ref.treeDigest), 'snapshot_reference_tree_digest');
  return structuredClone(ref);
}
/** Verify both content-addressed records, including every file's bytes and mode. */
export async function readSnapshot(store: EvidenceStore, ref: SnapshotRef): Promise<{ manifest: SnapshotManifest; bytes: SnapshotBytes }> {
  exact(store, 'directory'); store = { ...store }; ref = copyRef(ref);
  const manifest = await readEvidence(store, ref.manifest), bytes = await readEvidence(store, ref.bytes);
  validateManifest(manifest); validateSnapshotBytes(bytes);
  assert.deepEqual(makeManifest(bytes.files), manifest, 'snapshot_bytes_manifest_mismatch');
  assert.equal(ref.treeDigest, manifest.treeDigest, 'snapshot_reference_tree_mismatch');
  return { manifest, bytes };
}

function textWrites(bytes: SnapshotBytes, rules: CaseRules) {
  for (const path of rules.writePaths) {
    const file = bytes.files.find(f => f.path === path); assert(file, 'snapshot_write_file_missing');
    const text = new TextDecoder('utf-8', { fatal: true }).decode(Buffer.from(file.contentBase64, 'base64'));
    assert(!text.includes('\0'), 'case01_write_file_not_text');
  }
}

/** Capture only a frozen Docker export. Read permissions never filter the inventory. */
export async function captureSnapshot(archive: Uint8Array, baseManifest: SnapshotManifest,
  rules: CaseRules, store: EvidenceStore, revision: Revision): Promise<SnapshotRef> {
  validateManifest(baseManifest); validateRules(rules, baseManifest);
  assert(revision === 'base' || revision === 'candidate', 'snapshot_revision');
  baseManifest = structuredClone(baseManifest); rules = structuredClone(rules);
  exact(store, 'directory'); store = { ...store };
  const bytes: SnapshotBytes = { schema: 1, kind: 'baseline-snapshot-bytes', files: await parseArchive(archive) };
  validateSnapshotBytes(bytes); textWrites(bytes, rules);
  const manifest = makeManifest(bytes.files);
  if (revision === 'base') assert.deepEqual(manifest, baseManifest, 'snapshot_base_mismatch');
  else changedPaths(baseManifest, manifest, rules);
  const ref: SnapshotRef = {
    manifest: await retainEvidence(store, 'snapshot-manifest', manifest),
    bytes: await retainEvidence(store, 'snapshot-bytes', bytes),
    treeDigest: manifest.treeDigest,
  };
  await readSnapshot(store, ref);
  return ref;
}

/** Complete two-file candidate bundle. This is evidence, never an execution/acceptance grant. */
export async function retainCandidate(base: SnapshotRef, candidate: SnapshotRef, rules: CaseRules, store: EvidenceStore) {
  base = copyRef(base); candidate = copyRef(candidate); rules = JSON.parse(canonicalJSON(rules));
  exact(store, 'directory'); store = { ...store };
  const b = await readSnapshot(store, base), c = await readSnapshot(store, candidate);
  const changed = changedPaths(b.manifest, c.manifest, rules);
  assert.deepEqual([...changed].sort(), [...rules.writePaths].sort(), 'case01_incomplete_two_file_candidate');
  textWrites(b.bytes, rules); textWrites(c.bytes, rules);
  const bundle: CandidateBundle = { schema: 1, kind: 'baseline-repository-bundle', caseId: rules.caseId,
    registrationDigest: rules.registrationDigest, base, candidate, changedPaths: changed };
  const ref = await retainEvidence(store, 'candidate-bundle', bundle);
  assert.deepEqual(await readEvidence(store, ref), bundle, 'candidate_readback_mismatch');
  await readSnapshot(store, base); await readSnapshot(store, candidate);
  return ref;
}
