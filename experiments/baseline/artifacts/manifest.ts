import assert from 'node:assert/strict';
import { CASE01_LIMITS, type CaseRules, type ManifestFile, type SnapshotFile, type SnapshotManifest, type SnapshotBytes } from '../execution-contract.ts';
import { canonicalJSON, exact, hashBytes } from './store.ts';

export function sourcePath(path: unknown): asserts path is string {
  assert(typeof path === 'string' && path.length > 0 && path.length <= 256 && path.split('/').every(s =>
    /^[A-Za-z0-9_@+.-]+$/.test(s) && !['.', '..', '.git'].includes(s.toLowerCase())), 'snapshot_path');
}
export function validateFile(value: unknown, content: boolean): asserts value is SnapshotFile {
  exact(value, 'path mode size sha256' + (content ? ' contentBase64' : ''));
  sourcePath(value.path); assert([0o644, 0o755].includes(value.mode), 'snapshot_mode');
  assert(Number.isSafeInteger(value.size) && value.size >= 0 && value.size <= CASE01_LIMITS.sourceBytes, 'snapshot_file_size');
  assert(typeof value.sha256 === 'string' && /^[a-f0-9]{64}$/.test(value.sha256), 'snapshot_file_digest');
  if (content) {
    assert(typeof value.contentBase64 === 'string' && value.contentBase64.length <= Math.ceil(CASE01_LIMITS.sourceBytes / 3) * 4, 'snapshot_content_limit');
    const bytes = Buffer.from(value.contentBase64, 'base64');
    assert(bytes.toString('base64') === value.contentBase64 && bytes.length === value.size && hashBytes(bytes) === value.sha256, 'snapshot_content_mismatch');
  }
}
function inventory(files: unknown, content: boolean): asserts files is SnapshotFile[] {
  assert(Array.isArray(files) && files.length <= CASE01_LIMITS.files, 'snapshot_file_count');
  files.forEach(f => validateFile(f, content));
  const paths = files.map(f => f.path); assert(new Set(paths.map(p => p.toLowerCase())).size === paths.length, 'snapshot_duplicate_path');
  assert(files.reduce((n, f) => n + f.size, 0) <= CASE01_LIMITS.sourceBytes, 'snapshot_source_bytes');
  const folded = paths.map(p => p.toLowerCase());
  assert(!folded.some(p => folded.some(other => other.startsWith(p + '/'))), 'snapshot_file_directory_collision');
  const spellings = new Map<string, string>();
  for (const path of paths) for (let i = 1; i <= path.split('/').length; i++) {
    const prefix = path.split('/').slice(0, i).join('/'), previous = spellings.get(prefix.toLowerCase());
    assert(previous === undefined || previous === prefix, 'snapshot_case_alias'); spellings.set(prefix.toLowerCase(), prefix);
  }
}
const sortFiles = <T extends ManifestFile>(files: T[]) => [...files].sort((a, b) => a.path < b.path ? -1 : a.path > b.path ? 1 : 0);
export function makeManifest(files: readonly (ManifestFile | SnapshotFile)[]): SnapshotManifest {
  assert(Array.isArray(files) && files.length <= CASE01_LIMITS.files, 'snapshot_file_count');
  files.forEach(f => validateFile(f, Object.hasOwn(f, 'contentBase64')));
  const manifestFiles = sortFiles(files.map(({ path, mode, size, sha256 }) => ({ path, mode, size, sha256 })));
  inventory(manifestFiles, false);
  return { schema: 1, files: manifestFiles, treeDigest: hashBytes(canonicalJSON(manifestFiles)) };
}
export function validateManifest(value: unknown): asserts value is SnapshotManifest {
  exact(value, 'schema files treeDigest'); assert.equal(value.schema, 1); inventory(value.files, false);
  assert.deepEqual(value, makeManifest(value.files), 'snapshot_manifest_mismatch');
}
export function validateSnapshotBytes(value: unknown): asserts value is SnapshotBytes {
  exact(value, 'schema kind files'); assert.equal(value.schema, 1); assert.equal(value.kind, 'baseline-snapshot-bytes');
  inventory(value.files, true); assert.deepEqual(value.files, sortFiles(value.files), 'snapshot_inventory_order');
}
export function validateRules(rules: CaseRules, base: SnapshotManifest) {
  exact(rules, 'schema caseId registrationDigest readPaths writePaths'); assert.equal(rules.schema, 1);
  assert(typeof rules.caseId === 'string' && /^[a-z][a-z0-9_-]{0,63}$/.test(rules.caseId), 'snapshot_case');
  assert(typeof rules.registrationDigest === 'string' && /^[a-f0-9]{64}$/.test(rules.registrationDigest), 'snapshot_registration');
  assert(Array.isArray(rules.readPaths) && rules.readPaths.length > 0 && rules.readPaths.length <= CASE01_LIMITS.files, 'snapshot_read_paths');
  assert(Array.isArray(rules.writePaths) && rules.writePaths.length === 2, 'case01_two_existing_writes');
  for (const paths of [rules.readPaths, rules.writePaths]) {
    paths.forEach(sourcePath); assert.equal(new Set(paths).size, paths.length, 'snapshot_duplicate_rule');
    assert(paths.every(p => base.files.some(f => f.path === p)), 'snapshot_rule_not_in_base');
  }
}
export function changedPaths(base: SnapshotManifest, candidate: SnapshotManifest, rules: CaseRules) {
  validateManifest(base); validateManifest(candidate); validateRules(rules, base);
  assert.deepEqual(candidate.files.map(f => [f.path, f.mode]), base.files.map(f => [f.path, f.mode]), 'case01_inventory_or_mode_changed');
  const changed = candidate.files.filter((f, i) => f.sha256 !== base.files[i].sha256 || f.size !== base.files[i].size).map(f => f.path);
  assert(changed.every(p => rules.writePaths.includes(p)), 'case01_unrelated_change'); return changed;
}
