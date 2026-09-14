// Client-only files; no policy authority, SQLite, deployment controls or keys.
import { mkdir, copyFile, writeFile, readFile } from 'node:fs/promises';
import { resolve, dirname, join } from 'node:path';
import { createHash } from 'node:crypto';
import { probe } from './client.ts';
export const workerFiles = [
  'experiments/harness/pins.json',
  ...['client.ts', 'run.ts', 'relay.ts', 'worker.ts', 'fixture.ts'].map(x => 'experiments/harness/native/' + x),
  'experiments/inference-boundary/json.ts', 'experiments/native-evaluation/opencode-settings.mjs',
];
export async function stageWorker(root: string, target: string, binary: string) {
  if (resolve(root) === resolve(target) || !root.startsWith('/') || !target.startsWith('/') || !binary.startsWith('/')) throw Error('invalid_worker_staging');
  probe(binary); await mkdir(target, { recursive: false });
  const hashes: Record<string, string> = {};
  for (const file of workerFiles) {
    const dest = join(target, file); await mkdir(dirname(dest), { recursive: true }); await copyFile(join(root, file), dest);
    hashes[file] = createHash('sha256').update(await readFile(dest)).digest('hex');
  }
  await writeFile(join(target, 'package.json'), '{"type":"module"}\n'); await copyFile(binary, join(target, 'opencode')); probe(join(target, 'opencode'));
  return { files: [...workerFiles, 'package.json', 'opencode'], hashes, binary: probe(join(target, 'opencode')).binarySHA256 };
}
