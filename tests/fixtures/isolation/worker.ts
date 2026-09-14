import assert from 'node:assert/strict';
import { readFileSync, writeFileSync, symlinkSync, mkdirSync, unlinkSync, openSync, closeSync, writeSync, readdirSync } from 'node:fs';
import { spawn, spawnSync } from 'node:child_process';
import { request } from 'node:http';
import { connect } from 'node:net';
import { Resolver } from 'node:dns/promises';
import { networkInterfaces } from 'node:os';

const mode = process.argv[2];
const emit = (name: string, observation: unknown) => console.log(JSON.stringify({ name, observation }));
const errorCode = (error: unknown) => (error as NodeJS.ErrnoException).code;
const blocked = (name: string, action: () => unknown) => {
  let code: string | undefined;
  try { action(); } catch (error) { code = errorCode(error); }
  assert.ok(['ENOENT', 'EACCES', 'EPERM', 'EROFS'].includes(code ?? ''), `${name} unexpectedly succeeded or returned ${code}`);
  emit(name, code);
};
const hostTargets = JSON.parse(process.env.HOST_TARGETS!);
function attacks(prefix: string) {
  for (const [name, path] of Object.entries(hostTargets) as [string, string][]) {
    blocked(`${prefix}-${name}-read`, () => readFileSync(path));
    blocked(`${prefix}-${name}-write`, () => writeFileSync(path, 'BREACHED'));
  }
  for (const path of ['/var/run/docker.sock', '/run/docker.sock', '/run/host-services/ssh-auth.sock', '/root/.ssh/id_ed25519']) {
    blocked(`${prefix}-denied-${path}`, () => readFileSync(path));
  }
}
if (mode === 'attack') {
  attacks('hook');
} else if (mode === 'oom') {
  const allocations: Buffer[] = [];
  setInterval(() => { allocations.push(Buffer.alloc(16 * 1024 * 1024, 0x61)); }, 20);
} else if (mode === 'cancel') {
  process.on('SIGTERM', () => {});
  const child = spawn('/bin/sh', ['-c', "trap '' TERM; /bin/sh -c 'trap \"\" TERM; while :; do sleep 1; done' & while :; do sleep 1; done"], { detached: true, stdio: 'ignore' });
  emit('tree-started', { parent: process.pid, child: child.pid, detached: true });
  setInterval(() => emit('heartbeat', process.pid), 100);
} else if (mode === 'probe') {
  emit('fixture-runtime', { node: process.version, git: spawnSync('git', ['--version'], { encoding: 'utf8' }).stdout.trim(), osRelease: readFileSync('/etc/os-release', 'utf8') });
  assert.equal(process.getuid!(), 1000);
  const status = readFileSync('/proc/self/status', 'utf8');
  for (const field of ['CapInh', 'CapPrm', 'CapEff', 'CapBnd', 'CapAmb']) assert.match(status, new RegExp(`${field}:\\s+0000000000000000`));
  assert.match(status, /NoNewPrivs:\s+1/);
  assert.match(status, /Seccomp:\s+2/);
  emit('kernel-security', status.split('\n').filter(line => /^(Uid|Cap|NoNewPrivs|Seccomp):?/.test(line)));
  blocked('cannot-become-root', () => process.setuid!(0));
  blocked('root-filesystem-readonly', () => writeFileSync('/etc/gaffer-escape', 'BREACHED'));
  blocked('fixture-readonly', () => writeFileSync('/fixture/worker.ts', 'BREACHED'));
  blocked('socket-volume-readonly', () => writeFileSync('/router/escape', 'BREACHED'));
  blocked('socket-unlink-denied', () => unlinkSync('/router/inference.sock'));
  assert.deepEqual(readdirSync('/router'), ['inference.sock']);
  attacks('direct');
  symlinkSync(hostTargets.secret, '/work/secret-link');
  blocked('symlink-host-secret-read', () => readFileSync('/work/secret-link'));
  symlinkSync(hostTargets.policy, '/work/policy-link');
  blocked('symlink-host-policy-write', () => writeFileSync('/work/policy-link', 'BREACHED'));
  symlinkSync('/proc/1/root' + hostTargets.secret, '/work/proc-link');
  blocked('proc-root-secret-read', () => readFileSync('/work/proc-link'));

  const git = (...args: string[]) => {
    const r = spawnSync('git', args, { cwd: '/work/repo', encoding: 'utf8', env: { ...process.env, GIT_CONFIG_NOSYSTEM: '1', GIT_CONFIG_GLOBAL: '/dev/null' } });
    assert.equal(r.status, 0, r.stderr + r.stdout);
    return r.stdout + r.stderr;
  };
  mkdirSync('/work/repo');
  git('init', '-q');
  git('config', 'user.name', 'Isolation Fixture');
  git('config', 'user.email', 'fixture@example.invalid');
  writeFileSync('/work/repo/.git/hooks/pre-commit', '#!/bin/sh\nexec node /fixture/worker.ts attack\n', { mode: 0o700 });
  const hook = git('commit', '-q', '--allow-empty', '-m', 'execute malicious hook inside boundary');
  const hookEvents = hook.trim().split('\n').filter(Boolean).map(line => JSON.parse(line));
  assert.ok(hookEvents.length >= 10, 'pre-commit hook did not execute adversarial checks');
  emit('git-hook-executed', { committed: git('rev-parse', '--verify', 'HEAD').trim().length === 40, events: hookEvents });
  const descendant = spawnSync(process.execPath, ['/fixture/worker.ts', 'attack'], { encoding: 'utf8' });
  assert.equal(descendant.status, 0, descendant.stderr);
  emit('child-attacks', descendant.stdout.trim().split('\n').map(line => JSON.parse(line)));

  const interfaces = networkInterfaces();
  assert.deepEqual(Object.keys(interfaces), ['lo']);
  emit('network-interfaces', Object.keys(interfaces));
  for (const [host, port] of [['1.1.1.1', 443], ['192.168.65.2', 443], ['169.254.169.254', 80], ['127.0.0.1', 20128], ['::1', 20128], ['2606:4700:4700::1111', 443]] as [string, number][]) {
    const code = await new Promise<string>((resolve, reject) => {
      const socket = connect({ host, port });
      socket.setTimeout(1000, () => { socket.destroy(); resolve('TIMEOUT'); });
      socket.once('connect', () => { socket.destroy(); reject(new Error(`Egress succeeded: ${host}:${port}`)); });
      socket.once('error', error => resolve(errorCode(error)!));
    });
    emit('blocked-egress', { host, port, code });
  }
  const resolver = new Resolver({ timeout: 500, tries: 1 });
  resolver.setServers(['1.1.1.1']);
  let dnsCode: string | undefined;
  try { await resolver.resolve4('example.com'); } catch (error) { dnsCode = errorCode(error); }
  assert.ok(dnsCode, 'DNS egress unexpectedly succeeded');
  emit('blocked-dns', dnsCode);

  async function inference(path: string, expected: number, options: { method?: string, token?: string, epoch?: string, body?: unknown } = {}) {
    const body = typeof options.body === 'string' ? options.body : JSON.stringify(options.body ?? { model: 'fixture/route', messages: [], max_tokens: 16 });
    const result = await new Promise<{ status: number, body: string }>((resolve, reject) => {
      const req = request({ socketPath: '/router/inference.sock', path, method: options.method ?? 'POST', headers: {
        authorization: `Bearer ${options.token ?? process.env.FIXTURE_TOKEN}`, 'x-gaffer-policy-epoch': options.epoch ?? '7',
        'content-type': 'application/json', 'content-length': Buffer.byteLength(body),
      } }, res => { let text = ''; res.on('data', chunk => { text += chunk; }); res.on('end', () => resolve({ status: res.statusCode!, body: text })); });
      req.setTimeout(2000, () => req.destroy(new Error('Inference fixture timeout')));
      req.on('error', reject);
      req.end(body);
    });
    assert.equal(result.status, expected, `${path}: ${result.body}`);
    emit('inference-fixture', { path, method: options.method ?? 'POST', status: result.status });
  }
  for (const path of ['/api/providers', '/api/management', '/v1/models', '/api/v1/responses', '/v1/v1/responses', '/codex/responses', '/v1/chat/completions?admin=true', '/v1/../api/providers', '/%61pi/providers', 'http://localhost/api/providers']) await inference(path, 404);
  for (const method of ['GET', 'PUT', 'DELETE', 'OPTIONS']) await inference('/v1/responses', 404, { method });
  await inference('/v1/responses', 401, { token: 'wrong' });
  await inference('/v1/responses', 403, { epoch: '6' });
  await inference('/v1/responses', 403, { body: { model: 'unapproved/route', max_tokens: 16 } });
  await inference('/v1/responses', 422, { body: { model: 'fixture/route', max_tokens: 65 } });
  await inference('/v1/responses', 400, { body: { model: 'fixture/route', max_tokens: 16, upstream_url: 'http://host' } });
  await inference('/v1/responses', 413, { body: 'x'.repeat(5000) });
  for (const path of ['/v1/chat/completions', '/v1/messages', '/v1/responses']) await inference(path, 200);
  await inference('/v1/responses', 429);

  const cgroup = (name: string) => readFileSync(`/sys/fs/cgroup/${name}`, 'utf8').trim();
  assert.equal(cgroup('memory.max'), '134217728');
  assert.equal(cgroup('memory.swap.max'), '0');
  assert.equal(cgroup('pids.max'), '64');
  assert.equal(cgroup('cpu.max'), '50000 100000');
  emit('cgroup-limits', Object.fromEntries(['memory.max', 'memory.swap.max', 'pids.max', 'cpu.max'].map(key => [key, cgroup(key)])));
  const cpuBefore = cgroup('cpu.stat');
  const until = Date.now() + 1500;
  while (Date.now() < until) Math.sqrt(Math.random());
  const cpuAfter = cgroup('cpu.stat');
  const throttled = (s: string) => Number(s.match(/nr_throttled (\d+)/)![1]);
  assert.ok(throttled(cpuAfter) > throttled(cpuBefore));
  emit('cpu-throttled', { before: cpuBefore, after: cpuAfter });

  const children: ReturnType<typeof spawn>[] = [];
  let spawnFailure = '';
  for (let i = 0; i < 80; i++) {
    const child = spawn('/bin/sleep', ['30'], { stdio: 'ignore' });
    children.push(child);
    const outcome = await new Promise<string>(resolve => { child.once('spawn', () => resolve('spawned')); child.once('error', error => resolve(errorCode(error)!)); });
    if (outcome !== 'spawned') { spawnFailure = outcome; break; }
  }
  assert.equal(spawnFailure, 'EAGAIN');
  emit('pids-limited', { spawned: children.length - 1, current: cgroup('pids.current'), events: cgroup('pids.events'), code: spawnFailure });
  await Promise.all(children.filter(child => child.pid).map(child => new Promise<void>(resolve => { child.once('close', () => resolve()); child.kill('SIGKILL'); })));

  for (const [directory, maxMiB] of [['/work', 32], ['/tmp', 8], ['/dev/shm', 4]] as [string, number][]) {
    const file = openSync(`${directory}/fill`, 'w');
    let bytes = 0;
    let diskCode = '';
    try { while (bytes < (maxMiB + 1) * 1024 * 1024) bytes += writeSync(file, Buffer.alloc(1024 * 1024, 0x61)); }
    catch (error) { diskCode = errorCode(error)!; }
    finally { closeSync(file); unlinkSync(`${directory}/fill`); }
    assert.equal(diskCode, 'ENOSPC');
    assert.ok(bytes <= maxMiB * 1024 * 1024);
    emit('disk-limited', { directory, bytes, code: diskCode });
  }
  emit('probe-complete', true);
} else {
  throw new Error(`Unknown fixture mode: ${mode}`);
}
