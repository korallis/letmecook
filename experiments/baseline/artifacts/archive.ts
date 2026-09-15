import assert from 'node:assert/strict';
import { CASE01_LIMITS, type SnapshotFile } from '../execution-contract.ts';
import { hashBytes } from './store.ts';
import { makeManifest, sourcePath } from './manifest.ts';

export type FrozenArchive = Uint8Array | AsyncIterable<Uint8Array>;
// Git controls are never source artifacts. These independent limits also bound a normal
// freshly initialized synthetic Git repository without admitting unlimited ignored bytes.
export const CONTROL_LIMITS = Object.freeze({ bytes: 524288, entries: 256, directories: 256, metadataBytes: 16384, metadataEntries: 64, chunks: 65536 });
const decoder = new TextDecoder('utf-8', { fatal: true });
const zero = (b: Uint8Array) => b.every(v => v === 0);
async function collect(archive: FrozenArchive) {
  if (archive instanceof Uint8Array) {
    assert(archive.byteLength <= CASE01_LIMITS.archiveBytes, 'archive_bytes_limit'); return Buffer.from(archive);
  }
  assert(archive && typeof archive[Symbol.asyncIterator] === 'function', 'archive_input');
  // ponytail: case01 fits in 1 MiB; use incremental parsing if its registered ceiling grows.
  const bytes = Buffer.alloc(CASE01_LIMITS.archiveBytes); let size = 0, chunks = 0;
  for await (const chunk of archive) {
    assert(++chunks <= CONTROL_LIMITS.chunks, 'archive_chunk_limit'); assert(chunk instanceof Uint8Array, 'archive_chunk');
    assert(size + chunk.byteLength <= CASE01_LIMITS.archiveBytes, 'archive_bytes_limit'); bytes.set(chunk, size); size += chunk.byteLength;
  }
  return bytes.subarray(0, size);
}
function field(bytes: Buffer) {
  const end = bytes.indexOf(0);
  if (end !== -1) assert(zero(bytes.subarray(end)), 'archive_field_padding');
  return decoder.decode(end === -1 ? bytes : bytes.subarray(0, end));
}
function octal(bytes: Buffer) {
  assert(!bytes.some(v => v >= 128), 'archive_binary_number_unsupported');
  const text = bytes.toString('ascii').replace(/[\0 ]+$/g, '').replace(/^ +/g, '');
  assert(/^[0-7]+$/.test(text), 'archive_octal');
  const n = parseInt(text, 8); assert(Number.isSafeInteger(n), 'archive_number'); return n;
}
function decimal(text: string) {
  assert(/^(0|[1-9][0-9]*)$/.test(text), 'archive_decimal');
  const n = Number(text); assert(Number.isSafeInteger(n), 'archive_number'); return n;
}
function pax(bytes: Buffer) {
  assert(bytes.length <= CONTROL_LIMITS.metadataBytes, 'archive_metadata_bytes');
  const result: Record<string, string> = Object.create(null); let offset = 0, count = 0;
  while (offset < bytes.length) {
    assert(++count <= CONTROL_LIMITS.metadataEntries, 'archive_metadata_count');
    const space = bytes.indexOf(32, offset); assert(space > offset && space - offset < 10, 'archive_pax_length');
    const length = decimal(bytes.subarray(offset, space).toString('latin1'));
    assert(length > space - offset + 3 && offset + length <= bytes.length && bytes[offset + length - 1] === 10, 'archive_pax_record');
    const record = decoder.decode(bytes.subarray(space + 1, offset + length - 1));
    const eq = record.indexOf('='); assert(eq > 0, 'archive_pax_record');
    const key = record.slice(0, eq), value = record.slice(eq + 1);
    assert(['path', 'size', 'mtime', 'atime', 'ctime', 'uid', 'gid', 'uname', 'gname'].includes(key), 'archive_pax_key');
    assert(!Object.hasOwn(result, key) && !value.includes('\0'), 'archive_pax_duplicate_or_nul');
    result[key] = value; offset += length;
  }
  return result;
}
function archivePath(path: string) {
  assert(path.length > 0 && path.length <= 256 && path.split('/').every(s => /^[A-Za-z0-9_@+.-]+$/.test(s) && s !== '.' && s !== '..'), 'archive_path');
  assert(path.split('/').every((part, i) => part.toLowerCase() !== '.git' || i === 0 && part === '.git'), 'archive_nested_git');
}

/** Parse uncompressed USTAR/PAX (and GNU long-name) Docker cp exports in memory.
 * Requires the initial exported directory header: repo/ (any portable basename)
 * or ./ from `docker cp container:/work/repo/. -`. Never extracts or follows links.
 */
export async function parseArchive(archive: FrozenArchive): Promise<SnapshotFile[]> {
  const bytes = await collect(archive);
  assert(bytes.length >= 1024 && bytes.length % 512 === 0, 'archive_block_alignment');
  let offset = 0, headerCount = 0, metadataBytes = 0, metadataCount = 0, controlBytes = 0, controlEntries = 0, directories = 0, sourceBytes = 0;
  let root: string | undefined, pending: Record<string, string> | undefined, longName: string | undefined, ended = false;
  const files: SnapshotFile[] = [], entries = new Map<string, 'file'|'directory'>();
  while (offset < bytes.length) {
    const header = bytes.subarray(offset, offset + 512); offset += 512;
    if (zero(header)) {
      assert(!pending && longName === undefined && offset + 512 <= bytes.length && zero(bytes.subarray(offset)), 'archive_end_blocks');
      ended = true; break;
    }
    assert(++headerCount <= 1024, 'archive_entry_limit');
    const checksum = octal(header.subarray(148, 156));
    const actual = header.reduce((sum, b, i) => sum + (i >= 148 && i < 156 ? 32 : b), 0);
    assert.equal(actual, checksum, 'archive_checksum');
    const magic = header.subarray(257, 263).toString('latin1'), version = header.subarray(263, 265).toString('latin1');
    assert(magic === 'ustar\0' && version === '00' || magic === 'ustar ' && version === ' \0', 'archive_format');
    let name = field(header.subarray(0, 100));
    if (magic === 'ustar\0') { const prefix = field(header.subarray(345, 500)); if (prefix) name = prefix + '/' + name; }
    const type = String.fromCharCode(header[156] || 48), mode = octal(header.subarray(100, 108));
    assert(mode <= 0o777, 'archive_privileged_mode');
    assert(field(header.subarray(157, 257)) === '', 'archive_link_target');
    let size = octal(header.subarray(124, 136));
    if (type !== 'x' && type !== 'L' && pending?.size !== undefined) size = decimal(pending.size);
    assert(size <= CASE01_LIMITS.archiveBytes && offset + Math.ceil(size / 512) * 512 <= bytes.length, 'archive_entry_size');
    const content = bytes.subarray(offset, offset + size); const paddingEnd = offset + Math.ceil(size / 512) * 512;
    assert(zero(bytes.subarray(offset + size, paddingEnd)), 'archive_entry_padding'); offset = paddingEnd;
    if (type === 'x' || type === 'L') {
      metadataBytes += size; assert(++metadataCount <= CONTROL_LIMITS.metadataEntries && metadataBytes <= CONTROL_LIMITS.metadataBytes, 'archive_metadata_limit');
      assert(!pending && longName === undefined, 'archive_repeated_metadata');
      if (type === 'x') pending = pax(content);
      else { assert(content.length > 1 && content.at(-1) === 0, 'archive_longname'); longName = field(content); }
      continue;
    }
    assert(type === '0' || type === '5', 'archive_links_or_special_entry');
    name = pending?.path ?? longName ?? name; pending = undefined; longName = undefined;
    assert(type !== '5' || size === 0, 'archive_directory_content');
    if (root === undefined) {
      assert(type === '5', 'archive_root_directory_required');
      assert(name === '.' || name === './' || /^[A-Za-z0-9_@+.-]+\/$/.test(name) && !['../', '.git/'].includes(name), 'archive_root');
      root = name === '.' || name === './' ? './' : name;
      continue;
    }
    if (root === './') { assert(name.startsWith('./'), 'archive_outside_root'); name = name.slice(2); }
    else { assert(name.startsWith(root), 'archive_outside_root'); name = name.slice(root.length); }
    if (type === '5' && name.endsWith('/')) name = name.slice(0, -1);
    archivePath(name);
    const key = name.toLowerCase(); assert(!entries.has(key), 'archive_duplicate_path');
    const parents = key.split('/').slice(0, -1).map((_, i, parts) => parts.slice(0, i + 1).join('/'));
    assert(parents.every(p => entries.get(p) !== 'file'), 'archive_file_directory_collision');
    assert(type === '5' || ![...entries.keys()].some(p => p.startsWith(key + '/')), 'archive_file_directory_collision');
    entries.set(key, type === '5' ? 'directory' : 'file');
    if (type === '5') { assert(++directories <= CONTROL_LIMITS.directories, 'archive_directory_limit'); continue; }
    assert(name !== '.git', 'archive_git_file');
    if (name.startsWith('.git/')) {
      controlBytes += size; assert(++controlEntries <= CONTROL_LIMITS.entries && controlBytes <= CONTROL_LIMITS.bytes, 'archive_git_limit'); continue;
    }
    sourcePath(name); assert(mode === 0o644 || mode === 0o755, 'snapshot_mode');
    sourceBytes += size; assert(sourceBytes <= CASE01_LIMITS.sourceBytes && files.length < CASE01_LIMITS.files, 'archive_source_limit');
    files.push({ path: name, mode, size, sha256: hashBytes(content), contentBase64: content.toString('base64') });
  }
  assert(ended && root !== undefined, 'archive_incomplete');
  files.sort((a, b) => a.path < b.path ? -1 : a.path > b.path ? 1 : 0); makeManifest(files);
  return files;
}
