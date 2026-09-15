// Trusted bootstrap, fixed synthetic mock, and explicitly separated untrusted fixture modes.
// This module does nothing on import; native entrypoints refuse macOS before opening files.
import assert from 'node:assert/strict';
import * as fs from 'node:fs';
import { spawn, spawnSync } from 'node:child_process';
import { request, createServer } from 'node:http';
import { connect } from 'node:net';
import { createSocket } from 'node:dgram';
import { Resolver } from 'node:dns/promises';
import { networkInterfaces } from 'node:os';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { setTimeout as delay } from 'node:timers/promises';
import { createHash } from 'node:crypto';
import type { Profile, Case, Role } from './run.ts';

type Endpoint = { protocol: string; host: string; port: number; label: string };
type Config = { profile: Profile; profileHash: string; runId: string; role: Role; testCase: Case; token: string; endpoints: Endpoint[] };
const text = (path: string) => fs.readFileSync(path, 'utf8').trim();
const code = (error: unknown) => (error as NodeJS.ErrnoException).code;
const DENIED = ['EACCES', 'EPERM', 'EROFS', 'ENOENT'];
function deny(name: string, action: () => unknown) {
  let result: string | undefined;
  try { action(); } catch (e) { result = code(e); }
  assert.ok(DENIED.includes(result ?? ''), `${name}: ${result ?? 'operation succeeded'}`);
  return { name, error: result };
}
function native(tool: string, args: string[]) {
  return spawnSync(tool, args, { encoding: 'utf8', timeout: 2000, maxBuffer: 32768,
    env: { PATH: '/usr/bin:/bin', LANG: 'C', LC_ALL: 'C', HOME: '/nonexistent', GIT_CONFIG_NOSYSTEM: '1', GIT_CONFIG_GLOBAL: '/dev/null' }, stdio: ['ignore', 'pipe', 'pipe'] });
}
function denyCommand(tool: string, args: string[]) {
  const r = native(tool, args);
  assert.equal(r.error, undefined); assert.equal(r.signal, null); assert.notEqual(r.status, 0);
  assert.match(r.stderr, /Operation not permitted|Permission denied|Read-only file system/);
  return { argv: [tool, ...args], status: r.status, stderr: r.stderr };
}
const fields = (value: string) => Object.fromEntries(value.split('\n').filter(Boolean).map(line => { const [k, v] = line.trim().split(/\s+/); return [k, Number(v)]; }));
function memory() {
  return { current: Number(text('/sys/fs/cgroup/memory.current')), stat: fields(text('/sys/fs/cgroup/memory.stat')),
    events: fields(text('/sys/fs/cgroup/memory.events')), swap: Number(text('/sys/fs/cgroup/memory.swap.current')) };
}
function noOOM(before: ReturnType<typeof memory>, after: ReturnType<typeof memory>) {
  assert.equal(after.events.oom, before.events.oom); assert.equal(after.events.oom_kill, before.events.oom_kill); assert.equal(after.swap, 0);
}
function statfs(path: string) {
  const s = fs.statfsSync(path);
  return { type: s.type, bsize: s.bsize, blocks: s.blocks, bfree: s.bfree, bavail: s.bavail, files: s.files, ffree: s.ffree };
}
function writeGate(value: unknown, initialize = false) {
  const body = JSON.stringify(value); assert.ok(Buffer.byteLength(body) < 16384);
  // Allocate report space before pressure. Overwrites never extend it under log ENOSPC.
  const bytes = Buffer.from(body.padEnd(16384, ' '));
  const fd = fs.openSync('/log/gate.json', initialize ? 'wx' : 'r+');
  try { fs.writeSync(fd, bytes, 0, bytes.length, 0); } finally { fs.closeSync(fd); }
}
function emit(name: string, value: unknown) {
  const line = JSON.stringify({ name, value }) + '\n';
  assert.ok(fs.statSync('/log/events.jsonl').size + Buffer.byteLength(line) <= 128 * 1024, 'structured log budget');
  fs.appendFileSync('/log/events.jsonl', line);
}
function config(): Config {
  const s = fs.lstatSync('/fixture/config.json');
  assert.ok(s.isFile() && !s.isSymbolicLink()); assert.equal(s.uid, 0); assert.equal(s.mode & 0o222, 0);
  assert.ok(s.size <= 65536);
  const c = JSON.parse(text('/fixture/config.json')) as Config;
  assert.match(c.runId, /^[a-f0-9]{24}$/); assert.match(c.profileHash, /^[a-f0-9]{64}$/); assert.match(c.token, /^[a-f0-9]{64}$/);
  assert.ok(['worker', 'mock'].includes(c.role)); assert.equal(c.profile.status, 'measured'); assert.equal(c.profile.unattendedSupported, false);
  return c;
}
function mounts() {
  return text('/proc/self/mountinfo').split('\n').map(line => {
    const [left, right] = line.split(' - '), a = left.split(' '), b = right.split(' ');
    assert.ok(left && right && !/\\/.test(a[4]), 'unexpected mount escaping');
    return { id: a[0], parent: a[1], device: a[2], root: a[3], path: a[4], options: a[5].split(','), type: b[0], source: b[1], superOptions: b[2].split(',') };
  });
}
function namespaces() { return Object.fromEntries(['user', 'pid', 'net', 'ipc', 'mnt', 'cgroup'].map(name => [name, fs.readlinkSync(`/proc/self/ns/${name}`)])); }
async function unix(c: Config, path: string, expected: number, options: { method?: string; token?: string; epoch?: string; body?: unknown } = {}) {
  const body = typeof options.body === 'string' ? options.body : JSON.stringify(options.body ?? { model: 'fixture/route', input: 'synthetic', max_output_tokens: 64 });
  const response = await new Promise<{ status: number; body: string }>((done, fail) => {
    const req = request({ socketPath: '/router/inference.sock', path, method: options.method ?? 'POST', agent: false,
      headers: { authorization: `Bearer ${options.token ?? c.token}`, 'x-gaffer-policy-epoch': options.epoch ?? '7',
        'content-type': 'application/json', 'content-length': Buffer.byteLength(body) } }, res => {
      let output = ''; res.on('data', chunk => { output += chunk; if (output.length > 4096) req.destroy(new Error('reply limit')); });
      res.on('end', () => done({ status: res.statusCode!, body: output })); res.on('error', fail);
    });
    req.setTimeout(1500, () => req.destroy(new Error('UDS timeout'))); req.on('error', fail); req.end(body);
  });
  assert.equal(response.status, expected, `${path}: ${response.body}`);
  return { path, method: options.method ?? 'POST', status: response.status, body: response.body };
}
async function bootstrap(c: Config) {
  fs.writeFileSync('/log/events.jsonl', '', { flag: 'wx', mode: 0o600 });
  try {
    assert.equal(process.pid, 1, 'PrivatePIDs degraded: bootstrap is not PID1');
    assert.equal(process.version, c.profile.identity.nodeVersion); assert.equal(process.arch, 'x64');
    assert.equal(text('/proc/sys/kernel/osrelease'), c.profile.identity.kernelRelease);
    assert.equal(createHash('sha256').update(fs.readFileSync('/etc/os-release')).digest('hex'), c.profile.identity.osReleaseSha256);
    assert.equal(createHash('sha256').update(fs.readFileSync('/fixture/probe.ts')).digest('hex'), c.profile.artifacts.find(a => a.path.endsWith('/rootfs/fixture/probe.ts'))!.sha256);
    const uid = c.role === 'worker' ? c.profile.ids.workerUid : c.profile.ids.mockUid;
    const gid = c.role === 'worker' ? c.profile.ids.workerGid : c.profile.ids.mockGid;
    assert.equal(process.getuid!(), uid); assert.equal(process.geteuid!(), uid); assert.equal(process.getgid!(), gid); assert.equal(process.getegid!(), gid);
    assert.deepEqual([...new Set(process.getgroups!())].sort((a, b) => a - b), [gid, c.profile.ids.socketGid].sort((a, b) => a - b));
    for (const path of ['/proc/self/uid_map', '/proc/self/gid_map']) assert.equal(text(path).replace(/\s+/g, ' '), '0 0 65536', 'identity user mapping drift');
    const status = text('/proc/self/status');
    for (const field of ['CapInh', 'CapPrm', 'CapEff', 'CapBnd', 'CapAmb']) assert.match(status, new RegExp(`^${field}:\\s+0+$`, 'm'));
    assert.match(status, /^NoNewPrivs:\s+1$/m); assert.match(status, /^Seccomp:\s+2$/m);
    const ns = namespaces(); for (const [name, value] of Object.entries(ns)) assert.notEqual(value, c.profile.identity.controllerNamespaces[name]);
    assert.equal(text('/proc/self/cgroup'), '0::/'); assert.equal(fs.statfsSync('/sys/fs/cgroup').type, 0x63677270);
    assert.equal(text('/sys/fs/cgroup/cpu.max'), '50000 100000'); assert.equal(text('/sys/fs/cgroup/memory.max'), '134217728');
    assert.equal(text('/sys/fs/cgroup/memory.swap.max'), '0'); assert.equal(text('/sys/fs/cgroup/pids.max'), '64', 'effective pids.max mismatch');
    const limits = text('/proc/self/limits');
    assert.match(limits, /^Max open files\s+256\s+256\s+files$/m); assert.match(limits, /^Max core file size\s+0\s+0\s+bytes$/m);
    assert.equal(fs.readlinkSync('/proc/1/ns/pid'), ns.pid);
    assert.ok(fs.readdirSync('/proc').filter(s => /^\d+$/.test(s)).every(s => s === '1'), 'foreign process visible before canaries');
    const table = mounts();
    const mount = (path: string) => { const matches = table.filter(m => m.path === path); assert.equal(matches.length, 1, `mount ${path}`); return matches[0]; };
    assert.ok(mount('/').options.includes('ro')); assert.equal(mount('/proc').type, 'proc'); assert.ok(mount('/sys/fs/cgroup').options.includes('ro'));
    const writable: [string, number, number][] = c.role === 'worker'
      ? [['/work', 33554432, 4096], ['/tmp', 8388608, 1024], ['/dev/shm', 4194304, 1024], ['/log', 1048576, 64]]
      : [['/router', 1048576, 64], ['/log', 1048576, 64]];
    for (const [path, bytes, inodes] of writable) {
      const m = mount(path), stat = statfs(path);
      assert.equal(m.type, 'tmpfs'); assert.equal(stat.type, 0x01021994);
      for (const flag of ['rw', 'nosuid', 'nodev', ...(path === '/work' ? [] : ['noexec'])]) assert.ok(m.options.includes(flag), `${path} missing ${flag}`);
      assert.ok(m.superOptions.includes('noswap'), `${path} missing noswap`);
      assert.equal(stat.blocks * stat.bsize, bytes); assert.equal(stat.files, inodes);
      assert.equal(fs.statSync(path).uid, uid);
      const canary = `${path}/bootstrap-canary`; fs.writeFileSync(canary, 'below-limit', { flag: 'wx' }); fs.unlinkSync(canary);
    }
    for (const m of table) {
      if (m.options.includes('rw')) assert.ok(writable.some(([path]) => path === m.path) || m.type === 'proc', `unexpected writable mount ${m.path}`);
    }
    for (const key of Object.keys(process.env)) assert.ok(['PATH', 'LANG', 'LC_ALL', 'HOME', 'GIT_CONFIG_NOSYSTEM', 'GIT_CONFIG_GLOBAL', 'INVOCATION_ID', 'SYSTEMD_EXEC_PID'].includes(key), `unexpected environment ${key}`);
    assert.equal(process.env.PATH, '/usr/bin:/bin'); assert.equal(process.env.HOME, '/nonexistent');
    const descriptors: Record<string, string> = {};
    for (const fd of fs.readdirSync('/proc/self/fd')) {
      let target: string;
      try { target = fs.readlinkSync(`/proc/self/fd/${fd}`); } catch (e) { if (code(e) === 'ENOENT') continue; throw e; }
      descriptors[fd] = target;
      assert.ok(target === '/dev/null' || /^anon_inode:\[(eventpoll|eventfd)\]$/.test(target) || /^pipe:\[\d+\]$/.test(target), `unexpected descriptor ${fd}: ${target}`);
    }
    for (const path of ['/etc/resolv.conf', '/run/dbus/system_bus_socket', '/run/systemd/journal/socket', '/run/docker.sock', '/var/run/docker.sock', '/run/host-services/ssh-auth.sock']) assert.ok(!fs.existsSync(path), `forbidden host surface ${path}`);
    assert.ok(Object.keys(networkInterfaces()).every(name => name === 'lo'));
    assert.equal(text('/proc/net/route').split('\n').length, 1, 'external IPv4 route');
    assert.ok(!text('/proc/net/ipv6_route').split('\n').some(line => line.trim() && !line.trim().endsWith('lo')), 'external IPv6 route');
    const canaries = [
      deny('setuid0', () => process.setuid!(0)), deny('root-remains-readonly', () => fs.writeFileSync('/escape', 'x')),
      deny('cgroup-write', () => fs.writeFileSync('/sys/fs/cgroup/pids.max', 'max')),
      denyCommand('/usr/bin/unshare', ['--user', '--map-root-user', '/usr/bin/true']),
      denyCommand('/usr/bin/nsenter', ['--target', '1', '--mount', '/usr/bin/true']),
      denyCommand('/usr/bin/mount', ['-o', 'remount,rw', '/']),
    ];
    const network = await egress([
      { protocol: 'tcp', host: '127.0.0.1', port: 9, label: 'pre-code socket denial' },
      { protocol: 'udp', host: '::1', port: 9, label: 'pre-code socket denial' },
    ], true);
    let scoped: unknown = null;
    if (c.role === 'worker') {
      const dir = fs.statSync('/router'), sock = fs.statSync('/router/inference.sock');
      assert.equal(dir.mode & 0o777, 0o750); assert.equal(dir.uid, c.profile.ids.mockUid); assert.equal(dir.gid, c.profile.ids.socketGid);
      assert.ok(sock.isSocket()); assert.equal(sock.mode & 0o777, 0o660); assert.equal(sock.uid, c.profile.ids.mockUid); assert.equal(sock.gid, c.profile.ids.socketGid);
      assert.ok(mount('/router').options.includes('ro'));
      deny('replace-socket', () => fs.unlinkSync('/router/inference.sock')); deny('write-socket-directory', () => fs.writeFileSync('/router/escape', 'x'));
      scoped = await unix(c, '/v1/responses', 200);
      assert.equal(JSON.parse((scoped as any).body).instance, 'gate');
    }
    const current = memory(); assert.ok(current.current <= c.profile.controls.allowanceBytes, 'measured non-payload allowance exceeded');
    writeGate({ result: 'admitted', runId: c.runId, profileHash: c.profileHash, role: c.role, pid: process.pid,
      namespaces: ns, status, limits, mounts: table, descriptors, environment: process.env, canaries, network, scoped,
      memoryCurrent: current.current, memory: current }, true);
    for (let n = 0; n < 1500; n++) {
      if (fs.existsSync('/log/release.json')) {
        const s = fs.lstatSync('/log/release.json'); assert.ok(s.isFile() && !s.isSymbolicLink()); assert.equal(s.uid, 0); assert.equal(s.mode & 0o222, 0);
        assert.deepEqual(JSON.parse(text('/log/release.json')), { runId: c.runId, profileHash: c.profileHash });
        // execve replaces trusted PID1; no fork or repository callback before release.
        process.execve!('/usr/bin/node', ['/usr/bin/node', '/fixture/probe.ts', c.role === 'mock' ? '--mock' : '--fixture'], {
          PATH: '/usr/bin:/bin', LANG: 'C', LC_ALL: 'C', HOME: '/nonexistent', GIT_CONFIG_NOSYSTEM: '1', GIT_CONFIG_GLOBAL: '/dev/null',
        });
        throw new Error('execve returned');
      }
      await delay(20);
    }
    throw new Error('bootstrap release deadline');
  } catch (e) {
    const failure = { result: 'denied', runId: c.runId, profileHash: c.profileHash, role: c.role, error: String(e), repositoryStarted: false };
    writeGate(failure, !fs.existsSync('/log/gate.json'));
    // Remain observable; PID1's fixed RuntimeMaxSec bounds even controller death.
    await delay(60000); throw e;
  }
}

export async function egress(endpoints: Endpoint[], isolated: boolean) {
  assert.equal(process.platform, 'linux', 'native network probes require Linux');
  assert.ok(endpoints.length > 0 && endpoints.length <= 8);
  const results: unknown[] = [];
  for (const endpoint of endpoints) {
    assert.ok(['127.0.0.1', '::1'].includes(endpoint.host)); assert.ok(['tcp', 'udp', 'dns'].includes(endpoint.protocol));
    assert.ok(Number.isInteger(endpoint.port) && endpoint.port > 0 && endpoint.port < 65536);
    let error: string | undefined, received = false;
    try {
      if (endpoint.protocol === 'tcp') {
        await new Promise<void>((done, fail) => {
          const socket = connect(endpoint.port, endpoint.host);
          socket.setTimeout(1000, () => socket.destroy(Object.assign(new Error('timeout is not denial'), { code: 'TIMEOUT' })));
          socket.once('connect', () => { received = true; socket.destroy(); done(); }); socket.once('error', fail);
        });
      } else if (endpoint.protocol === 'dns') {
        const resolver = new Resolver({ timeout: 1000, tries: 1 });
        resolver.setServers([`${endpoint.host === '::1' ? '[::1]' : endpoint.host}:${endpoint.port}`]);
        try { assert.deepEqual(await resolver.resolve4('fixture.invalid'), ['127.0.0.1']); received = true; }
        finally { resolver.cancel(); }
      } else {
        await new Promise<void>((done, fail) => {
          const socket = createSocket(endpoint.host === '::1' ? 'udp6' : 'udp4');
          const timer = setTimeout(() => { socket.close(); fail(Object.assign(new Error('timeout is not denial'), { code: 'TIMEOUT' })); }, 1000);
          const finish = (e?: Error) => { clearTimeout(timer); try { socket.close(); } catch {} e ? fail(e) : done(); };
          socket.once('error', finish); socket.once('message', msg => { received = msg.toString() === 'synthetic-native-89'; finish(); });
          socket.send('synthetic-native-89', endpoint.port, endpoint.host, e => { if (e) finish(e); });
        });
      }
    } catch (e) { error = code(e); }
    if (isolated) assert.ok(['EPERM', 'EACCES', 'EAFNOSUPPORT'].includes(error ?? ''), `${endpoint.protocol}: ${error ?? 'egress succeeded'}; timeouts not accepted`);
    else { assert.equal(error, undefined); assert.equal(received, true, 'positive control unavailable'); }
    results.push({ endpoint, error: error ?? null, received });
  }
  for (const [family, host] of [['-4', '127.0.0.1'], ['-6', '::1']]) {
    // Identifier 0 forces raw sockets in iputils ping; exact binary hash is frozen.
    const argv = [family, '-e', '0', '-c', '1', '-W', '1', host];
    const r = native('/usr/bin/ping', argv);
    assert.equal(r.error, undefined); assert.equal(r.signal, null);
    if (isolated) { assert.notEqual(r.status, 0); assert.match(r.stderr, /Operation not permitted|Permission denied|Address family not supported/); }
    else assert.equal(r.status, 0, `raw positive control unavailable: ${r.stderr}`);
    results.push({ argv: ['/usr/bin/ping', ...argv], status: r.status, stdout: r.stdout, stderr: r.stderr });
  }
  return results;
}

async function mock(c: Config) {
  assert.equal(c.role, 'mock'); assert.equal(process.pid, 1);
  let instance: 'gate' | 'fixture' = 'gate', accepted = 0, pressureStarted = false;
  const paths = new Set(['/v1/chat/completions', '/v1/messages', '/v1/responses']);
  const start = () => {
    const server = createServer((req, res) => {
      const reply = (status: number, value: string) => {
        if (res.writableEnded) return;
        res.writeHead(status, { 'content-type': 'application/json', connection: 'close' });
        res.end(JSON.stringify({ fixture: true, instance, result: value })); req.resume();
      };
      if (req.method !== 'POST' || !paths.has(req.url ?? '')) return reply(404, 'not_inference');
      if (req.headers.authorization !== `Bearer ${c.token}`) return reply(401, 'scope');
      if (req.headers['x-gaffer-policy-epoch'] !== '7') return reply(403, 'epoch');
      let bytes = 0; const chunks: Buffer[] = [];
      req.setTimeout(1000, () => req.destroy());
      req.on('data', chunk => { bytes += chunk.length; if (bytes > 4096) reply(413, 'body_limit'); else chunks.push(chunk); });
      req.on('end', () => {
        if (res.writableEnded) return;
        let value: any; try { value = JSON.parse(Buffer.concat(chunks).toString()); } catch { return reply(400, 'invalid_json'); }
        if (!value || Array.isArray(value) || typeof value !== 'object') return reply(400, 'envelope');
        if (value.model !== 'fixture/route') return reply(403, 'route');
        const allowed = new Set(['model', 'messages', 'input', 'max_tokens', 'max_output_tokens']);
        if (Object.keys(value).some(key => !allowed.has(key))) return reply(400, 'unsupported_field');
        if ('max_tokens' in value && 'max_output_tokens' in value) return reply(400, 'ambiguous_output_limit');
        const limit = value.max_output_tokens ?? value.max_tokens;
        if (!Number.isInteger(limit) || limit < 1 || limit > 64) return reply(422, 'output_limit');
        if (accepted >= (instance === 'gate' ? 1 : 3)) return reply(429, 'attempt_budget');
        accepted++; reply(200, 'synthetic inference accepted');
        if (instance === 'gate') {
          // Two sequential server instances, independent frozen budgets. Same read-only-bound directory.
          server.close(() => { fs.unlinkSync('/router/inference.sock'); instance = 'fixture'; accepted = 0; start(); });
        } else if (accepted === 3 && /^(bytes|inodes)-(socket|mock-log)$/.test(c.testCase) && !pressureStarted) {
          pressureStarted = true;
          setImmediate(() => { try { const result = pressure(c.testCase); writeGate({ result: 'fixture-complete', pressure: result }); }
            catch (e) { writeGate({ result: 'fixture-failed', error: String(e) }); } });
        }
      });
    });
    server.maxConnections = 4; server.maxRequestsPerSocket = 1; server.maxHeadersCount = 16;
    server.headersTimeout = 2000; server.requestTimeout = 2000; server.keepAliveTimeout = 100;
    server.on('error', e => { writeGate({ result: 'fixture-failed', error: String(e) }); });
    server.listen('/router/inference.sock', () => {
      // Controller assigns shared group after positive-owned socket inspection;
      // mock keeps @privileged (including chown) denied.
      fs.chmodSync('/router/inference.sock', 0o660);
      emit(instance === 'gate' ? 'mock-ready' : 'mock-fixture-ready', { instance, accepted, maxConnections: 4 });
    });
  };
  start();
}

function attacks(c: Config) {
  const results: unknown[] = [];
  for (const name of ['host-home', 'host-secret', 'runner-policy', 'daemon-state', 'ssh-agent', 'publication-credential', 'provider-credential']) {
    const path = `/var/lib/gaffer-native/evidence/${c.runId}/${name}`;
    results.push(deny(`${name}-read`, () => fs.readFileSync(path)), deny(`${name}-write`, () => fs.writeFileSync(path, 'BREACHED')));
  }
  return results;
}
function baseline(c: Config) {
  const direct = attacks(c);
  const secret = `/var/lib/gaffer-native/evidence/${c.runId}/host-secret`;
  fs.symlinkSync(secret, '/work/secret-link'); deny('symlink', () => fs.readFileSync('/work/secret-link'));
  fs.symlinkSync(`/proc/1/root${secret}`, '/work/proc-link'); deny('proc-root', () => fs.readFileSync('/work/proc-link'));
  fs.mkdirSync('/work/repo');
  const git = (...args: string[]) => {
    const r = spawnSync('/usr/bin/git', args, { cwd: '/work/repo', encoding: 'utf8', timeout: 3000, maxBuffer: 65536,
      env: { PATH: '/usr/bin:/bin', HOME: '/nonexistent', GIT_CONFIG_NOSYSTEM: '1', GIT_CONFIG_GLOBAL: '/dev/null' } });
    assert.equal(r.error, undefined); assert.equal(r.status, 0, r.stderr); return r.stdout;
  };
  git('init', '-q'); git('config', 'user.name', 'Native Isolation Fixture'); git('config', 'user.email', 'fixture@example.invalid');
  fs.writeFileSync('/work/repo/.git/hooks/pre-commit', '#!/usr/bin/dash\nexec /usr/bin/node /fixture/probe.ts --attack\n', { mode: 0o700 });
  git('commit', '-q', '--allow-empty', '-m', 'synthetic hostile hook');
  assert.ok(!fs.existsSync('/work/repo/.git/objects/info/alternates'));
  assert.ok(fs.lstatSync('/work/repo/.git').isDirectory());
  assert.equal(git('rev-parse', '--git-common-dir').trim(), '.git');
  return { direct, head: git('rev-parse', 'HEAD'), independentGit: true };
}
function pressure(testCase: string, flood = false) {
  const match = testCase.match(/^(bytes|inodes)-(work|tmp|shm|log|socket|mock-log)$/); assert.ok(match);
  const mode = match[1], path = ({ work: '/work', tmp: '/tmp', shm: '/dev/shm', log: '/log', socket: '/router', 'mock-log': '/log' } as Record<string, string>)[match[2]];
  const before = statfs(path), memBefore = memory();
  const control = `${path}/pressure-control`; fs.writeFileSync(control, Buffer.alloc(4096, 0x61), { flag: 'wx' }); fs.unlinkSync(control);
  const dir = `${path}/pressure`; if (!flood) fs.mkdirSync(dir);
  let error = '', bytes = 0, created = 0;
  if (mode === 'bytes') {
    const fd = fs.openSync(flood ? '/log/events.jsonl' : `${dir}/fill`, flood ? 'a' : 'wx');
    const block = Buffer.alloc(4096, 0x61); block[4095] = 10;
    try { while (bytes <= before.blocks * before.bsize) bytes += fs.writeSync(fd, block); }
    catch (e) { error = code(e)!; } finally { fs.closeSync(fd); }
  } else {
    try { for (; created <= before.files; created++) fs.closeSync(fs.openSync(`${dir}/i${created}`, 'wx')); }
    catch (e) { error = code(e)!; }
  }
  const after = statfs(path), memAfter = memory();
  assert.equal(error, 'ENOSPC'); noOOM(memBefore, memAfter);
  if (mode === 'bytes') { assert.equal(after.bavail, 0); assert.ok(after.ffree > 0); assert.ok(before.ffree - after.ffree <= 2); }
  else { assert.equal(after.ffree, 0); assert.ok(after.bavail > 0); assert.ok((before.bavail - after.bavail) * before.bsize < 1024 ** 2); }
  return { path, mode, belowLimit: true, error, bytes, created, before, after, memBefore, memAfter };
}
async function inference(c: Config) {
  const results: unknown[] = [];
  for (const path of ['/api/providers', '/api/management', '/v1/models', '/api/v1/responses', '/v1/v1/responses', '/codex/responses', '/v1/chat/completions?admin=true', '/v1/../api/providers', '/%61pi/providers', 'http://localhost/api/providers']) results.push(await unix(c, path, 404));
  for (const method of ['GET', 'PUT', 'DELETE', 'OPTIONS']) results.push(await unix(c, '/v1/responses', 404, { method }));
  results.push(await unix(c, '/v1/responses', 401, { token: 'wrong' }), await unix(c, '/v1/responses', 403, { epoch: '6' }),
    await unix(c, '/v1/responses', 403, { body: { model: 'unapproved/route', max_tokens: 16 } }),
    await unix(c, '/v1/responses', 422, { body: { model: 'fixture/route', max_tokens: 65 } }),
    await unix(c, '/v1/responses', 400, { body: { model: 'fixture/route', max_tokens: 16, upstream_url: 'http://host' } }),
    await unix(c, '/v1/responses', 413, { body: 'x'.repeat(5000) }));
  for (const path of ['/v1/chat/completions', '/v1/messages', '/v1/responses']) results.push(await unix(c, path, 200));
  // Mock pressure starts after the third accepted response and may fill socket filesystem.
  if (!/^(bytes|inodes)-(socket|mock-log)$/.test(c.testCase)) results.push(await unix(c, '/v1/responses', 429));
  return results;
}
async function tree(c: Config) {
  process.on('SIGTERM', () => {});
  const child = spawn('/usr/bin/dash', ['-c', 'trap "" TERM; /usr/bin/dash -c \'trap "" TERM; while :; do /usr/bin/sleep 1; done\' & while :; do /usr/bin/sleep 1; done'], { detached: true, stdio: 'ignore' });
  await new Promise<void>((done, fail) => { child.once('spawn', done); child.once('error', fail); });
  child.unref(); emit('tree-started', { pid: process.pid, detachedPid: child.pid, testCase: c.testCase });
  if (c.testCase === 'parent-exit') { await delay(1000); process.exit(0); }
  await delay(60000);
}
async function fixture(c: Config) {
  assert.equal(c.role, 'worker'); assert.equal(process.pid, 1);
  emit('repository-start', { pid: process.pid, runId: c.runId });
  const results: unknown[] = [];
  try {
    if (['stop', 'expiry', 'parent-exit', 'controller-loss', 'observation-loss', 'reboot'].includes(c.testCase)) { await tree(c); return; }
    if (c.testCase === 'baseline') {
      results.push(baseline(c));
      const child = native('/usr/bin/node', ['/fixture/probe.ts', '--attack']); assert.equal(child.status, 0, child.stderr);
      assert.ok(text('/log/events.jsonl').includes('hook-attacks'));
      fs.writeFileSync('/work/plugin', '#!/usr/bin/dash\nexec /usr/bin/node /fixture/probe.ts --attack\n', { mode: 0o700 });
      const plugin = native('/work/plugin', []); assert.equal(plugin.status, 0, plugin.stderr);
      results.push({ childStatus: child.status, executablePluginStatus: plugin.status });
    }
    if (c.testCase === 'baseline' || /^(bytes|inodes)-(socket|mock-log)$/.test(c.testCase)) results.push(await inference(c));
    if (/^(bytes|inodes)-(work|tmp|shm|log)$/.test(c.testCase)) results.push(pressure(c.testCase));
    if (c.testCase === 'cpu') {
      const before = fields(text('/sys/fs/cgroup/cpu.stat')); const until = Date.now() + 2000;
      while (Date.now() < until) Math.sqrt(Math.random());
      const after = fields(text('/sys/fs/cgroup/cpu.stat')); assert.ok(after.nr_throttled > before.nr_throttled); assert.ok(after.throttled_usec > before.throttled_usec);
      results.push({ before, after });
    }
    if (c.testCase === 'pids') {
      const before = fields(text('/sys/fs/cgroup/pids.events')); const children: ReturnType<typeof spawn>[] = []; let error = '';
      try {
        for (let i = 0; i < 80; i++) {
          const child = spawn('/usr/bin/sleep', ['30'], { stdio: 'ignore' }); children.push(child);
          const outcome = await new Promise<string>(done => { child.once('spawn', () => done('spawned')); child.once('error', e => done(code(e)!)); });
          if (outcome !== 'spawned') { error = outcome; break; }
        }
        const after = fields(text('/sys/fs/cgroup/pids.events')); assert.equal(error, 'EAGAIN'); assert.ok(children.length > 1); assert.ok(after.max > before.max);
        results.push({ before, after, error, children: children.length - 1 });
      } finally {
        await Promise.all(children.filter(child => child.pid).map(child => new Promise<void>(done => { child.once('close', () => done()); child.kill('SIGKILL'); })));
      }
    }
    if (c.testCase === 'swap') {
      const allocations: Buffer[] = []; const before = memory();
      for (let i = 0; i < 16; i++) { allocations.push(Buffer.alloc(1024 ** 2, 0x61)); assert.equal(memory().swap, 0); }
      const after = memory(); noOOM(before, after); results.push({ before, after, allocated: allocations.reduce((n, b) => n + b.length, 0) });
    }
    if (c.testCase === 'oom') {
      const allocations = [Buffer.alloc(1024 ** 2, 0x61)];
      emit('allocation-control', { bytes: allocations[0].length, memory: memory(), mounts: ['/work', '/tmp', '/dev/shm', '/log'].map(path => ({ path, stat: statfs(path) })) });
      for (let i = 0; i < 256; i++) { allocations.push(Buffer.alloc(1024 ** 2, 0x61)); await delay(30); }
      throw new Error('memory ceiling failed to kill fixture');
    }
    if (c.testCase === 'log-flood') {
      results.push(pressure('bytes-log', true));
      // Structured report uses preallocated gate file even when log mount is full.
      writeGate({ result: 'fixture-complete', results }); await delay(60000); return;
    }
    if (c.testCase === 'egress') results.push(await egress(c.endpoints, true));
    writeGate({ result: 'fixture-complete', results });
    if (!/^(bytes|inodes)-log$/.test(c.testCase)) emit('fixture-complete', results);
    await delay(60000); // Keep mounts/counters observable until explicit complete-tree stop.
  } catch (e) {
    writeGate({ result: 'fixture-failed', error: String(e), results });
    try { emit('fixture-failed', String(e)); } catch {}
    await delay(60000);
  }
}
export async function main(argv: string[]) {
  assert.equal(process.platform, 'linux', 'native probe requires Linux; no effects performed');
  assert.equal(argv.length, 1); assert.ok(['--bootstrap', '--fixture', '--mock', '--attack'].includes(argv[0]));
  const c = config();
  if (argv[0] === '--bootstrap') return bootstrap(c);
  if (argv[0] === '--mock') return mock(c);
  if (argv[0] === '--attack') { emit('hook-attacks', attacks(c)); return; }
  return fixture(c);
}
if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) main(process.argv.slice(2)).catch(error => { console.error(String(error)); process.exitCode = 1; });
