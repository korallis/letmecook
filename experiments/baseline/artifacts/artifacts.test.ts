import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import type { CaseRules, EvidenceStore, SnapshotFile } from '../execution-contract.ts';
import { captureSnapshot, makeManifest, readEvidence, readSnapshot, retainCandidate, retainEvidence } from './index.ts';
import { canonicalJSON, hashBytes } from './store.ts';

// Public synthetic fixtures only. No extraction, Docker, Git controls or inference needed.
type Entry = { name: string; content?: Uint8Array | string; mode?: number; type?: string };
function tar(entries: Entry[]): Buffer {
  return Buffer.concat([...entries.flatMap(({ name, content = '', mode = 0o644, type = '0' }) => {
    const bytes = Buffer.from(content), header = Buffer.alloc(512);
    header.write(name, 0, 100); header.write(mode.toString(8).padStart(7, '0') + '\0', 100, 8);
    header.write(bytes.length.toString(8).padStart(11, '0') + '\0', 124, 12);
    header.fill(32, 148, 156); header.write(type, 156, 1); header.write('ustar\0', 257, 6); header.write('00', 263, 2);
    header.write(header.reduce((a, b) => a + b, 0).toString(8).padStart(6, '0') + '\0 ', 148, 8);
    return [header, bytes, Buffer.alloc((512 - bytes.length % 512) % 512)];
  }), Buffer.alloc(1024)]);
}
const file = (path: string, content: string, mode: 0o644 | 0o755 = 0o644): SnapshotFile => ({
  path, mode, size: Buffer.byteLength(content), sha256: hashBytes(content), contentBase64: Buffer.from(content).toString('base64'),
});
const files = [file('a.ts', 'export const a = 1;\n'), file('b.ts', 'export const b = 2;\n'), file('unread.txt', 'preserve me\n')];
const manifest = makeManifest(files);
const rules: CaseRules = { schema: 1, caseId: 'case01', registrationDigest: 'a'.repeat(64), readPaths: ['a.ts'], writePaths: ['a.ts', 'b.ts'] };
const archive = (input = files) => tar([{ name: 'repo/', type: '5', mode: 0o755 }, ...input.map(f => ({ name: 'repo/' + f.path, mode: f.mode, content: Buffer.from(f.contentBase64, 'base64') }))]);
async function withStore(run: (store: EvidenceStore) => Promise<void>) {
  const directory = await mkdtemp(join(tmpdir(), 'gaffer-artifacts-'));
  try { await run({ directory }); } finally { await rm(directory, { recursive: true, force: true }); }
}

test('canonical durable evidence is content addressed and corruption is rejected', async () => withStore(async store => {
  const value = JSON.parse('{"z":1,"2":2,"10":10,"__proto__":{"safe":true}}');
  const ref = await retainEvidence(store, 'observation', value);
  assert.equal(canonicalJSON(value), '{"10":10,"2":2,"__proto__":{"safe":true},"z":1}');
  assert.equal(await readFile(join(store.directory, ref.ref), 'utf8'), canonicalJSON(value));
  assert.equal(ref.sha256, hashBytes(canonicalJSON(value)));
  assert.deepEqual(await retainEvidence(store, 'observation', value), ref);
  assert.deepEqual(await readEvidence(store, ref), value);
  await writeFile(join(store.directory, ref.ref), '{}');
  await assert.rejects(readEvidence(store, ref), /evidence_digest_mismatch/);
  await assert.rejects(retainEvidence(store, 'observation', value), /evidence_content_changed/);
  assert.equal(await readFile(join(store.directory, ref.ref), 'utf8'), '{}');
}));

test('snapshot captures full bytes and two-file candidate, rejecting unrelated changes', async () => withStore(async store => {
  const base = await captureSnapshot(archive(), manifest, rules, store, 'base');
  const restored = await readSnapshot(store, base);
  assert.deepEqual(restored.manifest, manifest); assert.deepEqual(restored.bytes.files, files);
  const candidateFiles = [file('a.ts', 'export const a = 3;\n'), file('b.ts', 'export const b = 4;\n'), files[2]];
  const candidate = await captureSnapshot(archive(candidateFiles), manifest, rules, store, 'candidate');
  const bundle = await readEvidence(store, await retainCandidate(base, candidate, rules, store));
  assert.deepEqual(bundle, { schema: 1, kind: 'baseline-repository-bundle', caseId: rules.caseId,
    registrationDigest: rules.registrationDigest, base, candidate, changedPaths: ['a.ts', 'b.ts'] });
  await assert.rejects(retainCandidate(base, base, rules, store), /case01_incomplete_two_file_candidate/);
  await assert.rejects(captureSnapshot(archive([...candidateFiles.slice(0, 2), file('unread.txt', 'changed')]), manifest, rules, store, 'candidate'), /case01_unrelated_change/);
  await assert.rejects(readSnapshot(store, { ...candidate, bytes: base.bytes }), /snapshot_bytes_manifest_mismatch/);
}));
