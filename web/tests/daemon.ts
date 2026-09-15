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
  execFileSync('go', ['build', '-trimpath', '-buildvcs=false', '-o', binary, './cmd/gafferd'], { cwd: root, stdio: 'inherit', env: { ...process.env, CGO_ENABLED: '0' } });
  const daemon: ChildProcess = spawn(binary, [], { cwd: dir, stdio: ['ignore', 'pipe', 'inherit'] });
  const base = await new Promise<string>((resolve, reject) => {
    let output = '';
    const timer = setTimeout(() => reject(new Error('daemon startup timeout')), 20000);
    daemon.stdout!.on('data', chunk => {
      output += chunk.toString();
      const match = /^fixture-only (http:\/\/127\.0\.0\.1:\d+)\/api\/v1\/status\n/.exec(output);
      if (match) { clearTimeout(timer); resolve(match[1]!); }
      else if (output.length > 1024) { clearTimeout(timer); reject(new Error(output)); }
    });
    daemon.on('exit', code => { clearTimeout(timer); reject(new Error(`daemon exited ${code}`)); });
  });
  return {
    base,
    stop: async () => {
      const exited = new Promise<void>(resolve => daemon.once('exit', () => resolve()));
      daemon.kill('SIGTERM');
      await exited;
      rmSync(dir, { recursive: true, force: true });
    },
  };
}
