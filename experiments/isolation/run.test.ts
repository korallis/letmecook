import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { chmod, copyFile, mkdtemp, readFile, readdir, rm, symlink } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { selectedProfile } from './profile.ts';

const exec = promisify(execFile);
for (const signal of ['SIGINT', 'SIGTERM']) {
  for (const stage of ['create', 'inspect', 'wait', 'event']) {
    test(`${signal} during ${stage} refuses further execution and promptly cleans owned resources`, async () => {
      await runInterrupted(stage, signal);
    });
  }
}
test('interrupted launcher still refuses cleanup of a container owned by another run', async () => {
  await runInterrupted('inspect', 'SIGTERM', true);
});

async function runInterrupted(stage: string, signal: string, wrongOwner = false) {
  const dir = await mkdtemp(join(tmpdir(), 'isolation-stop-test-'));
  try {
    const docker = join(dir, 'fake-docker.ts');
    await copyFile(new URL('../../tests/fixtures/isolation/fake-docker.ts', import.meta.url), docker);
    await chmod(docker, 0o755);
    await symlink(docker, join(dir, 'docker'));
    const output = join(dir, 'evidence.json');
    let failure: any;
    try {
      await exec(process.execPath, [fileURLToPath(new URL('./run.ts', import.meta.url)), output], {
        timeout: 12_000,
        env: { ...process.env, PATH: [dir, dirname(process.execPath), process.env.PATH].join(':'),
          GAFFER_ISOLATION_DOCKER_CONTEXT: 'test-only', FAKE_DOCKER_STATE: dir,
          FAKE_DOCKER_PROFILE: JSON.stringify(selectedProfile), FAKE_DOCKER_STOP: stage,
          FAKE_DOCKER_SIGNAL: signal, FAKE_DOCKER_WRONG_OWNER: wrongOwner ? 'yes' : '',
        },
      });
    } catch (error) { failure = error; }
    assert.equal(failure?.code, 1, failure?.stderr ?? 'Launcher must exit with a blocked result');
    assert.equal(failure?.killed, false, 'Launcher must finish without the test timeout');
    const evidence = JSON.parse(await readFile(output, 'utf8'));
    assert.equal(evidence.result, 'blocked');
    assert.equal(evidence.unattendedSupported, false);
    assert.equal(evidence.cleanup.verified, !wrongOwner);
    const calls: any[][] = (await readFile(join(dir, 'calls.jsonl'), 'utf8')).trim().split('\n').map(s => JSON.parse(s));
    const signalled = calls.findIndex(args => args[0] === 'signal');
    assert.ok(signalled >= 0, 'Fake Docker must actually signal the running launcher');
    const afterStop = calls.slice(signalled + 1);
    assert.ok(afterStop.every(args => !['start', 'create', 'wait'].includes(args[0])), JSON.stringify(afterStop));
    const removed = calls.filter(args => args[0] === 'remove');
    assert.ok(removed.length > 0, 'Cleanup must remove the existing gateway');
    assert.ok(removed[0][2] - calls[signalled][2] < 8000, 'Cleanup must not wait for the 30-second fake wait/logs completion');
    for (const removal of removed) {
      const index = calls.indexOf(removal);
      assert.deepEqual(calls[index - 2], ['inspect', removal[1]], 'Removal must follow ownership inspection');
    }
    assert.ok(calls.some(args => args[0] === 'volume' && args[1] === 'rm'), 'Socket volume cleanup must still run');
    const remaining = (await readdir(dir)).filter(name => name.startsWith('gaffer-isolation-'));
    if (wrongOwner) {
      assert.equal(remaining.length, 1);
      assert.ok(remaining[0].endsWith('-probe.json'));
      assert.match(evidence.cleanup.error, /Cannot verify\/remove owned container/);
    } else assert.deepEqual(remaining, []);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
}
