import assert from 'node:assert/strict';
import { test } from 'node:test';
import { chmod, link, lstat, mkdir, mkdtemp, readFile, readdir, rename, rm, symlink, unlink, writeFile } from 'node:fs/promises';
import { execFileSync } from 'node:child_process';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { CASE01_LIMITS, type CaseRules, type EvidenceStore, type SnapshotFile } from '../execution-contract.ts';
import { captureSnapshot, makeManifest, readEvidence, readSnapshot, retainCandidate, retainEvidence } from './index.ts';
import { canonicalJSON, hashBytes, writeRecord } from './store.ts';
import { CONTROL_LIMITS, parseArchive } from './archive.ts';

// Public synthetic fixtures only. No extraction, Docker, Git controls or inference needed.
type Entry = { name: string; content?: Uint8Array | string; mode?: number; type?: string; link?: string; prefix?: string };
function checksum(header: Buffer) {
  header.fill(32, 148, 156);
  header.write(header.subarray(0, 512).reduce((a, b) => a + b, 0).toString(8).padStart(6, '0') + '\0 ', 148, 8);
}
function tar(entries: Entry[]): Buffer {
  return Buffer.concat([...entries.flatMap(({ name, content = '', mode = 0o644, type = '0', link = '', prefix = '' }) => {
    const bytes = Buffer.from(content), header = Buffer.alloc(512);
    header.write(name, 0, 100); header.write(mode.toString(8).padStart(7, '0') + '\0', 100, 8);
    header.write(bytes.length.toString(8).padStart(11, '0') + '\0', 124, 12);
    header.write(type, 156, 1); header.write(link, 157, 100); header.write('ustar\0', 257, 6); header.write('00', 263, 2);
    header.write(prefix, 345, 155); checksum(header);
    return [header, bytes, Buffer.alloc((512 - bytes.length % 512) % 512)];
  }), Buffer.alloc(1024)]);
}
const file = (path: string, content: string, mode: 0o644 | 0o755 = 0o644): SnapshotFile => ({
  path, mode, size: Buffer.byteLength(content), sha256: hashBytes(content), contentBase64: Buffer.from(content).toString('base64'),
});
const files = [file('a.ts', 'export const a = 1;\n'), file('b.ts', 'export const b = 2;\n'), file('unread.txt', 'preserve me\n')];
const manifest = makeManifest(files);
const rules: CaseRules = { schema: 1, caseId: 'case01', registrationDigest: 'a'.repeat(64), readPaths: ['a.ts'], writePaths: ['a.ts', 'b.ts'] };
const root: Entry = { name: 'repo/', type: '5', mode: 0o755 };
const archive = (input = files) => tar([root, ...input.map(f => ({ name: 'repo/' + f.path, mode: f.mode, content: Buffer.from(f.contentBase64, 'base64') }))]);
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

test('JSON and store trust boundaries reject unsupported data before retention', async () => withStore(async store => {
  let getterRan = false;
  const accessor = { get secret() { getterRan = true; return 1; } };
  const cyclic: any = {}; cyclic.self = cyclic;
  const hidden = Object.defineProperty({}, 'hidden', { value: 1 });
  const arrayProperty = Object.assign([1], { extra: 2 });
  const deep = Array.from({ length: 33 }).reduce<unknown>(v => [v], null);
  for (const value of [undefined, NaN, Infinity, 1n, new Date(), () => {}, [undefined], Array(2), cyclic, accessor, hidden, arrayProperty,
    { [Symbol('hidden')]: 1 }, { text: 'x'.repeat(CASE01_LIMITS.artifactBytes) }, deep, Array(100000).fill(null),
    Array(20).fill('x'.repeat(65536)), { ['x'.repeat(CASE01_LIMITS.artifactBytes)]: true }]) {
    await assert.rejects(retainEvidence(store, 'invalid', value));
  }
  assert.equal(getterRan, false); assert.deepEqual(await readdir(store.directory), []);
  for (const kind of ['../escape', 'Bad', '', 'x'.repeat(49)]) await assert.rejects(retainEvidence(store, kind, {}), /evidence_kind/);
  for (const directory of ['relative', store.directory + '/', store.directory + '/../elsewhere']) {
    await assert.rejects(retainEvidence({ directory }, 'test', {}), /evidence_directory_path/);
  }
  await assert.rejects(readEvidence(store, { ref: '../escape.json', sha256: 'a'.repeat(64) }), /evidence_reference/);
}));

test('evidence accepts the exact byte ceiling and rejects escaping beyond it', async () => withStore(async store => {
  const value = 'x'.repeat(CASE01_LIMITS.artifactBytes - 2), ref = await retainEvidence(store, 'limit', value);
  assert.equal((await lstat(join(store.directory, ref.ref))).size, CASE01_LIMITS.artifactBytes);
  assert.equal(await readEvidence(store, ref), value);
  assert.throws(() => canonicalJSON(value + 'x'), /evidence_bytes_limit/);
  assert.throws(() => canonicalJSON('\0'.repeat(CASE01_LIMITS.artifactBytes / 2)), /evidence_bytes_limit/);
}));

test('retained files reject symlinks, hardlinks, permissions and noncanonical JSON', async () => withStore(async store => {
  const ref = await retainEvidence(store, 'record', { ok: true }), path = join(store.directory, ref.ref);
  assert.equal((await lstat(path)).mode & 0o7777, 0o600); assert.equal((await lstat(path)).nlink, 1);
  await symlink(store.directory, join(store.directory, 'alias'));
  await assert.rejects(readEvidence({ directory: join(store.directory, 'alias') }, ref));
  await assert.rejects(readEvidence({ directory: join(store.directory, 'alias') + '/' }, ref), /evidence_directory_path/);
  await rename(path, path + '.original'); await symlink(path + '.original', path);
  await assert.rejects(readEvidence(store, ref)); await assert.rejects(retainEvidence(store, 'record', { ok: true }));
  await unlink(path); await rename(path + '.original', path);
  await link(path, path + '.hard'); await assert.rejects(readEvidence(store, ref), /private_evidence_file/); await unlink(path + '.hard');
  await chmod(path, 0o644); await assert.rejects(readEvidence(store, ref), /private_evidence_file/); await chmod(path, 0o600);
  await chmod(store.directory, 0o755); await assert.rejects(readEvidence(store, ref), /private_evidence_directory/); await chmod(store.directory, 0o700);
  for (const bytes of [Buffer.from('{ "ok": true }'), Buffer.from('{"ok":true,"ok":true}'), Buffer.from([0xff]), Buffer.alloc(CASE01_LIMITS.artifactBytes + 1)]) {
    const sha256 = hashBytes(bytes), bad = { ref: 'record-' + sha256 + '.json', sha256 };
    await writeFile(join(store.directory, bad.ref), bytes, { mode: 0o600 });
    await assert.rejects(readEvidence(store, bad));
  }
}));

test('failed durability checkpoints never acknowledge and can be retried', async t => {
  for (const phase of ['file-synced', 'linked', 'directory-synced'] as const) await t.test(phase, async () => withStore(async store => {
    await assert.rejects(writeRecord(store, 'fault', { phase }, async current => { if (current === phase) throw new Error('injected_failure'); }), /injected_failure/);
    const ref = await retainEvidence(store, 'fault', { phase });
    assert.deepEqual(await readEvidence(store, ref), { phase });
    assert.equal((await lstat(join(store.directory, ref.ref))).nlink, 1);
  }));
});

test('retention detects content, inode and directory substitution around durability', async t => {
  for (const phase of ['file-synced', 'linked', 'directory-synced'] as const) {
    for (const attack of ['content', 'inode', 'directory'] as const) await t.test(phase + '/' + attack, async () => withStore(async outer => {
      const directory = join(outer.directory, 'store'); await mkdir(directory, { mode: 0o700 });
      await assert.rejects(writeRecord({ directory }, 'fault', { ok: true }, async (current, path) => {
        if (current !== phase) return;
        if (attack === 'content') await writeFile(path, '{"ok":null}');
        if (attack === 'inode') { await rename(path, path + '.original'); await writeFile(path, '{"ok":true}', { mode: 0o600 }); }
        if (attack === 'directory') { await rename(directory, directory + '.original'); await mkdir(directory, { mode: 0o700 }); }
      }), /evidence_content_changed|evidence_identity_changed|ENOENT/);
    }));
  }
});

test('async operations bind store, rules, manifests and references at entry', async () => withStore(async store => {
  const valueRef = await retainEvidence(store, 'record', { original: true });
  const mutableStore = { ...store }, mutableRef = { ...valueRef };
  const reading = readEvidence(mutableStore, mutableRef);
  mutableStore.directory += '/missing'; mutableRef.sha256 = '0'.repeat(64); mutableRef.ref = 'record-' + mutableRef.sha256 + '.json';
  assert.deepEqual(await reading, { original: true });
  const mutableRules = structuredClone(rules), mutableManifest = structuredClone(manifest), captureStore = { ...store };
  const mutableArchive = archive(), capturing = captureSnapshot(mutableArchive, mutableManifest, mutableRules, captureStore, 'base');
  mutableArchive.fill(0); mutableRules.writePaths = ['a.ts', 'unread.txt']; mutableManifest.files[2] = file('unread.txt', 'replacement'); captureStore.directory += '/missing';
  const base = await capturing;
  assert.deepEqual((await readSnapshot(store, base)).manifest, manifest);
  const swapped = structuredClone(base), pending = readSnapshot(store, swapped); swapped.bytes = valueRef;
  assert.deepEqual((await pending).manifest, manifest);
  const candidate = await captureSnapshot(archive([file('a.ts', 'new a'), file('b.ts', 'new b'), files[2]]), manifest, rules, store, 'candidate');
  const bundleBase = structuredClone(base), bundleCandidate = structuredClone(candidate), bundleRules = structuredClone(rules);
  const retaining = retainCandidate(bundleBase, bundleCandidate, bundleRules, store);
  bundleBase.treeDigest = '0'.repeat(64); bundleCandidate.bytes = valueRef; bundleRules.writePaths = [];
  const bundle = await readEvidence(store, await retaining) as any;
  assert.deepEqual(bundle.base, base); assert.deepEqual(bundle.candidate, candidate);
}));

test('manifest rejects ambiguous paths, invalid bytes, modes and inventory limits', () => {
  for (const path of ['', '/absolute', '../escape', 'a/../b', 'a//b', './a', 'a\\b', 'a b', '.git/config', 'a/.GIT/config', 'a'.repeat(257)]) {
    assert.throws(() => makeManifest([file(path, '')]), /snapshot_path/);
  }
  for (const paths of [['a', 'a'], ['A', 'a'], ['a', 'a/b'], ['A', 'a/b'], ['A/x', 'a/y']]) assert.throws(() => makeManifest(paths.map(p => file(p, ''))));
  for (const change of [{ mode: 0o600 }, { size: -1 }, { size: 2 }, { sha256: 'bad' }, { contentBase64: 'Zg' }, { contentBase64: 'AB==' }, { extra: true }]) {
    assert.throws(() => makeManifest([{ ...file('a', 'x'), ...change } as SnapshotFile]));
  }
  assert.throws(() => makeManifest(Array.from({ length: 65 }, (_, i) => file('f' + i, ''))), /snapshot_file_count/);
  assert.throws(() => makeManifest([file('a', 'a'.repeat(40000)), file('b', 'b'.repeat(30000))]), /snapshot_source_bytes/);
  const reversed = [...files].reverse(), before = structuredClone(reversed);
  assert.deepEqual(makeManifest(reversed), manifest); assert.deepEqual(reversed, before);
});

function paxRecord(key: string, value: string) {
  const body = key + '=' + value + '\n'; let length = Buffer.byteLength(body) + 2;
  while (Buffer.byteLength(length + ' ' + body) !== length) length = Buffer.byteLength(length + ' ' + body);
  return length + ' ' + body;
}
test('USTAR, PAX, GNU names, binary bytes and bounded Git controls retain a complete inventory', async () => {
  const expected = file('src/main.ts', 'hello');
  assert.deepEqual(await parseArchive(tar([root, { name: 'main.ts', prefix: 'repo/src', content: 'hello' }])), [expected]);
  assert.deepEqual(await parseArchive(tar([{ name: './', type: '5' }, { name: './src/main.ts', content: 'hello' }])), [expected]);
  const long = 'src/' + 'x'.repeat(120) + '.ts';
  for (const metadata of [
    { name: 'PaxHeader', type: 'x', content: paxRecord('path', 'repo/' + long) },
    { name: '././@LongLink', type: 'L', content: 'repo/' + long + '\0' },
  ]) assert.deepEqual(await parseArchive(tar([root, metadata, { name: 'placeholder', content: 'hello' }])), [file(long, 'hello')]);
  const sized = tar([root, { name: 'PaxHeader', type: 'x', content: paxRecord('size', '5') }, { name: 'repo/a', content: 'hello' }]);
  sized.write('00000000000\0', 1536 + 124, 12); checksum(sized.subarray(1536, 2048));
  assert.deepEqual(await parseArchive(sized), [file('a', 'hello')]);
  const gnu = tar([root, { name: '././@LongLink', type: 'L', content: 'repo/' + long + '\0' }, { name: 'placeholder', content: 'hello' }]);
  for (const offset of [0, 512, 1536]) { gnu.write('ustar  \0', offset + 257, 8); checksum(gnu.subarray(offset, offset + 512)); }
  assert.deepEqual(await parseArchive(gnu), [file(long, 'hello')]);
  const binary = Buffer.from([0, 1, 0xff, 0x80]);
  const parsed = await parseArchive(tar([root, { name: 'repo/.git/', type: '5' }, { name: 'repo/.git/HEAD', content: 'ref: refs/heads/main\n' },
    { name: 'repo/.env', content: 'PUBLIC_SYNTHETIC=1' }, { name: 'repo/binary', content: binary, mode: 0o755 }]));
  assert.deepEqual(parsed.map(f => f.path), ['.env', 'binary']); assert.equal(parsed[1].contentBase64, binary.toString('base64')); assert.equal(parsed[1].mode, 0o755);
});

test('malformed and dangerous archives fail closed', async t => {
  const cases: [string, Entry[]][] = [
    ['missing root', [{ name: 'repo/a' }]], ['traversal root', [{ name: '../', type: '5' }]],
    ['duplicate', [root, { name: 'repo/a' }, { name: 'repo/a' }]], ['case duplicate', [root, { name: 'repo/A' }, { name: 'repo/a' }]],
    ['file parent', [root, { name: 'repo/a' }, { name: 'repo/a/b' }]], ['file after child', [root, { name: 'repo/a/b' }, { name: 'repo/a' }]],
    ['directory content', [root, { name: 'repo/a/', type: '5', content: 'x' }]],
    ['privileged mode', [root, { name: 'repo/a', mode: 0o4755 }]], ['unsupported source mode', [root, { name: 'repo/a', mode: 0o600 }]],
    ['link target', [root, { name: 'repo/a', link: 'b' }]], ['git file', [root, { name: 'repo/.git', content: 'gitdir: /outside' }]],
    ['ignored control symlink', [root, { name: 'repo/.git/a', type: '2' }]],
    ['dangling pax', [root, { name: 'PaxHeader', type: 'x', content: paxRecord('path', 'repo/a') }]],
    ['pax unknown key', [root, { name: 'PaxHeader', type: 'x', content: paxRecord('GNU.sparse.size', '5') }, { name: 'repo/a' }]],
    ['pax xattr', [root, { name: 'PaxHeader', type: 'x', content: paxRecord('SCHILY.xattr.user.synthetic', 'public') }, { name: 'repo/a' }]],
    ['pax duplicate', [root, { name: 'PaxHeader', type: 'x', content: paxRecord('path', 'repo/a').repeat(2) }, { name: 'repo/a' }]],
    ['pax traversal', [root, { name: 'PaxHeader', type: 'x', content: paxRecord('path', 'repo/../escape') }, { name: 'repo/a' }]],
    ['pax invalid length', [root, { name: 'PaxHeader', type: 'x', content: '999 path=repo/a\n' }, { name: 'repo/a' }]],
    ['repeated metadata', [root, { name: 'PaxHeader', type: 'x', content: paxRecord('path', 'repo/a') }, { name: 'Long', type: 'L', content: 'repo/a\0' }, { name: 'repo/a' }]],
    ['longname missing nul', [root, { name: 'Long', type: 'L', content: 'repo/a' }, { name: 'repo/a' }]],
  ];
  for (const name of ['other/a', '/repo/a', 'repo/../a', 'repo/a//b', 'repo/a\\b', 'repo/a b', 'repo/a/.git/config', 'repo/.GIT/config']) cases.push([name, [root, { name }]]);
  for (const type of ['1', '2', '3', '4', '6', '7', 'S', 'g', 'K']) cases.push(['special type ' + type, [root, { name: 'repo/a', type }]]);
  for (const [name, entries] of cases) await t.test(name, async () => { await assert.rejects(parseArchive(tar(entries))); });
  const valid = tar([root, { name: 'repo/a', content: 'x' }]);
  const checksumBad = Buffer.from(valid); checksumBad[0] ^= 1;
  const paddingBad = Buffer.from(valid); paddingBad[1025] = 1;
  const binarySize = Buffer.from(valid); binarySize[512 + 124] = 128; checksum(binarySize.subarray(512, 1024));
  const badMagic = Buffer.from(valid); badMagic[257] |= 128; checksum(badMagic);
  const badVersion = Buffer.from(valid); badVersion[263] = 49; checksum(badVersion);
  const badField = Buffer.from(valid); badField[99] = 1; checksum(badField);
  const badPax = Buffer.from(paxRecord('path', 'repo/a')); badPax[0] |= 128;
  await assert.rejects(parseArchive(tar([root, { name: 'PaxHeader', type: 'x', content: badPax }, { name: 'repo/a' }])));
  for (const bytes of [Buffer.alloc(1024), valid.subarray(1), valid.subarray(0, -1024), valid.subarray(0, -512),
    Buffer.concat([valid, valid]), checksumBad, paddingBad, binarySize, badMagic, badVersion, badField]) await assert.rejects(parseArchive(bytes));
});

test('archive inventory, source, controls, metadata and buffered byte limits are enforced', async () => {
  const atLimit = Array.from({ length: CASE01_LIMITS.files }, (_, i) => ({ name: 'repo/f' + i, content: i ? '' : 'x'.repeat(CASE01_LIMITS.sourceBytes) }));
  assert.equal((await parseArchive(tar([root, ...atLimit]))).length, 64);
  const cases = [
    tar([root, ...atLimit, { name: 'repo/extra' }]), tar([root, { name: 'repo/a', content: 'x'.repeat(CASE01_LIMITS.sourceBytes + 1) }]),
    tar([root, ...Array.from({ length: CONTROL_LIMITS.directories + 1 }, (_, i) => ({ name: 'repo/d' + i + '/', type: '5' }))]),
    tar([root, ...Array.from({ length: CONTROL_LIMITS.entries + 1 }, (_, i) => ({ name: 'repo/.git/f' + i }))]),
    tar([root, { name: 'repo/.git/large', content: Buffer.alloc(CONTROL_LIMITS.bytes + 1) }]),
    tar([root, { name: 'PaxHeader', type: 'x', content: 'x'.repeat(CONTROL_LIMITS.metadataBytes + 1) }]),
    tar([root, ...Array.from({ length: CONTROL_LIMITS.metadataEntries + 1 }, (_, i) => [
      { name: 'PaxHeader', type: 'x', content: paxRecord('path', 'repo/.git/f' + i) }, { name: 'placeholder' },
    ]).flat()]),
    Buffer.alloc(CASE01_LIMITS.archiveBytes + 512),
  ];
  for (const bytes of cases) await assert.rejects(parseArchive(bytes));
  const bytes = archive(), padded = Buffer.alloc(CASE01_LIMITS.archiveBytes); bytes.copy(padded);
  assert.deepEqual(await parseArchive(new Uint8Array(padded)), files);
  await assert.rejects(parseArchive(new Uint8Array(CASE01_LIMITS.archiveBytes + 1)), /archive_bytes_limit/);
  let consumed = false;
  const stream = { async *[Symbol.asyncIterator]() { consumed = true; yield bytes; } };
  for (const input of [null, {}, 'invalid', bytes.buffer, stream]) await assert.rejects(parseArchive(input as any), /archive_input/);
  assert.equal(consumed, false);
});

test('snapshot admission rejects additions, deletions, modes, binary writes and partial candidates', async () => withStore(async store => {
  const base = await captureSnapshot(archive(), manifest, rules, store, 'base');
  for (const input of [files.slice(0, 2), [...files, file('new', '')], [{ ...files[0], mode: 0o755 as const }, ...files.slice(1)],
    [file('a.ts', '\0'), ...files.slice(1)], [{ ...file('a.ts', ''), size: 1, sha256: hashBytes(Buffer.from([0xff])), contentBase64: '/w==' }, ...files.slice(1)]]) {
    await assert.rejects(captureSnapshot(archive(input), manifest, rules, store, 'candidate'));
  }
  const partial = await captureSnapshot(archive([file('a.ts', 'changed'), ...files.slice(1)]), manifest, rules, store, 'candidate');
  await assert.rejects(retainCandidate(base, partial, rules, store), /case01_incomplete_two_file_candidate/);
  await assert.rejects(captureSnapshot(archive([file('a.ts', 'changed'), ...files.slice(1)]), manifest, rules, store, 'base'), /snapshot_base_mismatch/);
  for (const change of [{ writePaths: ['a.ts'] }, { writePaths: ['a.ts', 'a.ts'] }, { writePaths: ['a.ts', 'missing'] }, { readPaths: [] }, { readPaths: ['missing'] }, { registrationDigest: 'bad' }, { schema: 2 }]) {
    await assert.rejects(captureSnapshot(archive(), manifest, { ...rules, ...change } as CaseRules, store, 'base'));
  }
  await assert.rejects(readSnapshot(store, { ...base, treeDigest: '0'.repeat(64) }), /snapshot_reference_tree_mismatch/);
}));

test('native tar USTAR and PAX exports roundtrip public synthetic files', async () => withStore(async store => {
  const directory = join(store.directory, 'repo'); await mkdir(directory, { mode: 0o755 });
  for (const f of files) await writeFile(join(directory, f.path), Buffer.from(f.contentBase64, 'base64'), { mode: f.mode });
  for (const format of ['ustar', 'pax']) {
    const bytes = execFileSync('/usr/bin/tar', ['--no-xattrs', '--format=' + format, '-cf', '-', '-C', store.directory, 'repo'],
      { maxBuffer: CASE01_LIMITS.archiveBytes, env: { ...process.env, COPYFILE_DISABLE: '1', COPY_EXTENDED_ATTRIBUTES_DISABLE: '1' } });
    assert.deepEqual(await parseArchive(bytes), files);
  }
}));
