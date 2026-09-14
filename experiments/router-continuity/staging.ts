import { copyFile, readFile, mkdir } from 'node:fs/promises';
import { join } from 'node:path';
import { createHash } from 'node:crypto';
import { stageWorker } from '../harness/native/staging.ts';
export async function stageContinuity(root: string,target: string,binary: string) {
  const result = await stageWorker(root,target,binary); // Default, not proof staging.
  await mkdir(join(target,'experiments/router-continuity'));
  for (const file of ['constants.ts','client.ts','worker.ts']) {
    const relative = 'experiments/router-continuity/'+file;
    await copyFile(join(root,relative),join(target,relative));
    result.files.push(relative); result.hashes[relative] = createHash('sha256').update(await readFile(join(target,relative))).digest('hex');
  }
  return result;
}
