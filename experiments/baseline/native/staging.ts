import { mkdir, copyFile, readFile, writeFile } from 'node:fs/promises';
import { join, dirname } from 'node:path';
import { createHash } from 'node:crypto';
import { stageWorker } from '../../harness/native/staging.ts';
const files = ['experiments/baseline/execution-contract.ts', ...['input.ts','worker.ts'].map(n => 'experiments/baseline/native/' + n), ...['manifest.ts','store.ts'].map(n => 'experiments/baseline/artifacts/' + n)];
export async function stageBaselineWorker(root: string, target: string, binary: string) {
  const stage = await stageWorker(root, target, binary);
  for (const path of files) { const destination = join(target, path); await mkdir(dirname(destination), { recursive: true }); await copyFile(join(root, path), destination); stage.hashes[path] = createHash('sha256').update(await readFile(destination)).digest('hex'); stage.files.push(path); }
  await writeFile(join(target, 'package.json'), '{"type":"module"}\n');
  return stage;
}
