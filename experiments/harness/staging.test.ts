import assert from 'node:assert/strict';
import { test } from 'node:test';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { mkdtemp, readdir, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { codecOverlayFiles, routerWorkerFiles, stageFiles } from './staging.ts';

const exec = promisify(execFile), root = resolve(import.meta.dirname, '../..');
const overlay = 'experiments/router-authority-extension/overlay';
test('actual-router worker imports from its isolated validation-only closure', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'gaffer-worker-closure-'));
  try {
    await stageFiles(root, directory, routerWorkerFiles);
    await exec(process.execPath, ['--input-type=module', '-e', "await import('./experiments/harness/adapter.ts')"], { cwd: directory });
    assert.deepEqual((await readdir(join(directory, overlay))).sort(), ['native-profile.mjs', 'scope-profile.mjs']);
    assert(!routerWorkerFiles.some(file => /authority\.mjs|evaluation-scope|boundary\.ts|policy\.ts$/.test(file) && !/router-policy|native-policy/.test(file)));
    // An absent transitive validator must fail the staged import, without a host
    // repository fallback making this regression check accidentally pass.
    await rm(join(directory, overlay, 'scope-profile.mjs'));
    await assert.rejects(exec(process.execPath, ['--input-type=module', '-e', "await import('./experiments/harness/adapter.ts')"], { cwd: directory }), /ERR_MODULE_NOT_FOUND/);
  } finally { await rm(directory, { recursive: true, force: true }); }
});

test('legacy fixture boundary imports with only pure overlay codecs', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'gaffer-boundary-closure-'));
  try {
    const files = (await readdir(join(root, 'experiments/inference-boundary'))).filter(n => n.endsWith('.ts') && !n.endsWith('.test.ts')).map(n => 'experiments/inference-boundary/' + n);
    await stageFiles(root, directory, [...files, ...codecOverlayFiles]);
    await exec(process.execPath, ['--input-type=module', '-e', "await import('./experiments/inference-boundary/boundary.ts'); await import('./experiments/inference-boundary/policy.ts')"], { cwd: directory });
    assert.deepEqual((await readdir(join(directory, overlay))).sort(), ['native-profile.mjs', 'native-responses.mjs', 'responses-terminal.mjs', 'scope-profile.mjs']);
  } finally { await rm(directory, { recursive: true, force: true }); }
});
