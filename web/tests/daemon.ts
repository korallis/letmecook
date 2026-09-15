import { execFileSync, spawn, type ChildProcess } from 'node:child_process';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

export const root = fileURLToPath(new URL('../../', import.meta.url));

/** Build and start the real CGO-free daemon; the embedded shell must already be in web/dist. */
export async function startDaemon(): Promise<{ base: string; stop: () => Promise<void> }> {
  const dir = mkdtempSync(join(tmpdir(), 'gaffer-shell-test-'));
  const binary = join(dir, 'gafferd');
  let daemon: ChildProcess | undefined;
  let closed: Promise<void> | undefined;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const stop = async () => {
    const killTimer = setTimeout(() => daemon?.kill('SIGKILL'), 6000);
    try {
      if (daemon?.pid && daemon.exitCode === null && daemon.signalCode === null) daemon.kill('SIGTERM');
      await closed;
    } finally {
      clearTimeout(killTimer);
      rmSync(dir, { recursive: true, force: true });
    }
  };
  try {
    execFileSync('go', ['build', '-trimpath', '-buildvcs=false', '-o', binary, './cmd/gafferd'], { cwd: root, stdio: 'inherit', env: { ...process.env, CGO_ENABLED: '0' } });
    daemon = spawn(binary, ['--fixture'], { cwd: dir, stdio: ['ignore', 'pipe', 'inherit'] });
    closed = new Promise<void>(resolve => daemon!.once('close', () => resolve()));
    const base = await new Promise<string>((resolve, reject) => {
      let output = '';
      timer = setTimeout(() => reject(new Error('daemon startup timeout')), 20000);
      daemon!.stdout!.on('data', chunk => {
        output += chunk.toString();
        const match = /^fixture-only (http:\/\/127\.0\.0\.1:\d+)\/api\/v1\/status\n/.exec(output);
        if (match) resolve(match[1]!);
        else if (output.length > 1024) reject(new Error(output));
      });
      daemon!.once('error', reject);
      daemon!.once('exit', code => reject(new Error(`daemon exited ${code}`)));
    });
    return { base, stop };
  } catch (error) {
    await stop();
    throw error;
  } finally {
    clearTimeout(timer);
  }
}
