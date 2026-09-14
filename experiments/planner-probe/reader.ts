import { constants } from 'node:fs';
import { lstat, open, realpath } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { createHash } from 'node:crypto';

export const sha256 = (value: string | Uint8Array) => createHash('sha256').update(value).digest('hex');
export class ProbeError extends Error {
  readonly code: string;
  constructor(code: string) { super(code); this.code = code; }
}
export interface Snapshot { path: 'fixture.txt'; sha256: string; bytes: number; text: string }

// Trusted host acquisition only. A preapproved digest is mandatory: no arbitrary
// repository traversal and no unverified bytes can enter the inference packet.
export async function snapshot(root: string, path: string, digest: string, maxBytes: number, signal: AbortSignal): Promise<Snapshot> {
  const check = () => { if (signal.aborted) throw new ProbeError('cancelled'); };
  check();
  if (path !== 'fixture.txt' || !/^[a-f0-9]{64}$/.test(digest) || !Number.isSafeInteger(maxBytes) || maxBytes < 1 || maxBytes > 4096) throw new ProbeError('discovery_denied');
  try {
    const approvedRoot = resolve(root);
    if (await realpath(root) !== approvedRoot) throw new ProbeError('discovery_denied');
    const before = await lstat(approvedRoot);
    if (!before.isDirectory() || before.isSymbolicLink()) throw new ProbeError('discovery_denied');
    const file = await open(join(approvedRoot, path), constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK);
    try {
      check();
      const info = await file.stat();
      if (!info.isFile() || info.nlink !== 1) throw new ProbeError('discovery_denied');
      if (info.size > maxBytes) throw new ProbeError('discovery_budget_exhausted');
      // A fixed buffer prevents a file growing after stat from causing unbounded reads.
      const buffer = Buffer.alloc(maxBytes + 1); let size = 0;
      while (size < buffer.length) {
        check();
        const part = await file.read(buffer, size, buffer.length - size, size);
        if (!part.bytesRead) break;
        size += part.bytesRead;
      }
      if (size > maxBytes) throw new ProbeError('discovery_budget_exhausted');
      const after = await file.stat(); const rootAfter = await lstat(approvedRoot);
      if (before.dev !== rootAfter.dev || before.ino !== rootAfter.ino || info.ino !== after.ino || info.size !== after.size || info.mtimeMs !== after.mtimeMs || after.nlink !== 1) throw new ProbeError('discovery_changed');
      const bytes = buffer.subarray(0, size);
      if (sha256(bytes) !== digest) throw new ProbeError('discovery_changed');
      check();
      return Object.freeze({ path: 'fixture.txt', sha256: digest, bytes: size, text: new TextDecoder('utf-8', { fatal: true }).decode(bytes) });
    } finally { await file.close(); check(); }
  } catch (error) {
    if (error instanceof ProbeError) throw error;
    throw new ProbeError('discovery_denied');
  }
}
