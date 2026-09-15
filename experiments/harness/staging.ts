import { copyFile, mkdir, writeFile } from 'node:fs/promises';
import { dirname, join } from 'node:path';

// Static validation dependencies only. In particular, evaluation-scope.mjs and
// authority.mjs must never enter the actual-router worker's namespace.
const overlay = 'experiments/router-authority-extension/overlay/';
export const validationOverlayFiles = ['initial-suite.mjs', 'native-profile.mjs', 'scope-profile.mjs', 'native-planner.mjs', 'planner-plan-schema.mjs', 'responses-terminal.mjs'].map(n => overlay + n);
export const codecOverlayFiles = [...validationOverlayFiles, ...['native-responses.mjs'].map(n => overlay + n)];
export const routerWorkerFiles = [
  'experiments/harness/adapter.ts', 'experiments/harness/pins.json',
  'tests/fixtures/harness/router-worker.ts', 'tests/fixtures/harness/containment.ts',
  ...['json.ts', 'types.ts', 'profile-ids.ts', 'router-policy.ts', 'native-policy.ts'].map(n => 'experiments/inference-boundary/' + n),
  ...validationOverlayFiles,
];

export async function stageFiles(root: string, destination: string, files: readonly string[]) {
  await mkdir(destination, { recursive: true });
  await writeFile(join(destination, 'package.json'), '{"type":"module"}\n');
  for (const file of files) {
    await mkdir(dirname(join(destination, file)), { recursive: true });
    await copyFile(join(root, file), join(destination, file));
  }
}
