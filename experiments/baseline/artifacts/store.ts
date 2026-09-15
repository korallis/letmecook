import assert from 'node:assert/strict';
import { constants, type Stats } from 'node:fs';
import { open, lstat, link, unlink } from 'node:fs/promises';
import type { FileHandle } from 'node:fs/promises';
import { isAbsolute, join, resolve } from 'node:path';
import { createHash, randomBytes } from 'node:crypto';
import { canonical, parseJSON } from '../../inference-boundary/json.ts';
import { CASE01_LIMITS, type EvidenceRef, type EvidenceStore } from '../execution-contract.ts';

export const hashBytes = (bytes: Uint8Array | string) => createHash('sha256').update(bytes).digest('hex');
export function canonicalJSON(value: unknown): string {
  let nodes = 0, bytes = 0;
  const charge = (n: number) => assert((bytes += n) <= CASE01_LIMITS.artifactBytes, 'evidence_bytes_limit');
  const stringBytes = (v: string) => {
    assert(v.length <= CASE01_LIMITS.artifactBytes, 'evidence_bytes_limit'); return Buffer.byteLength(JSON.stringify(v));
  };
  function validate(v: unknown, depth: number): void {
    assert(depth <= 32 && ++nodes <= 100000, 'evidence_structure_limit');
    if (typeof v === 'string') { charge(stringBytes(v)); return; }
    if (v === null || typeof v === 'boolean') { charge(JSON.stringify(v).length); return; }
    if (typeof v === 'number') { assert(Number.isFinite(v), 'evidence_json_number'); charge(JSON.stringify(v).length); return; }
    assert(v && typeof v === 'object', 'evidence_json_value');
    assert(Array.isArray(v) || [Object.prototype, null].includes(Object.getPrototypeOf(v)), 'evidence_json_object');
    const descriptors = Object.getOwnPropertyDescriptors(v);
    assert(Object.getOwnPropertySymbols(v).length === 0, 'evidence_symbol');
    if (Array.isArray(v)) assert(Object.keys(v).length === v.length, 'evidence_sparse_array');
    charge(2); let count = 0;
    for (const key of Object.getOwnPropertyNames(v)) {
      if (Array.isArray(v) && key === 'length') continue;
      assert(descriptors[key].enumerable && Object.hasOwn(descriptors[key], 'value'), 'evidence_accessor_or_hidden_property');
      if (Array.isArray(v)) assert(/^(0|[1-9][0-9]*)$/.test(key) && Number(key) < v.length, 'evidence_array_property');
      if (count++) charge(1);
      if (!Array.isArray(v)) charge(stringBytes(key) + 1);
      validate(descriptors[key].value, depth + 1);
    }
  }
  validate(value, 0);
  const encoded = canonical(value);
  assert(Buffer.byteLength(encoded) <= CASE01_LIMITS.artifactBytes, 'evidence_bytes_limit');
  return encoded;
}
export function exact(value: unknown, names: string): asserts value is Record<string, any> {
  assert(value && typeof value === 'object' && !Array.isArray(value), 'evidence_record_required');
  assert([Object.prototype, null].includes(Object.getPrototypeOf(value)) && Object.getOwnPropertySymbols(value).length === 0
    && Object.values(Object.getOwnPropertyDescriptors(value)).every(d => d.enumerable && Object.hasOwn(d, 'value')), 'evidence_record_data');
  assert.deepEqual(Object.keys(value).sort(), names.split(' ').sort(), 'evidence_closed_fields');
}
export function validateEvidenceRef(ref: unknown): asserts ref is EvidenceRef {
  exact(ref, 'ref sha256');
  assert(typeof ref.sha256 === 'string' && /^[a-f0-9]{64}$/.test(ref.sha256), 'evidence_digest');
  assert(typeof ref.ref === 'string' && /^[a-z][a-z0-9-]{0,47}-[a-f0-9]{64}\.json$/.test(ref.ref)
    && ref.ref.endsWith('-' + ref.sha256 + '.json'), 'evidence_reference');
}
const privateDir = (s: Stats) => assert(s.isDirectory() && s.uid === process.getuid?.() && (s.mode & 0o7777) === 0o700, 'private_evidence_directory');
const privateFile = (s: Stats) => assert(s.isFile() && s.uid === process.getuid?.() && (s.mode & 0o7777) === 0o600
  && s.nlink === 1 && s.size <= CASE01_LIMITS.artifactBytes, 'private_evidence_file');
const same = (a: Stats, b: Stats) => assert(a.dev === b.dev && a.ino === b.ino, 'evidence_identity_changed');
async function openDirectory(store: EvidenceStore) {
  exact(store, 'directory');
  assert(typeof store.directory === 'string' && isAbsolute(store.directory) && resolve(store.directory) === store.directory, 'evidence_directory_path');
  const fd = await open(store.directory, constants.O_RDONLY | constants.O_DIRECTORY | constants.O_NOFOLLOW | constants.O_NONBLOCK);
  try { const identity = await fd.stat(); privateDir(identity); same(identity, await lstat(store.directory)); return { fd, identity }; }
  catch (error) { await fd.close(); throw error; }
}
async function checkDirectory(directory: string, fd: FileHandle, identity: Stats) {
  const actual = await fd.stat(), path = await lstat(directory); privateDir(actual); privateDir(path); same(identity, actual); same(identity, path);
}
async function boundedBytes(fd: FileHandle) {
  const chunks: Buffer[] = []; let length = 0;
  while (length <= CASE01_LIMITS.artifactBytes) {
    const buffer = Buffer.alloc(Math.min(65536, CASE01_LIMITS.artifactBytes + 1 - length));
    const { bytesRead } = await fd.read(buffer, 0, buffer.length, length);
    if (!bytesRead) break;
    chunks.push(buffer.subarray(0, bytesRead)); length += bytesRead;
  }
  assert(length <= CASE01_LIMITS.artifactBytes, 'evidence_bytes_limit'); return Buffer.concat(chunks, length);
}
async function checkFile(path: string, fd: FileHandle, expected: Uint8Array) {
  const before = await fd.stat(); privateFile(before); const pathBefore = await lstat(path); privateFile(pathBefore); same(before, pathBefore);
  const bytes = await boundedBytes(fd); assert(Buffer.from(expected).equals(bytes), 'evidence_content_changed');
  const after = await fd.stat(), pathAfter = await lstat(path); privateFile(after); privateFile(pathAfter); same(before, after); same(before, pathAfter);
  assert(after.size === bytes.length && before.size === after.size, 'evidence_size_changed');
}

// Internal fault-test seam. The normal public API supplies no callback.
export async function writeRecord(store: EvidenceStore, kind: string, value: unknown,
  checkpoint: (phase: 'file-synced'|'linked'|'directory-synced', path: string) => Promise<void> = async () => {}) : Promise<EvidenceRef> {
  exact(store, 'directory'); store = { ...store };
  assert(typeof kind === 'string' && /^[a-z][a-z0-9-]{0,47}$/.test(kind), 'evidence_kind');
  const bytes = Buffer.from(canonicalJSON(value)), sha256 = hashBytes(bytes), ref = kind + '-' + sha256 + '.json';
  const directory = await openDirectory(store), target = join(store.directory, ref);
  const temporary = join(store.directory, '.' + ref + '.' + randomBytes(12).toString('hex') + '.next');
  let staged: FileHandle | undefined, retained: FileHandle | undefined;
  try {
    await checkDirectory(store.directory, directory.fd, directory.identity);
    staged = await open(temporary, constants.O_CREAT | constants.O_EXCL | constants.O_RDWR | constants.O_NOFOLLOW, 0o600);
    await staged.writeFile(bytes); await staged.sync(); await checkpoint('file-synced', temporary);
    await checkFile(temporary, staged, bytes); await checkDirectory(store.directory, directory.fd, directory.identity);
    try { await link(temporary, target); retained = staged; }
    catch (error: any) { if (error.code !== 'EEXIST') throw error; retained = await open(target, constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK); }
    await unlink(temporary);
    await checkpoint('linked', target);
    await checkFile(target, retained, bytes); await retained.sync(); await directory.fd.sync();
    await checkpoint('directory-synced', target);
    await checkDirectory(store.directory, directory.fd, directory.identity); await checkFile(target, retained, bytes);
    await checkDirectory(store.directory, directory.fd, directory.identity);
    return { ref, sha256 };
  } finally {
    // Leave a failed pending file for inspection; never unlink a substituted path.
    if (retained && retained !== staged) await retained.close();
    await staged?.close(); await directory.fd.close();
  }
}
export const retainEvidence = (store: EvidenceStore, kind: string, value: unknown) => writeRecord(store, kind, value);

export async function readEvidence(store: EvidenceStore, ref: EvidenceRef): Promise<unknown> {
  exact(store, 'directory'); store = { ...store }; validateEvidenceRef(ref); ref = { ...ref };
  const directory = await openDirectory(store); let file: FileHandle | undefined;
  try {
    const path = join(store.directory, ref.ref);
    file = await open(path, constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK);
    privateFile(await file.stat()); const bytes = await boundedBytes(file);
    assert(hashBytes(bytes) === ref.sha256, 'evidence_digest_mismatch');
    const value = JSON.parse(JSON.stringify(parseJSON(new TextDecoder('utf-8', { fatal: true }).decode(bytes), true))) as unknown;
    assert(canonicalJSON(value) === bytes.toString('utf8'), 'evidence_not_canonical');
    await checkFile(path, file, bytes); await checkDirectory(store.directory, directory.fd, directory.identity);
    return value;
  } finally { await file?.close(); await directory.fd.close(); }
}
