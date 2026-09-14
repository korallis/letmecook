import assert from 'node:assert/strict';
import { readFileSync, writeFileSync, readdirSync, unlinkSync, symlinkSync, openSync, writeSync, closeSync } from 'node:fs';
import { connect } from 'node:net';
import { networkInterfaces } from 'node:os';
import { spawn } from 'node:child_process';
import { Resolver } from 'node:dns/promises';
export async function containment(router = false, memoryMiB: 512 | 768 = 512) {
  const evidence: Record<string, unknown> = {};
  assert.equal(process.getuid!(), 1000);
  const status = readFileSync('/proc/self/status', 'utf8');
  for (const cap of ['CapInh', 'CapPrm', 'CapEff', 'CapBnd', 'CapAmb']) assert.match(status, new RegExp(`${cap}:\\s+0000000000000000`));
  assert.match(status, /NoNewPrivs:\s+1/); assert.match(status, /Seccomp:\s+2/);
  const blocked = (action: () => unknown) => { let code = ''; try { action(); } catch (error: any) { code = error.code; } assert(['ENOENT', 'EPERM', 'EACCES', 'EROFS'].includes(code), code); return code; };
  const paths = ['/var/run/docker.sock', '/run/host-services/ssh-auth.sock', '/root/.ssh/id_ed25519', '/work/upstream.sock', '/work/journal/state.json', process.env.HOST_SENTINEL!, ...(router ? ['/router-source/package.json','/probe/overlay/authority.mjs','/bridge/authority.ts','/inference-boundary/boundary.ts','/work/router.sock','/work/router-db/db.sqlite','/work/approved.json'] : [])];
  evidence.deniedReads = Object.fromEntries(paths.map(path => [path, blocked(() => readFileSync(path))]));
  evidence.deniedWrites = Object.fromEntries(['/etc/escape', '/fixture/opencode', '/router/escape'].map(path => [path, blocked(() => writeFileSync(path, 'escaped'))]));
  evidence.cannotUnlinkBoundary = blocked(() => unlinkSync('/router/inference.sock'));
  evidence.cannotBecomeRoot = blocked(() => process.setuid!(0));
  assert.deepEqual(readdirSync('/router').sort(), router ? ['inference.sock'] : ['grant.json', 'inference.sock']);
  symlinkSync(process.env.HOST_SENTINEL!, '/work/host-link'); evidence.symlinkDenied = blocked(() => readFileSync('/work/host-link'));
  symlinkSync('/proc/1/root' + process.env.HOST_SENTINEL, '/work/proc-link'); evidence.procDenied = blocked(() => readFileSync('/work/proc-link'));
  assert.deepEqual(Object.keys(networkInterfaces()), ['lo']); evidence.interfaces = ['lo'];
  const targets: [string, number][] = [['1.1.1.1', 443], ['192.168.65.2', 20128], ['169.254.169.254', 80], ['127.0.0.1', 20128], ['::1', 20128], ['2606:4700:4700::1111', 443]];
  evidence.egress = await Promise.all(targets.map(async ([host, port]) => ({ host, port, result: await new Promise<string>((resolve, reject) => {
    const socket = connect({ host, port }); socket.setTimeout(500, () => { socket.destroy(); resolve('TIMEOUT'); });
    socket.once('connect', () => { socket.destroy(); reject(new Error('egress_succeeded')); }); socket.once('error', (error: any) => resolve(error.code));
  }) })));
  const resolver = new Resolver({ timeout: 500, tries: 1 }); resolver.setServers(['1.1.1.1']); let dns = '';
  try { await resolver.resolve4('example.com'); } catch (error: any) { dns = error.code; } assert(dns); evidence.dns = dns;
  const cgroup = (key: string) => readFileSync('/sys/fs/cgroup/' + key, 'utf8').trim();
  assert.equal(cgroup('memory.max'), String(memoryMiB * 1048576)); assert.equal(cgroup('memory.swap.max'), '0'); assert.equal(cgroup('pids.max'), '64'); assert.equal(cgroup('cpu.max'), '50000 100000');
  evidence.cgroup = Object.fromEntries(['memory.max', 'memory.swap.max', 'pids.max', 'cpu.max'].map(k => [k, cgroup(k)]));
  const throttled = () => Number(cgroup('cpu.stat').match(/nr_throttled (\d+)/)![1]); const before = throttled(); const until = Date.now() + 1000;
  while (Date.now() < until) Math.sqrt(Math.random()); assert(throttled() > before); evidence.cpuThrottled = true;
  const children: ReturnType<typeof spawn>[] = []; let errorCode = '';
  for (let i = 0; i < 80; i++) {
    const child = spawn('/bin/sleep', ['30'], { stdio: 'ignore' }); children.push(child);
    const outcome = await new Promise<string>(resolve => { child.once('spawn', () => resolve('spawned')); child.once('error', (error: any) => resolve(error.code)); });
    if (outcome !== 'spawned') { errorCode = outcome; break; }
  }
  assert.equal(errorCode, 'EAGAIN'); evidence.pidsExhausted = children.length - 1;
  await Promise.all(children.filter(c => c.pid).map(c => new Promise<void>(resolve => { c.once('close', () => resolve()); c.kill('SIGKILL'); })));
  evidence.filesystems = [];
  for (const [directory, maxMiB] of [['/work', 32], ['/tmp', 8], ['/dev/shm', 4]] as const) {
    const file = openSync(directory + '/fill', 'w'); let bytes = 0, code = '';
    try { while (bytes < (maxMiB + 1) * 1048576) bytes += writeSync(file, Buffer.alloc(1048576, 0x61)); } catch (error: any) { code = error.code; } finally { closeSync(file); unlinkSync(directory + '/fill'); }
    assert.equal(code, 'ENOSPC'); assert(bytes <= maxMiB * 1048576); (evidence.filesystems as unknown[]).push({ directory, bytes, code });
  }
  return evidence;
}
