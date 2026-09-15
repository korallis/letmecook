import { test } from 'node:test';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
const execute = promisify(execFile);
const probe = new URL('./run-case-probe.mjs', import.meta.url);
for (const mode of ['control', 'missing-evidence', 'substituted-evidence', 'cleanup-unknown', 'sync-run-failure', 'sync-parent-failure', 'sync-ancestor-failure']) {
  test('coordinator: ' + mode, async () => {
    await execute(process.execPath, ['--experimental-test-module-mocks', probe.pathname, mode], { timeout: 20000 });
  });
}
for (const signal of ['SIGINT', 'SIGTERM']) for (const mode of ['pre-scope-stop', 'base-check-stop', 'pre-worker-stop', 'invocation-stop', 'worker-create-stop', 'late-stop', 'evidence-stop', 'ack-stop', 'readback-stop', 'cleanup-stop', 'dataset-stop', 'export-stop', 'final-save-stop']) {
  test('coordinator: ' + signal + ' at ' + mode, async () => {
    await execute(process.execPath, ['--experimental-test-module-mocks', probe.pathname, mode, signal], { timeout: 20000 });
  });
}
