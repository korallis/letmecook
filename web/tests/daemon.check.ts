import assert from 'node:assert/strict';
import childProcess, { type ChildProcess } from 'node:child_process';
import { existsSync, rmSync, writeFileSync } from 'node:fs';
import { syncBuiltinESMExports } from 'node:module';
import { dirname } from 'node:path';
import { test } from 'node:test';
import { startDaemon } from './daemon.ts';

const spawn = childProcess.spawn;

for (const mode of ['build failure', 'spawn failure', 'early exit', 'timeout', 'already exited', 'running', 'ignores SIGTERM'] as const) {
  test(`daemon cleanup: ${mode}`, { timeout: 5000 }, async t => {
    let dir = '';
    let daemon: ChildProcess | undefined;
    let closed: Promise<void> | undefined;
    let spawned!: () => void;
    const started = new Promise<void>(resolve => { spawned = resolve; });
    t.mock.method(childProcess, 'execFileSync', (_command: string, args: readonly string[]) => {
      const binary = args[args.indexOf('-o') + 1]!;
      dir = dirname(binary);
      if (mode === 'build failure') throw new Error('build failed');
      if (mode === 'spawn failure') return Buffer.alloc(0);
      const body = mode === 'early exit' ? 'process.exit(2);' : mode === 'timeout' ? 'setInterval(() => {}, 1000);'
        : `${mode === 'ignores SIGTERM' ? "process.on('SIGTERM', () => {});" : ''}process.stdout.write('fixture-only http://127.0.0.1:12345/api/v1/status\\n');${mode !== 'already exited' ? 'setInterval(() => {}, 1000);' : ''}`;
      writeFileSync(binary, `#!${process.execPath}\n${body}\n`, { mode: 0o700 });
      return Buffer.alloc(0);
    });
    t.mock.method(childProcess, 'spawn', (...args: Parameters<typeof spawn>) => {
      daemon = spawn(...args);
      closed = new Promise<void>(resolve => daemon!.once('close', () => resolve()));
      daemon.once('spawn', spawned);
      return daemon;
    });
    syncBuiltinESMExports();
    try {
      if (mode === 'timeout' || mode === 'ignores SIGTERM') t.mock.timers.enable({ apis: ['setTimeout'] });
      const starting = startDaemon();
      if (mode === 'running' || mode === 'already exited' || mode === 'ignores SIGTERM') {
        const { base, stop } = await starting;
        assert.equal(base, 'http://127.0.0.1:12345');
        if (mode === 'already exited') await closed;
        const stopping = stop();
        if (mode === 'ignores SIGTERM') t.mock.timers.tick(6000);
        await stopping;
        if (mode === 'ignores SIGTERM') assert.equal(daemon!.signalCode, 'SIGKILL');
        await stop();
      } else {
        const rejected = assert.rejects(starting, mode === 'timeout' ? /daemon startup timeout/
          : mode === 'early exit' ? /daemon exited 2/ : mode === 'spawn failure' ? /ENOENT/ : /build failed/);
        if (mode === 'timeout') {
          await started;
          t.mock.timers.tick(20000);
        }
        await rejected;
      }
      assert.equal(existsSync(dir), false);
      if (daemon?.pid) assert.ok(daemon.exitCode !== null || daemon.signalCode !== null);
    } finally {
      t.mock.timers.reset();
      t.mock.restoreAll();
      syncBuiltinESMExports();
      if (daemon?.pid && daemon.exitCode === null && daemon.signalCode === null) daemon.kill('SIGKILL');
      await closed;
      if (dir) rmSync(dir, { recursive: true, force: true });
    }
  });
}
