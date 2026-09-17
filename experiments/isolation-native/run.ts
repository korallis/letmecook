// Offline import is inert. Only explicit --execute/--initialize-journal can mutate Linux.
import assert from 'node:assert/strict';
import { createHash, randomBytes } from 'node:crypto';
import * as fs from 'node:fs';
import { join, dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { execFileSync } from 'node:child_process';
import { setTimeout as delay } from 'node:timers/promises';
import { arch, release } from 'node:os';
import { createServer as tcpServer } from 'node:net';
import { createSocket } from 'node:dgram';

export const PROCEDURE = 'fbc0b408d2023a769a24a8b48e856b52a6744c5efc4e91869775be65a4ec6b59';
export const BASE = '/var/lib/gaffer-native';
const CONTROLLER_GROUP = '/sys/fs/cgroup/gaffer89.slice';
export const CONTROLS = Object.freeze({
  cpuMax: '50000 100000', memoryMax: '134217728', swapMax: '0', pidsMax: '64',
  nofile: 256, core: 0, runtimeSeconds: 60, stopSeconds: 1, emptySeconds: 5,
  workerBytes: 45 * 1024 ** 2, mockBytes: 2 * 1024 ** 2,
  allowanceBytes: 32 * 1024 ** 2, reserveBytes: 16 * 1024 ** 2,
  evidenceBytes: 16 * 1024 ** 2, evidenceFiles: 128, journalBytes: 1024 ** 2, journalFiles: 16,
});
export const CASES = [
  'baseline', 'missing-pids-limit', 'runtime-drift', 'cpu', 'pids', 'swap', 'oom',
  'bytes-work', 'inodes-work', 'bytes-tmp', 'inodes-tmp', 'bytes-shm', 'inodes-shm',
  'bytes-log', 'inodes-log', 'bytes-socket', 'inodes-socket', 'bytes-mock-log', 'inodes-mock-log',
  'log-flood', 'egress', 'stop', 'expiry', 'parent-exit', 'launch-interrupt-before-gate',
  'launch-interrupt-after-gate', 'controller-loss', 'observation-loss', 'reboot',
] as const;
export type Case = typeof CASES[number];
export type Role = 'worker' | 'mock';
type Artifact = { path: string; sha256: string };
export type Profile = {
  schema: 1; id: string; status: 'measured'; unattendedSupported: false; procedureSha256: string;
  identity: { osReleaseSha256: string; kernelRelease: string; kernelConfigSha256: string;
    bootId: string; machineIdSha256: string; systemdVersion: string; nodeVersion: string;
    architecture: 'x64'; imageSha256: string; hypervisorSha256: string; rootfsSha256: string;
    controllerNamespaces: Record<string, string> };
  ids: { workerUid: number; workerGid: number; mockUid: number; mockGid: number; socketGid: number };
  storage: { evidenceDevice: number; evidenceInode: number; evidenceFsType: number;
    rootfsDevice: number; rootfsInode: number };
  artifacts: Artifact[]; controls: typeof CONTROLS;
  headroom: { controllerBytes: number; osBytes: number; diskReserveBytes: number; inodeReserve: number;
    controllerSliceDevice: number; controllerSliceInode: number };
};
export const hash = (bytes: string | Buffer) => createHash('sha256').update(bytes).digest('hex');
export const json = (value: unknown): string => {
  if (Array.isArray(value)) return `[${value.map(json).join(',')}]`;
  if (value !== null && typeof value === 'object') return `{${Object.keys(value).sort().map(k => `${JSON.stringify(k)}:${json((value as Record<string, unknown>)[k])}`).join(',')}}`;
  return JSON.stringify(value);
};
function keys(value: any, expected: string[]) {
  assert.ok(value && typeof value === 'object' && !Array.isArray(value), 'required object absent');
  assert.deepEqual(Object.keys(value).sort(), [...expected].sort(), 'missing or unknown fields');
}
function exactText(value: unknown) {
  assert.equal(typeof value, 'string');
  assert.ok((value as string).length > 0 && (value as string).length < 4096);
  assert.ok(!/[\x00-\x08\x0b-\x1f*?]/.test(value as string), 'wildcard/control identity');
  assert.ok(!/^(unknown|unmeasured|pending|any|null)$/i.test(value as string), 'unmeasured identity');
}
function digest(value: unknown) { assert.match(value as string, /^[a-f0-9]{64}$/); assert.notEqual(value, '0'.repeat(64)); }
export function validateProfile(value: unknown): Profile {
  const p = value as Profile;
  keys(p, ['schema', 'id', 'status', 'unattendedSupported', 'procedureSha256', 'identity', 'ids', 'storage', 'artifacts', 'controls', 'headroom']);
  assert.equal(p.schema, 1); assert.equal(p.id, 'm0-debian13-amd64-systemd-native-v1');
  assert.equal(p.status, 'measured', 'default/unmeasured profile is ineligible');
  assert.equal(p.unattendedSupported, false); assert.equal(p.procedureSha256, PROCEDURE);
  keys(p.identity, ['osReleaseSha256', 'kernelRelease', 'kernelConfigSha256', 'bootId', 'machineIdSha256',
    'systemdVersion', 'nodeVersion', 'architecture', 'imageSha256', 'hypervisorSha256', 'rootfsSha256', 'controllerNamespaces']);
  for (const [key, val] of Object.entries(p.identity)) {
    if (key.endsWith('Sha256')) digest(val);
    else if (key !== 'controllerNamespaces') exactText(val);
  }
  assert.equal(p.identity.architecture, 'x64');
  assert.match(p.identity.kernelRelease, /^6\.12\.[0-9]+[-.a-zA-Z0-9]*$/);
  assert.match(p.identity.systemdVersion, /^systemd 257[ .(]/);
  assert.match(p.identity.nodeVersion, /^v(2[4-9]|[3-9][0-9])\.\d+\.\d+$/);
  assert.match(p.identity.bootId, /^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/);
  keys(p.identity.controllerNamespaces, ['user', 'pid', 'net', 'ipc', 'mnt', 'cgroup']);
  for (const [name, id] of Object.entries(p.identity.controllerNamespaces)) assert.match(id, new RegExp(`^${name}:\\[[0-9]+\\]$`));
  keys(p.ids, ['workerUid', 'workerGid', 'mockUid', 'mockGid', 'socketGid']);
  for (const id of Object.values(p.ids)) assert.ok(Number.isInteger(id) && id > 0 && id < 65536);
  assert.notEqual(p.ids.workerUid, p.ids.mockUid);
  assert.equal(new Set([p.ids.workerGid, p.ids.mockGid, p.ids.socketGid]).size, 3);
  keys(p.storage, ['evidenceDevice', 'evidenceInode', 'evidenceFsType', 'rootfsDevice', 'rootfsInode']);
  for (const n of Object.values(p.storage)) assert.ok(Number.isSafeInteger(n) && n > 0);
  // One Linux profile, disk-backed ext4 or XFS only. tmpfs/overlay/NFS are not evidence storage.
  assert.ok([0xef53, 0x58465342].includes(p.storage.evidenceFsType));
  assert.equal(json(p.controls), json(CONTROLS), 'mandatory controls changed or absent');
  keys(p.headroom, ['controllerBytes', 'osBytes', 'diskReserveBytes', 'inodeReserve', 'controllerSliceDevice', 'controllerSliceInode']);
  for (const n of Object.values(p.headroom)) assert.ok(Number.isSafeInteger(n) && n > 0);
  assert.ok(p.headroom.controllerBytes >= 128 * 1024 ** 2 && p.headroom.osBytes >= 256 * 1024 ** 2);
  assert.ok(p.headroom.diskReserveBytes >= CONTROLS.evidenceBytes && p.headroom.inodeReserve >= 256);
  const required = [
    '/usr/bin/systemctl', '/usr/bin/systemd-run', '/usr/bin/systemd-analyze',
    '/usr/bin/node', '/usr/bin/git', '/usr/bin/unshare', '/usr/bin/nsenter',
    '/usr/bin/mount', '/usr/bin/ping', '/usr/bin/true', '/usr/bin/sleep', '/usr/bin/dash', '/usr/bin/env',
    `${BASE}/rootfs/fixture/probe.ts`, `${BASE}/controller/run.ts`,
    `${BASE}/image-provenance.json`, `${BASE}/hypervisor-provenance.json`,
  ];
  assert.ok(Array.isArray(p.artifacts) && p.artifacts.length === required.length);
  assert.deepEqual(p.artifacts.map(a => a.path).sort(), required.sort());
  for (const a of p.artifacts) { keys(a, ['path', 'sha256']); digest(a.sha256); }
  return p;
}
export function admission(p: unknown, platform: string, expectedHash: string, bytes: string): Profile {
  assert.equal(platform, 'linux', 'native execution requires Linux; no effects performed');
  digest(expectedHash); assert.equal(hash(bytes), expectedHash, 'operator-frozen profile digest mismatch');
  return validateProfile(p);
}

function regular(path: string, owner = 0) {
  const s = fs.lstatSync(path);
  assert.ok(s.isFile() && !s.isSymbolicLink() && s.nlink === 1, `unsafe file: ${path}`);
  assert.equal(s.uid, owner); assert.equal(s.mode & 0o022, 0);
  return s;
}
function trustedDirectory(path: string, owner = 0) {
  assert.equal(resolve(path), path);
  for (let dir = path; dir !== '/'; dir = dirname(dir)) {
    const s = fs.lstatSync(dir);
    assert.ok(s.isDirectory() && !s.isSymbolicLink(), `unsafe directory: ${dir}`);
    assert.equal(s.uid, owner); assert.equal(s.mode & 0o022, 0);
  }
}
function syncDirectory(path: string) {
  const fd = fs.openSync(path, fs.constants.O_RDONLY | fs.constants.O_DIRECTORY);
  try { fs.fsyncSync(fd); } finally { fs.closeSync(fd); }
}
export function exclusive(path: string, body: string) {
  const fd = fs.openSync(path, fs.constants.O_CREAT | fs.constants.O_EXCL | fs.constants.O_WRONLY | fs.constants.O_NOFOLLOW, 0o400);
  try { fs.writeFileSync(fd, body); fs.fsyncSync(fd); } finally { fs.closeSync(fd); }
  syncDirectory(dirname(path));
}
type RecordValue = { kind: 'genesis' | 'intent' | 'failed' | 'completed' | 'resolution'; runId: string; profileHash: string; bootId: string; detail: unknown };
type JournalRow = RecordValue & { sequence: number; previous: string; digest: string };
function assertResolution(detail: unknown, failedRecord: JournalRow) {
  const value = detail as Record<string, unknown>;
  keys(value, ['failedRecordDigest', 'replayAllowed', 'cleanupVerified', 'resources']);
  assert.equal(value.failedRecordDigest, failedRecord.digest, 'resolution failure identity mismatch');
  assert.equal(value.replayAllowed, false); assert.equal(value.cleanupVerified, true);
  assert.ok(Array.isArray(value.resources) && value.resources.length > 0, 'resolution needs cleanup observations');
  for (const resource of value.resources) {
    assert.equal(resource.populated, 0); assert.deepEqual(resource.processes, []);
    assert.ok(['inactive', 'failed', 'not-found'].includes(resource.active), 'resolution resource remains active');
  }
}
// Append only, bounded journal. A torn append is corrupt, never permission to retry.
export class Journal {
  directory: string;
  owner: number;
  constructor(directory: string, owner = 0) { this.directory = directory; this.owner = owner; }
  read(): JournalRow[] {
    const dir = fs.lstatSync(this.directory);
    assert.ok(dir.isDirectory() && !dir.isSymbolicLink()); assert.equal(dir.uid, this.owner); assert.equal(dir.mode & 0o077, 0);
    const names = fs.readdirSync(this.directory).sort();
    assert.deepEqual(names.filter(n => n !== 'append.lock'), ['journal.jsonl'], 'journal missing/incomplete');
    const path = join(this.directory, 'journal.jsonl'), s = regular(path, this.owner);
    assert.equal(s.mode & 0o077, 0); assert.ok(s.size > 0 && s.size <= CONTROLS.journalBytes, 'journal cap');
    const data = fs.readFileSync(path, 'utf8'); assert.ok(data.endsWith('\n'), 'torn journal');
    let previous = '0'.repeat(64);
    const rows = data.slice(0, -1).split('\n').map((line, sequence) => {
      const row = JSON.parse(line) as JournalRow;
      keys(row, ['kind', 'runId', 'profileHash', 'bootId', 'detail', 'sequence', 'previous', 'digest']);
      const { digest: recorded, ...value } = row;
      assert.equal(recorded, hash(json(value))); assert.equal(row.sequence, sequence); assert.equal(row.previous, previous);
      digest(row.profileHash); assert.match(row.bootId, /^[a-f0-9-]{36}$/);
      assert.ok(['genesis', 'intent', 'failed', 'completed', 'resolution'].includes(row.kind));
      if (sequence === 0) assert.equal(row.kind, 'genesis'); else assert.notEqual(row.kind, 'genesis');
      if (row.kind !== 'genesis') assert.match(row.runId, /^[a-f0-9]{24}$/);
      previous = recorded; return row;
    });
    // Validate transitions too: well-hashed fabricated/incomplete resolution is still denied.
    const runs = new Map<string, string>();
    const failures = new Map<string, JournalRow>();
    for (const row of rows.slice(1)) {
      const state = runs.get(row.runId);
      if (row.kind === 'intent') {
        assert.equal(state, undefined);
        assert.ok([...runs.values()].every(s => s === 'completed' || s === 'resolved-failure'), 'overlapping intent/quarantine');
        runs.set(row.runId, 'intent');
      }
      else if (row.kind === 'failed') { assert.equal(state, 'intent'); runs.set(row.runId, 'failed'); failures.set(row.runId, row); }
      else if (row.kind === 'completed') { assert.equal(state, 'intent'); runs.set(row.runId, 'completed'); }
      else { assert.equal(state, 'failed'); assertResolution(row.detail, failures.get(row.runId)!); runs.set(row.runId, 'resolved-failure'); }
      assert.equal(row.profileHash, rows[0].profileHash, 'journal profile mismatch');
    }
    return rows;
  }
  assertLaunch(profileHash: string, runId: string) {
    assert.ok(!fs.existsSync(join(this.directory, 'append.lock')), 'interrupted journal append; reconciliation required');
    const rows = this.read();
    assert.equal(rows[0].profileHash, profileHash);
    assert.ok(!rows.some(r => r.runId === runId), 'run identity cannot be replayed');
    const states = new Map<string, string>();
    for (const r of rows.slice(1)) states.set(r.runId, r.kind);
    assert.ok([...states.values()].every(s => s === 'completed' || s === 'resolution'), 'unresolved intent/quarantine; launch denied');
    // Reserve intent + failure + reconciliation, not merely the next write.
    assert.ok(fs.statSync(join(this.directory, 'journal.jsonl')).size + 3 * 16384 <= CONTROLS.journalBytes, 'journal reserve exhausted');
  }
  append(value: RecordValue, initialize = false) {
    if (initialize) { assert.equal(value.kind, 'genesis'); assert.equal(fs.readdirSync(this.directory).length, 0); }
    const lock = join(this.directory, 'append.lock');
    exclusive(lock, json({ pid: process.pid, kind: value.kind, runId: value.runId }));
    // A crash or write error retains lock/intent. Never auto-repair torn state.
    const owned = fs.lstatSync(lock);
    const rows = initialize ? [] : this.read();
    if (value.kind === 'intent') {
      assert.ok(!rows.some(r => r.runId === value.runId), 'run identity cannot be replayed');
      const states = new Map<string, string>(); for (const r of rows.slice(1)) states.set(r.runId, r.kind);
      assert.ok([...states.values()].every(s => s === 'completed' || s === 'resolution'), 'unresolved intent/quarantine');
    } else if (!initialize) {
      const prior = rows.filter(r => r.runId === value.runId).at(-1);
      assert.equal(prior?.kind, value.kind === 'resolution' ? 'failed' : 'intent', 'invalid journal transition');
      if (value.kind === 'resolution') {
        assertResolution(value.detail, prior!);
      }
    }
    if (!initialize) assert.equal(value.profileHash, rows[0].profileHash);
    const entry = { ...value, sequence: rows.length, previous: rows.at(-1)?.digest ?? '0'.repeat(64) };
    const body = json({ ...entry, digest: hash(json(entry)) }) + '\n';
    assert.ok(Buffer.byteLength(body) <= 16384);
    const path = join(this.directory, 'journal.jsonl');
    assert.ok((initialize ? 0 : fs.statSync(path).size) + Buffer.byteLength(body) <= CONTROLS.journalBytes);
    const fd = fs.openSync(path, fs.constants.O_WRONLY | fs.constants.O_NOFOLLOW | (initialize ? fs.constants.O_CREAT | fs.constants.O_EXCL : fs.constants.O_APPEND), 0o600);
    try { fs.writeFileSync(fd, body); fs.fsyncSync(fd); } finally { fs.closeSync(fd); }
    syncDirectory(this.directory); this.read();
    const current = fs.lstatSync(lock); assert.equal(current.dev, owned.dev); assert.equal(current.ino, owned.ino);
    fs.unlinkSync(lock); syncDirectory(this.directory);
  }
}

export function assertEffective(observed: Record<string, string>) {
  for (const [name, expected] of Object.entries({ 'cpu.max': CONTROLS.cpuMax, 'memory.max': CONTROLS.memoryMax,
    'memory.swap.max': CONTROLS.swapMax, 'pids.max': CONTROLS.pidsMax })) assert.equal(observed[name], expected, `effective ${name} mismatch; repository gate closed`);
}
export function assertOwned(expected: Record<string, unknown>, actual: Record<string, unknown>) {
  for (const key of ['unit', 'invocation', 'cgroup', 'device', 'inode']) {
    assert.ok(expected[key] !== undefined && expected[key] !== '', `missing ownership ${key}`);
    assert.equal(actual[key], expected[key], `ownership ${key} mismatch`);
  }
}

function command(binary: string, args: string[], timeout = 5000): string {
  return execFileSync(binary, args, { encoding: 'utf8', timeout, maxBuffer: 256 * 1024,
    env: { PATH: '/usr/bin:/bin', LANG: 'C', LC_ALL: 'C' }, stdio: ['ignore', 'pipe', 'pipe'] }).trim();
}
function read(path: string) { return fs.readFileSync(path, 'utf8').trim(); }
function fileHash(path: string) {
  const digest = createHash('sha256'), buffer = Buffer.alloc(65536), fd = fs.openSync(path, 'r');
  try { let count: number; while ((count = fs.readSync(fd, buffer, 0, buffer.length, null)) > 0) digest.update(buffer.subarray(0, count)); }
  finally { fs.closeSync(fd); }
  return digest.digest('hex');
}
function controllerCharge(p: Profile) {
  assert.match(read('/proc/self/cgroup'), /^0::\/gaffer89\.slice\/gaffer-native-controller\.service$/);
  const s = fs.statSync(CONTROLLER_GROUP);
  assert.equal(s.dev, p.headroom.controllerSliceDevice); assert.equal(s.ino, p.headroom.controllerSliceInode);
  assert.equal(read(`${CONTROLLER_GROUP}/memory.max`), String(p.headroom.controllerBytes));
  assert.equal(read(`${CONTROLLER_GROUP}/memory.swap.max`), '0');
  const current = Number(read(`${CONTROLLER_GROUP}/memory.current`));
  assert.ok(current <= p.headroom.controllerBytes, 'controller charged memory exceeds frozen budget');
  return { current, stat: read(`${CONTROLLER_GROUP}/memory.stat`), events: read(`${CONTROLLER_GROUP}/memory.events`) };
}
export function rootfsDigest(root: string): string {
  const rows: unknown[] = [];
  const visit = (path: string) => {
    for (const name of fs.readdirSync(path).sort()) {
      const full = join(path, name), relative = full.slice(root.length), s = fs.lstatSync(full);
      assert.ok(rows.length < 8192); assert.equal(s.uid, 0); assert.equal(s.mode & 0o6022, 0);
      assert.ok(s.isDirectory() || s.isFile() || s.isSymbolicLink(), 'device/socket in staged root');
      const mountTarget = s.isDirectory() && ['/dev/shm', '/dev/pts', '/sys/fs', '/sys/fs/cgroup'].includes(relative);
      assert.ok(mountTarget || !/^\/(root|home|run|var|work|tmp|sys|proc|dev)\/.+/.test(relative), 'host data in staged root');
      assert.ok(!/(docker|containerd|credentials|id_rsa|id_ed25519|resolv\.conf|machine-id)/i.test(relative));
      if (s.isSymbolicLink()) {
        const target = fs.readlinkSync(full);
        assert.ok(!target.includes('..') && !target.startsWith('/'), 'rootfs symlinks must be relative and confined');
        rows.push([relative, s.mode, target]);
      } else if (s.isDirectory()) { rows.push([relative, s.mode]); visit(full); }
      else { assert.equal(s.nlink, 1); rows.push([relative, s.mode, fileHash(full)]); }
    }
  };
  visit(root); return hash(json(rows));
}
function verifyHost(p: Profile) {
  assert.equal(process.platform, 'linux'); assert.equal(process.getuid!(), 0, 'explicit trusted root controller required');
  const controllerBefore = controllerCharge(p);
  assert.equal(arch(), 'x64'); assert.equal(release(), p.identity.kernelRelease);
  assert.equal(process.version, p.identity.nodeVersion); assert.equal(process.execPath, '/usr/bin/node');
  assert.equal(hash(fs.readFileSync('/etc/os-release')), p.identity.osReleaseSha256);
  assert.match(read('/etc/os-release'), /^ID=debian$/m); assert.match(read('/etc/os-release'), /^VERSION_ID="13"$/m);
  assert.equal(hash(fs.readFileSync(`/boot/config-${release()}`)), p.identity.kernelConfigSha256);
  assert.equal(read('/proc/sys/kernel/random/boot_id'), p.identity.bootId);
  assert.equal(hash(fs.readFileSync('/etc/machine-id')), p.identity.machineIdSha256);
  for (const [name, expected] of Object.entries(p.identity.controllerNamespaces)) assert.equal(fs.readlinkSync(`/proc/self/ns/${name}`), expected);
  trustedDirectory(BASE); trustedDirectory(`${BASE}/rootfs`); trustedDirectory(`${BASE}/evidence`);
  for (const a of p.artifacts) { regular(a.path); assert.equal(fileHash(a.path), a.sha256, `artifact drift: ${a.path}`); }
  assert.equal(hash(fs.readFileSync(fileURLToPath(import.meta.url))), p.artifacts.find(a => a.path === `${BASE}/controller/run.ts`)!.sha256);
  for (const kind of ['image', 'hypervisor'] as const) {
    const path = `${BASE}/${kind}-provenance.json`; assert.ok(fs.statSync(path).size <= 16384);
    const provenance = JSON.parse(read(path));
    keys(provenance, ['schema', 'kind', 'artifactSha256', 'version', 'observedAt', 'dockerFree', 'disposable']);
    assert.equal(provenance.schema, 1); assert.equal(provenance.kind, kind); digest(provenance.artifactSha256);
    assert.equal(provenance.artifactSha256, p.identity[`${kind}Sha256`]); exactText(provenance.version);
    assert.equal(new Date(provenance.observedAt).toISOString(), provenance.observedAt); assert.ok(Date.parse(provenance.observedAt) <= Date.now());
    assert.equal(provenance.dockerFree, true); assert.equal(provenance.disposable, true);
  }
  assert.equal(command('/usr/bin/systemctl', ['--version']), p.identity.systemdVersion);
  assert.equal(rootfsDigest(`${BASE}/rootfs`), p.identity.rootfsSha256);
  for (const path of p.artifacts.map(a => a.path).filter(path => !path.startsWith(BASE) && !/system/.test(path))) {
    assert.equal(fileHash(`${BASE}/rootfs${path}`), p.artifacts.find(a => a.path === path)!.sha256, `staged tool differs: ${path}`);
  }
  for (const [prefix, path] of [['evidence', `${BASE}/evidence`], ['rootfs', `${BASE}/rootfs`]] as const) {
    const s = fs.statSync(path); assert.equal(s.dev, p.storage[`${prefix}Device`]); assert.equal(s.ino, p.storage[`${prefix}Inode`]);
  }
  const storage = fs.statfsSync(`${BASE}/evidence`);
  assert.equal(storage.type, p.storage.evidenceFsType);
  assert.ok(storage.bavail * storage.bsize >= p.headroom.diskReserveBytes + CONTROLS.evidenceBytes);
  assert.ok(storage.ffree >= p.headroom.inodeReserve + CONTROLS.evidenceFiles);
  const available = Number(read('/proc/meminfo').match(/^MemAvailable:\s+(\d+) kB$/m)?.[1]) * 1024;
  assert.ok(available >= 2 * Number(CONTROLS.memoryMax) + p.headroom.controllerBytes + p.headroom.osBytes, 'host headroom');
  assert.equal(read('/proc/1/comm'), 'systemd');
  assert.equal(fs.statfsSync('/sys/fs/cgroup').type, 0x63677270);
  for (const name of ['cpu', 'memory', 'pids']) assert.ok(read('/sys/fs/cgroup/cgroup.controllers').split(' ').includes(name));
  assert.ok(fs.existsSync('/sys/fs/cgroup/cgroup.kill'));
  // Disposable host only: do not stop Docker, inspect private configuration, or repair it.
  for (const path of ['/usr/bin/docker', '/usr/local/bin/docker', '/usr/bin/dockerd', '/usr/bin/containerd', '/run/docker.sock', '/var/run/docker.sock', '/run/containerd/containerd.sock', '/opt/docker-desktop', '/usr/lib/docker', '/var/lib/docker']) assert.ok(!fs.existsSync(path), `Docker-free prerequisite failed: ${path}`);
  assert.ok(!/docker|containerd/i.test(command('/usr/bin/systemctl', ['list-units', '--all', '--no-pager', '--no-legend'])));
  for (const pid of fs.readdirSync('/proc').filter(n => /^\d+$/.test(n))) {
    try {
      const status = read(`/proc/${pid}/status`), uid = Number(status.match(/^Uid:\s+(\d+)/m)?.[1]);
      assert.ok(!/^Name:\s+.*(docker|containerd)/im.test(status), 'Docker process present');
      assert.ok(![p.ids.workerUid, p.ids.mockUid].includes(uid), 'worker/mock identity already in use');
    } catch (e: any) { if (e.code !== 'ENOENT' && e.code !== 'ESRCH') throw e; }
  }
  return { controllerBefore, controllerAfter: controllerCharge(p), availableMemory: available, storage };
}

export function unitProperties(p: Profile, role: Role, runId: string, testCase: Case, socketSource?: string): string[] {
  assert.match(runId, /^[a-f0-9]{24}$/); assert.ok(CASES.includes(testCase));
  const uid = role === 'worker' ? p.ids.workerUid : p.ids.mockUid, gid = role === 'worker' ? p.ids.workerGid : p.ids.mockGid;
  const tmpfs = (path: string, size: number, inodes: number, exec = false) => `${path}:rw,nosuid,nodev,noswap,${exec ? 'exec' : 'noexec'},size=${size},nr_inodes=${inodes},mode=0700,uid=${uid},gid=${gid}`;
  const properties = [
    `Description=gaffer-native-${runId}-${role}`, 'Slice=system.slice', `RootDirectory=${BASE}/rootfs`,
    `User=${uid}`, `Group=${gid}`, `SupplementaryGroups=${p.ids.socketGid}`, 'PrivateUsers=identity',
    'PrivatePIDs=yes', 'PrivateIPC=yes', 'PrivateNetwork=yes', 'PrivateDevices=yes', 'ProtectControlGroups=strict',
    'ProtectSystem=strict', 'ProtectHome=yes', 'NoNewPrivileges=yes', 'CapabilityBoundingSet=', 'AmbientCapabilities=',
    'RestrictNamespaces=yes', 'RestrictAddressFamilies=AF_UNIX', 'SystemCallArchitectures=native',
    'SystemCallFilter=~@privileged @mount @debug @obsolete @reboot @swap @raw-io @module io_uring_setup io_uring_enter io_uring_register',
    'SystemCallErrorNumber=EPERM', 'LockPersonality=yes', 'RestrictRealtime=yes', 'RestrictSUIDSGID=yes',
    'ProtectKernelTunables=yes', 'ProtectKernelModules=yes', 'ProtectKernelLogs=yes', 'ProtectClock=yes',
    'ReadOnlyPaths=/dev', 'InaccessiblePaths=-/dev/pts -/dev/ptmx -/dev/mqueue',
    'KeyringMode=private', 'UMask=0077', 'RemoveIPC=yes', 'Delegate=no', 'CPUAccounting=yes',
    'MemoryAccounting=yes', 'TasksAccounting=yes', 'CPUQuota=50%', 'CPUQuotaPeriodSec=100ms',
    'MemoryMax=134217728', 'MemorySwapMax=0', `TasksMax=${testCase === 'missing-pids-limit' && role === 'worker' ? 'infinity' : '64'}`,
    'LimitNOFILE=256', 'LimitCORE=0', 'OOMPolicy=kill', 'MemoryOOMGroup=yes',
    'Restart=no', 'NotifyAccess=none', 'RuntimeMaxSec=60s', 'RuntimeRandomizedExtraSec=0',
    'KillMode=control-group', 'TimeoutStopSec=1s', 'SendSIGKILL=yes', 'FinalKillSignal=SIGKILL',
    'StandardInput=null', 'StandardOutput=null', 'StandardError=null',
    'Environment=PATH=/usr/bin:/bin LANG=C LC_ALL=C HOME=/nonexistent GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null',
    'UnsetEnvironment=USER LOGNAME SHELL MAIL XDG_RUNTIME_DIR JOURNAL_STREAM NOTIFY_SOCKET WATCHDOG_PID WATCHDOG_USEC MEMORY_PRESSURE_WATCH MEMORY_PRESSURE_WRITE NODE_OPTIONS NODE_PATH LD_PRELOAD LD_LIBRARY_PATH LD_AUDIT GLIBC_TUNABLES GCONV_PATH LOCPATH OPENSSL_CONF OPENSSL_MODULES',
    `BindReadOnlyPaths=${BASE}/evidence/${runId}/${role}.json:/fixture/config.json${socketSource ? ` ${socketSource}:/router` : ''}`,
  ];
  if (role === 'worker') {
    properties.push(`TemporaryFileSystem=${[
      tmpfs('/log', 1048576, 64), tmpfs('/work', 33554432, 4096, true), tmpfs('/tmp', 8388608, 1024), tmpfs('/dev/shm', 4194304, 1024),
    ].join(' ')}`);
    properties.push('ReadWritePaths=/dev/shm');
    if (testCase === 'parent-exit') properties.push('RemainAfterExit=yes');
    if (socketSource) assert.match(socketSource, /^\/proc\/[1-9][0-9]*\/root\/router$/);
  } else {
    assert.equal(socketSource, undefined);
    properties.push(`TemporaryFileSystem=${tmpfs('/log', 1048576, 64)} /router:rw,nosuid,nodev,noswap,noexec,size=1048576,nr_inodes=64,mode=0750,uid=${uid},gid=${p.ids.socketGid}`);
  }
  return properties;
}

type Owned = { role: Role; unit: string; invocation: string; cgroup: string; device: number; inode: number; pid: number; started: number; logFd?: number; gateFd?: number; memoryEventsFd?: number };
function unitState(unit: string) {
  assert.match(unit, /^gaffer-native-[a-f0-9]{24}-(worker|mock)\.service$/);
  return Object.fromEntries(command('/usr/bin/systemctl', ['show', unit, '--no-pager', '--property=Id,Description,InvocationID,ControlGroup,MainPID,ActiveState,SubState,LoadState,Result,ExecMainCode,ExecMainStatus,KillMode,RuntimeMaxUSec,TimeoutStopUSec,Restart,NotifyAccess,SendSIGKILL']).split('\n').map(line => {
    const at = line.indexOf('='); return [line.slice(0, at), line.slice(at + 1)];
  }));
}
function groupPath(group: string) {
  assert.match(group, /^\/system\.slice\/gaffer-native-[a-f0-9]{24}-(worker|mock)\.service$/);
  return `/sys/fs/cgroup${group}`;
}
function counters(cgroup: string) {
  const path = groupPath(cgroup);
  return Object.fromEntries(['cpu.max', 'cpu.stat', 'memory.max', 'memory.current', 'memory.swap.max', 'memory.swap.current', 'memory.stat', 'memory.events', 'pids.max', 'pids.current', 'pids.events', 'cgroup.events', 'cgroup.procs'].map(name => [name, read(`${path}/${name}`)]));
}
function fdText(fd: number, maximum = 1048576) {
  const buf = Buffer.alloc(maximum); const count = fs.readSync(fd, buf, 0, maximum, 0); return buf.subarray(0, count).toString('utf8').trim();
}
function rawCounters() {
  const lines = read('/proc/net/snmp').split('\n'), index = lines.findIndex(line => line.startsWith('Icmp:'));
  assert.ok(index >= 0);
  const names = lines[index].split(/\s+/), values = lines[index + 1].split(/\s+/);
  const v4 = Number(values[names.indexOf('OutEchos')]);
  const v6 = Number(read('/proc/net/snmp6').match(/^Icmp6OutEchos\s+(\d+)$/m)?.[1]);
  assert.ok(Number.isFinite(v4) && Number.isFinite(v6)); return { v4, v6 };
}
function verifyUnit(owned: Owned) {
  const state = unitState(owned.unit), path = groupPath(state.ControlGroup), s = fs.statSync(path);
  assertOwned(owned, { unit: state.Id, invocation: state.InvocationID, cgroup: state.ControlGroup, device: s.dev, inode: s.ino });
  assert.equal(state.Description, owned.unit.slice(0, -8));
  assert.equal(state.KillMode, 'control-group'); assert.equal(state.RuntimeMaxUSec, '1min'); assert.equal(state.TimeoutStopUSec, '1s');
  assert.equal(state.Restart, 'no'); assert.equal(state.NotifyAccess, 'none'); assert.equal(state.SendSIGKILL, 'yes');
  return state;
}
function processInventory(cgroup: string): number[] {
  const found: number[] = [];
  for (const name of fs.readdirSync('/proc').filter(n => /^[1-9][0-9]*$/.test(n))) {
    try {
      const lines = read(`/proc/${name}/cgroup`).split('\n');
      if (lines.some(line => line === `0::${cgroup}` || line.startsWith(`0::${cgroup}/`))) found.push(Number(name));
    } catch (e: any) { if (e.code !== 'ENOENT' && e.code !== 'ESRCH') throw e; }
  }
  return found;
}
async function clean(owned: Owned): Promise<unknown> {
  const initial = unitState(owned.unit), path = groupPath(owned.cgroup);
  if (!fs.existsSync(path) && (['inactive', 'failed'].includes(initial.ActiveState) || initial.SubState === 'exited')) {
    assert.ok(initial.InvocationID === owned.invocation || (initial.LoadState === 'not-found' && !initial.InvocationID));
    assert.deepEqual(processInventory(owned.cgroup), []);
    const started = Date.now(); let state = initial;
    if (state.SubState === 'exited') {
      command('/usr/bin/systemctl', ['stop', '--no-block', owned.unit]);
      while (Date.now() - started < 5000) {
        state = unitState(owned.unit);
        if (['inactive', 'failed'].includes(state.ActiveState)) break;
        assert.equal(state.InvocationID, owned.invocation); await delay(50);
      }
    }
    assert.ok(['inactive', 'failed'].includes(state.ActiveState));
    assert.ok(state.InvocationID === owned.invocation || (state.LoadState === 'not-found' && !state.InvocationID));
    assert.ok(!fs.existsSync(path)); assert.deepEqual(processInventory(owned.cgroup), []);
    return { unit: owned.unit, invocation: owned.invocation, cgroup: owned.cgroup, populated: 0, processes: [], active: state.ActiveState, elapsedMs: Date.now() - started, killUsed: false, groupAbsent: true };
  }
  verifyUnit(owned); // No stop/kill on a reused name or ambiguous invocation.
  const started = Date.now();
  command('/usr/bin/systemctl', ['stop', '--no-block', owned.unit]);
  let killUsed = false;
  while (Date.now() - started <= CONTROLS.emptySeconds * 1000) {
    const state = unitState(owned.unit), path = groupPath(owned.cgroup);
    const exists = fs.existsSync(path);
    if (exists) {
      const s = fs.statSync(path); assert.equal(s.dev, owned.device); assert.equal(s.ino, owned.inode);
      assert.equal(state.InvocationID, owned.invocation);
    }
    const processes = processInventory(owned.cgroup);
    assert.ok(state.InvocationID === owned.invocation || (!exists && state.LoadState === 'not-found' && !state.InvocationID), 'unit identity changed during stop');
    if (['inactive', 'failed'].includes(state.ActiveState) && !processes.length && (!exists || /^populated 0$/m.test(read(`${path}/cgroup.events`)))) {
      return { unit: owned.unit, invocation: owned.invocation, cgroup: owned.cgroup, populated: 0, processes, active: state.ActiveState, elapsedMs: Date.now() - started, killUsed };
    }
    if (exists && Date.now() - started > 1500 && !killUsed) {
      assert.equal(state.InvocationID, owned.invocation); fs.writeFileSync(`${path}/cgroup.kill`, '1'); killUsed = true;
    }
    await delay(50);
  }
  throw new Error(`complete-tree observation missing for ${owned.unit}; retain quarantine`);
}

// Synthetic stand-ins bind only loopback. No public/router/provider address is accepted.
async function listeners(runId: string) {
  let count = 0;
  const servers: ReturnType<typeof tcpServer>[] = [], sockets: ReturnType<typeof createSocket>[] = [];
  const endpoints: { protocol: string; host: string; port: number; label: string }[] = [];
  try {
    for (const host of ['127.0.0.1', '::1']) {
      const server = tcpServer(socket => { count++; socket.end(runId); }); servers.push(server);
      await new Promise<void>((done, fail) => { server.once('error', fail); server.listen(0, host, done); });
      endpoints.push({ protocol: 'tcp', host, port: (server.address() as any).port, label: 'public-provider-metadata-management-direct-inference-stand-in' });
      for (const protocol of ['udp', 'dns']) {
        const socket = createSocket(host === '::1' ? 'udp6' : 'udp4'); sockets.push(socket);
        socket.on('message', (msg, peer) => {
          count++;
          if (protocol === 'udp') socket.send(msg, peer.port, peer.address);
          else {
            // One fixed A answer; no forwarding, resolver configuration, or real DNS access.
            const question = Buffer.from('076669787475726507696e76616c69640000010001', 'hex');
            if (msg.length !== 12 + question.length || !msg.subarray(12).equals(question)) return;
            const header = Buffer.from(msg.subarray(0, 12)); header.writeUInt16BE(0x8180, 2); header.writeUInt16BE(1, 6);
            socket.send(Buffer.concat([header, question, Buffer.from('c00c000100010000000000047f000001', 'hex')]), peer.port, peer.address);
          }
        });
        await new Promise<void>((done, fail) => { socket.once('error', fail); socket.bind(0, host, done); });
        endpoints.push({ protocol, host, port: socket.address().port, label: `${protocol}-stand-in` });
      }
    }
  } catch (e) { for (const s of servers) s.close(); for (const s of sockets) s.close(); throw e; }
  return { endpoints, count: () => count, close: () => { for (const s of servers) s.close(); for (const s of sockets) s.close(); } };
}

export function prepare(p: Profile, profileHash: string, runId: string, testCase: Case) {
  validateProfile(p); digest(profileHash); assert.match(runId, /^[a-f0-9]{24}$/); assert.ok(CASES.includes(testCase));
  return { schema: 1, result: 'NOT RUN', unattendedSupported: false, profileHash, runId, testCase,
    prerequisiteTransaction: [
      'Operator measures and freezes disposable Debian 13 amd64 VM image, hypervisor, boot, kernel config, toolchain and namespace identities.',
      'Stage root-owned read-only minimal rootfs and controller at /var/lib/gaffer-native; no credentials, network installs or shared Git administration.',
      'Create distinct nonzero worker/mock users and dedicated socket group, no unrelated supplementary memberships.',
      'Create root-owned mode0700 disk-backed evidence directory, then freeze its device/inode/fs type and capacity/headroom.',
      'Prepare gaffer89.slice with MemoryMax equal to frozen controllerBytes and MemorySwapMax=0; freeze its device/inode. Controller service runs inside this slice, outside both job cgroups. Record memory.stat including staging/file-cache charges.',
      'Initialize journal explicitly once; never recreate a lost journal for an existing profile. Root reviews inventory before any setup.',
    ],
    initializeArgv: ['/usr/bin/node', `${BASE}/controller/run.ts`, '--initialize-journal', '--profile', '<operator-frozen-profile>', '--profile-sha256', profileHash],
    executeArgv: ['/usr/bin/node', `${BASE}/controller/run.ts`, '--execute', '--profile', '<operator-frozen-profile>', '--profile-sha256', profileHash, '--run-id', runId, '--case', testCase],
    controllerServiceArgv: ['/usr/bin/systemd-run', '--unit=gaffer-native-controller', '--slice=gaffer89.slice', '--service-type=exec', '--property=Restart=no',
      '/usr/bin/node', `${BASE}/controller/run.ts`, '--execute', '--profile', '<operator-frozen-profile>', '--profile-sha256', profileHash, '--run-id', runId, '--case', testCase],
    services: (['mock', 'worker'] as const).map(role => ({ role, properties: unitProperties(p, role, runId, testCase),
      argv: bootstrapArgv(), socketBind: role === 'worker' ? 'Exact positively identified mock MainPID /proc/<pid>/root/router; resolved only after mock gate' : null })),
    cleanup: 'Stop only matching invocation/cgroup device/inode; observe populated0, no owned processes, inactive units. Retain evidence and unresolved resources on uncertainty. No broad removal.',
  };
}
function bootstrapArgv() {
  return ['/usr/bin/env', '-i', 'PATH=/usr/bin:/bin', 'LANG=C', 'LC_ALL=C', 'HOME=/nonexistent',
    'GIT_CONFIG_NOSYSTEM=1', 'GIT_CONFIG_GLOBAL=/dev/null', '/usr/bin/node', '/fixture/probe.ts', '--bootstrap'];
}

async function execute(p: Profile, profileHash: string, runId: string, testCase: Case) {
  const host = verifyHost(p);
  const journal = new Journal(`${BASE}/evidence/journal`);
  journal.assertLaunch(profileHash, runId);
  if (testCase === 'runtime-drift') {
    const drifted = structuredClone(p); drifted.identity.kernelRelease += '-copied-expectation-drift';
    assert.throws(() => verifyHost(drifted));
    return { result: 'expected-rejection', testCase, profileHash, unattendedSupported: false, repositoryStarted: false };
  }
  const runDir = `${BASE}/evidence/${runId}`;
  assert.ok(!fs.existsSync(runDir));
  // Intent comes before directories, units, listeners, mounts or fixture allocation.
  journal.append({ kind: 'intent', runId, profileHash, bootId: p.identity.bootId,
    detail: { testCase, units: ['worker', 'mock'].map(role => `gaffer-native-${runId}-${role}.service`), runDir, rootfs: p.identity.rootfsSha256, controls: CONTROLS } });
  fs.mkdirSync(runDir, { mode: 0o700 }); syncDirectory(dirname(runDir));
  const owned: Owned[] = [], attemptedUnits: string[] = [], commands: unknown[] = [], observations: unknown[] = [];
  const cleanup: unknown[] = []; const token = randomBytes(32).toString('hex');
  const rawPath = `${runDir}/observations.jsonl`;
  const rawFd = fs.openSync(rawPath, fs.constants.O_CREAT | fs.constants.O_EXCL | fs.constants.O_WRONLY | fs.constants.O_NOFOLLOW, 0o600);
  let rawBytes = 0;
  syncDirectory(runDir);
  function budget(additional = 0, extraFiles = 0) {
    const files = fs.readdirSync(runDir);
    assert.ok(files.length + extraFiles <= CONTROLS.evidenceFiles - CONTROLS.journalFiles);
    const bytes = files.reduce((total, file) => total + regular(join(runDir, file)).size, 0);
    assert.ok(bytes + additional <= CONTROLS.evidenceBytes - CONTROLS.journalBytes, 'run evidence cap');
    assert.equal(fs.statfsSync(runDir).type, p.storage.evidenceFsType, 'evidence filesystem changed');
  }
  function record(value: unknown) {
    const line = json(value).replaceAll(token, '<synthetic-token>') + '\n';
    assert.ok(rawBytes + Buffer.byteLength(line) <= 10 * 1024 ** 2, 'raw observation budget');
    budget(Buffer.byteLength(line) + 512 * 1024, 1); // Reserved terminal receipt space.
    controllerCharge(p);
    fs.writeFileSync(rawFd, line); fs.fsyncSync(rawFd); rawBytes += Buffer.byteLength(line); observations.push(value);
  }
  let stop = false, failure: unknown, expectedRejection = false, observer: Awaited<ReturnType<typeof listeners>> | undefined;
  let externalRaw: ReturnType<typeof rawCounters> | undefined;
  const interrupt = () => { stop = true; };
  process.on('SIGINT', interrupt); process.on('SIGTERM', interrupt);
  const guard = () => { assert.ok(!stop, 'controller interrupted; repository gate closed'); };
  function snapshot(o: Owned) {
    const state = verifyUnit(o), values = counters(o.cgroup);
    record({ at: Date.now(), role: o.role, state, counters: values, processes: processInventory(o.cgroup) });
    return values;
  }
  function log(o: Owned) {
    if (o.logFd === undefined) return '';
    const size = fs.fstatSync(o.logFd).size; assert.ok(size <= 1048576);
    const buf = Buffer.alloc(size); fs.readSync(o.logFd, buf, 0, size, 0); return buf.toString('utf8');
  }
  function report(o: Owned) { return o.gateFd === undefined ? null : JSON.parse(fdText(o.gateFd, 16384)); }
  function scopeSocket(mock: Owned) {
    verifyUnit(mock);
    const dir = `/proc/${mock.pid}/root/router`, ds = fs.statSync(dir);
    assert.equal(ds.uid, p.ids.mockUid); assert.equal(ds.gid, p.ids.socketGid); assert.equal(ds.mode & 0o777, 0o750);
    const fd = fs.openSync(dir, fs.constants.O_RDONLY | fs.constants.O_DIRECTORY | fs.constants.O_NOFOLLOW);
    try {
      const stable = `/proc/self/fd/${fd}/inference.sock`, s = fs.lstatSync(stable);
      assert.ok(s.isSocket()); assert.equal(s.uid, p.ids.mockUid); assert.equal(s.gid, p.ids.mockGid); assert.equal(s.nlink, 1);
      fs.chownSync(stable, p.ids.mockUid, p.ids.socketGid);
      const after = fs.lstatSync(stable);
      assert.equal(after.dev, s.dev); assert.equal(after.ino, s.ino); assert.equal(after.uid, p.ids.mockUid); assert.equal(after.gid, p.ids.socketGid);
      record({ scopedSocket: { invocation: mock.invocation, device: after.dev, inode: after.ino, uid: after.uid, gid: after.gid, mode: after.mode } });
    } finally { fs.closeSync(fd); }
  }
  async function launch(role: Role, socketSource?: string) {
    guard();
    const unit = `gaffer-native-${runId}-${role}.service`;
    const config = { profile: p, profileHash, runId, role, testCase, token, endpoints: observer?.endpoints ?? [] };
    exclusive(`${runDir}/${role}.json`, json(config));
    // Config contains only synthetic token/public controls. Service must read it through read-only bind.
    fs.chmodSync(`${runDir}/${role}.json`, 0o444);
    const args = ['--unit', unit, '--service-type=exec', '--no-block', ...unitProperties(p, role, runId, testCase, socketSource).flatMap(prop => ['--property', prop]), ...bootstrapArgv()];
    commands.push(['/usr/bin/systemd-run', ...args]);
    record({ launchArgv: ['/usr/bin/systemd-run', ...args] }); attemptedUnits.push(unit);
    // Deterministic intent already records unit name if command completion becomes ambiguous.
    const activationBound = Date.now(); command('/usr/bin/systemd-run', args); guard();
    let state = unitState(unit);
    for (let n = 0; n < 50 && Number(state.MainPID) === 0; n++) { await delay(50); guard(); state = unitState(unit); }
    assert.equal(state.Description, unit.slice(0, -8)); assert.match(state.InvocationID, /^[a-f0-9]{32}$/);
    const path = groupPath(state.ControlGroup), s = fs.statSync(path), pid = Number(state.MainPID);
    assert.ok(Number.isInteger(pid) && pid > 1);
    const o: Owned = { role, unit, invocation: state.InvocationID, cgroup: state.ControlGroup, device: s.dev, inode: s.ino, pid, started: activationBound };
    owned.push(o); exclusive(`${runDir}/${role}-owned.json`, json(o));
    o.memoryEventsFd = fs.openSync(`${path}/memory.events`, fs.constants.O_RDONLY);
    verifyUnit(o);
    const before = snapshot(o);
    try { assertEffective(before); }
    catch (error) {
      if (testCase !== 'missing-pids-limit' || role !== 'worker' || before['pids.max'] !== 'max') throw error;
      const root = `/proc/${pid}/root`;
      for (let n = 0; n < 100 && !fs.existsSync(`${root}/log/gate.json`); n++) await delay(25);
      const denied = JSON.parse(read(`${root}/log/gate.json`));
      assert.equal(denied.result, 'denied'); assert.equal(denied.repositoryStarted, false);
      assert.match(denied.error, /pids.max/);
      o.logFd = fs.openSync(`${root}/log/events.jsonl`, fs.constants.O_RDONLY | fs.constants.O_NOFOLLOW);
      assert.ok(!log(o).includes('repository-start'));
      record({ missingPids: { effective: 'max', denied, repositoryStarted: false } });
      expectedRejection = true; throw new Error('expected missing-pids-limit admission rejection');
    }
    const root = `/proc/${pid}/root`;
    for (let n = 0; n < 100 && !fs.existsSync(`${root}/log/gate.json`); n++) { await delay(25); guard(); verifyUnit(o); }
    const gate = JSON.parse(read(`${root}/log/gate.json`));
    assert.equal(gate.runId, runId); assert.equal(gate.profileHash, profileHash); assert.equal(gate.role, role);
    record({ role, gate }); assert.equal(gate.result, 'admitted', json(gate));
    assert.equal(gate.pid, 1); assert.ok(gate.memoryCurrent <= CONTROLS.allowanceBytes, 'non-payload allowance exceeded');
    // Verify namespace IDs externally as well, before untrusted PID1 entrypoint.
    for (const [name, parent] of Object.entries(p.identity.controllerNamespaces)) {
      const actual = fs.readlinkSync(`/proc/${pid}/ns/${name}`); assert.equal(actual, gate.namespaces[name]); assert.notEqual(actual, parent);
    }
    o.logFd = fs.openSync(`${root}/log/events.jsonl`, fs.constants.O_RDONLY | fs.constants.O_NOFOLLOW);
    o.gateFd = fs.openSync(`${root}/log/gate.json`, fs.constants.O_RDONLY | fs.constants.O_NOFOLLOW);
    if (role === 'worker') {
      const mock = owned.find(o => o.role === 'mock')!;
      for (let n = 0; n < 50 && !log(mock).includes('mock-fixture-ready'); n++) { await delay(20); guard(); }
      assert.ok(log(mock).includes('mock-fixture-ready'), 'separate gate mock budget did not retire');
      scopeSocket(mock);
    }
    if (testCase === 'launch-interrupt-before-gate' && role === 'worker') throw new Error('injected interruption before repository gate');
    guard();
    exclusive(`${root}/log/release.json`, json({ runId, profileHash })); fs.chmodSync(`${root}/log/release.json`, 0o444);
    if (testCase === 'launch-interrupt-after-gate' && role === 'worker') throw new Error('injected interruption after repository gate');
    return o;
  }
  try {
    record({ host, at: Date.now() });
    for (const name of ['host-home', 'host-secret', 'runner-policy', 'daemon-state', 'ssh-agent', 'publication-credential', 'provider-credential']) exclusive(`${runDir}/${name}`, `SYNTHETIC:${runId}:${name}`);
    if (testCase === 'egress') observer = await listeners(runId);
    if (testCase === 'missing-pids-limit') await launch('worker');
    else {
      const mock = await launch('mock');
      for (let n = 0; n < 100 && !log(mock).includes('mock-ready'); n++) { guard(); await delay(25); snapshot(mock); }
      assert.ok(log(mock).includes('mock-ready'), 'mock readiness missing');
      scopeSocket(mock);
      // Trusted helper enters only mock mount namespace, drops every group and UID before connect.
      // Constant program, validated numeric argv; no shell or repository code in controller.
      const outsiderProgram = "const a=require('node:assert/strict');process.setgroups([]);process.setgid(Number(process.argv[1]));process.setuid(Number(process.argv[2]));const s=require('node:net').connect('/router/inference.sock');s.setTimeout(1000,()=>{s.destroy();process.exit(2)});s.on('connect',()=>{s.destroy();process.exit(3)});s.on('error',e=>{a.equal(e.code,'EACCES')});";
      const outsiderArgv = ['--target', String(mock.pid), '--mount', '--root', '--wd=/', '/usr/bin/node', '-e', outsiderProgram, String(p.ids.workerGid), String(p.ids.workerUid)];
      commands.push(['/usr/bin/nsenter', ...outsiderArgv]); command('/usr/bin/nsenter', outsiderArgv);
      record({ outsider: { result: 'EACCES', uid: p.ids.workerUid, supplementaryGroups: [], mockInvocation: mock.invocation } });
      if (observer) {
        // Same hashed probe module and endpoint argv, executed by trusted controller outside namespaces.
        const { egress } = await import(`${BASE}/rootfs/fixture/probe.ts`);
        const rawBefore = rawCounters();
        const positive = await egress(observer.endpoints, false);
        externalRaw = rawCounters(); assert.ok(externalRaw.v4 > rawBefore.v4 && externalRaw.v6 > rawBefore.v6);
        record({ positive, rawBefore, rawAfter: externalRaw, externalCount: observer.count() });
        assert.equal(observer.count(), observer.endpoints.length);
      }
      const worker = await launch('worker', `/proc/${mock.pid}/root/router`);
      if (['controller-loss', 'reboot'].includes(testCase)) {
        exclusive(`${runDir}/fault-ready.json`, json({ testCase, runId, units: owned, instruction: testCase === 'reboot' ? 'Authorized console power-cycle required; blocked intent persists. No automatic reboot command.' : 'Controller will SIGKILL itself; PID1 deadlines remain; blocked intent persists.' }));
        for (let n = 0; n < 50 && !log(worker).includes('tree-started'); n++) { await delay(50); guard(); }
        assert.ok(log(worker).includes('tree-started')); assert.ok(processInventory(worker.cgroup).length >= 3);
        if (testCase === 'controller-loss') process.kill(process.pid, 'SIGKILL');
        while (Date.now() - worker.started < 55000) { guard(); snapshot(worker); await delay(100); }
        throw new Error('console power-cycle NOT OBSERVED during fault-ready window; retain quarantine');
      }
      if (testCase === 'observation-loss') { fs.closeSync(worker.logFd!); worker.logFd = undefined; throw new Error('injected observation descriptor loss'); }
      const deadline = worker.started + (testCase === 'expiry' ? 65000 : 55000);
      let stopped = false;
      while (Date.now() < deadline) {
        guard();
        for (const o of owned) {
          const state = unitState(o.unit);
          if (['active', 'activating'].includes(state.ActiveState) && Number(state.MainPID) > 0) snapshot(o);
        }
        const text = log(worker);
        const workerReport = report(worker), mockReport = report(mock);
        if (testCase === 'stop' && text.includes('tree-started')) {
          assert.ok(processInventory(worker.cgroup).length >= 3, 'detached tree absent');
          cleanup.push(await clean(worker)); stopped = true; break;
        }
        if (testCase === 'expiry' && ['inactive', 'failed'].includes(unitState(worker.unit).ActiveState)) {
          assert.ok(Date.now() - worker.started <= 65000); assert.equal(unitState(worker.unit).Result, 'timeout');
          assert.equal(processInventory(worker.cgroup).length, 0); stopped = true; break;
        }
        const workerState = unitState(worker.unit);
        if (testCase === 'parent-exit' && workerState.SubState === 'exited' && workerState.MainPID === '0') {
          assert.equal(workerState.Result, 'success'); assert.equal(workerState.ExecMainStatus, '0');
          assert.ok(text.includes('tree-started')); assert.deepEqual(processInventory(worker.cgroup), []);
          record({ parentExit: workerState });
          stopped = true; break;
        }
        const mockPressure = /^(bytes|inodes)-(socket|mock-log)$/.test(testCase);
        if (workerReport.result === 'fixture-complete' && (!mockPressure || mockReport.result === 'fixture-complete')) break;
        if (workerReport.result === 'fixture-failed' || mockReport.result === 'fixture-failed') throw new Error('fixture failed; inspect sealed observations');
        if (testCase === 'oom' && unitState(worker.unit).Result === 'oom-kill') break;
        await delay(100);
      }
      const text = log(worker); record({ workerLog: text, mockLog: log(mock) });
      const workerReport = report(worker), mockReport = report(mock);
      record({ workerReport, mockReport });
      if (!stopped && testCase !== 'oom') assert.equal(workerReport.result, 'fixture-complete', 'completion observation missing');
      if (testCase === 'oom') {
        assert.ok(text.includes('allocation-control'));
        const samples = observations.filter((v: any) => v.role === 'worker' && v.counters) as any[];
        const finalEvents = fdText(worker.memoryEventsFd!, 4096);
        record({ oomFinalEvents: finalEvents });
        assert.ok(Number(finalEvents.match(/^oom_kill (\d+)$/m)?.[1]) > 0 && Number(finalEvents.match(/^oom (\d+)$/m)?.[1]) > 0, 'OOM counter observation missing');
        assert.ok(samples.length > 0);
        assert.ok(!text.includes('ENOSPC'));
      }
      if (/^(bytes|inodes)-/.test(testCase) || testCase === 'log-flood') {
        const isMock = /-(socket|mock-log)$/.test(testCase), target = isMock ? mock : worker;
        const pressure = isMock ? mockReport.pressure : workerReport.results.find((r: any) => r?.mode === 'bytes' || r?.mode === 'inodes');
        assert.ok(pressure && pressure.error === 'ENOSPC' && pressure.belowLimit === true);
        const observedStat = fs.statfsSync(`/proc/${target.pid}/root${pressure.path}`);
        for (const key of ['type', 'bsize', 'blocks', 'bfree', 'bavail', 'files', 'ffree'] as const) assert.equal(observedStat[key], pressure.after[key]);
        const events = fdText(target.memoryEventsFd!, 4096);
        assert.equal(Number(events.match(/^oom (\d+)$/m)?.[1]), 0); assert.equal(Number(events.match(/^oom_kill (\d+)$/m)?.[1]), 0);
        record({ pressureExternal: { stat: pressure.after, events, memory: counters(target.cgroup) } });
      }
      if (observer) { assert.equal(observer.count(), observer.endpoints.length, 'external observer saw isolated traffic'); assert.deepEqual(rawCounters(), externalRaw, 'external raw observer saw isolated traffic'); }
      for (const name of ['host-home', 'host-secret', 'runner-policy', 'daemon-state', 'ssh-agent', 'publication-credential', 'provider-credential']) assert.equal(read(`${runDir}/${name}`), `SYNTHETIC:${runId}:${name}`);
    }
  } catch (error) { failure = error; }
  finally {
    for (const o of [...owned].reverse()) {
      try {
        if (!cleanup.some((c: any) => c.unit === o.unit)) cleanup.push(await clean(o));
      } catch (error) { failure = error; cleanup.push({ unit: o.unit, verified: false, error: String(error) }); }
      try { record({ role: o.role, finalLog: log(o), finalReport: report(o), cleanup: cleanup.find((c: any) => c.unit === o.unit) }); }
      catch (error) { failure = error; }
      if (o.logFd !== undefined) fs.closeSync(o.logFd);
      if (o.gateFd !== undefined) fs.closeSync(o.gateFd);
      if (o.memoryEventsFd !== undefined) fs.closeSync(o.memoryEventsFd);
    }
    observer?.close(); process.off('SIGINT', interrupt); process.off('SIGTERM', interrupt);
    fs.fsyncSync(rawFd); fs.fchmodSync(rawFd, 0o400); fs.fsyncSync(rawFd); fs.closeSync(rawFd); syncDirectory(runDir);
  }
  // Do not turn an admission rejection into success if ownership/cleanup was uncertain.
  const completeCleanup = attemptedUnits.length === owned.length && cleanup.length === owned.length && cleanup.every((c: any) => c.populated === 0);
  const result = expectedRejection && completeCleanup && owned.length === 1 ? 'expected-rejection' : failure || stop || !completeCleanup ? 'failed' : 'case-passed';
  const receipt = { schema: 1, result, unattendedSupported: false, nativeProof: 'pending independent reproduction and all required cases',
    profileHash, measuredTuple: p.identity, runId, testCase, commands,
    rawObservations: { path: rawPath, sha256: hash(fs.readFileSync(rawPath)), bytes: rawBytes }, cleanup,
    uncertainUnits: attemptedUnits.filter(unit => !owned.some(o => o.unit === unit)),
    error: failure ? String(failure) : null, at: new Date().toISOString() };
  const body = json(receipt).replaceAll(token, '<synthetic-token>') + '\n';
  assert.ok(Buffer.byteLength(body) < CONTROLS.evidenceBytes - CONTROLS.journalBytes);
  const receiptPath = `${runDir}/${result}-${hash(body)}.json`;
  budget(Buffer.byteLength(body), 1);
  exclusive(receiptPath, body);
  // Failed receipt remains immutable; journal failure must be durable before reporting it.
  journal.append({ kind: result === 'failed' ? 'failed' : 'completed', runId, profileHash, bootId: p.identity.bootId,
    detail: { receipt: receiptPath, sha256: hash(body), cleanup, result } });
  return { result, runId, testCase, receipt: receiptPath, unattendedSupported: false };
}

export async function main(argv: string[]) {
  const options: Record<string, string> = {};
  for (let i = 0; i < argv.length; i++) {
    const key = argv[i]; assert.ok(['--execute', '--prepare', '--initialize-journal', '--profile', '--profile-sha256', '--run-id', '--case'].includes(key), `unknown option ${key}`);
    assert.equal(options[key], undefined, `duplicate ${key}`);
    if (['--execute', '--prepare', '--initialize-journal'].includes(key)) options[key] = 'true';
    else { assert.ok(argv[i + 1] && !argv[i + 1].startsWith('--')); options[key] = argv[++i]; }
  }
  assert.equal(['--execute', '--prepare', '--initialize-journal'].filter(k => options[k]).length, 1, 'choose explicit --prepare or authorized Linux operation; no implicit launch');
  if (!options['--prepare']) assert.equal(process.platform, 'linux', 'native execution requires Linux; no effects performed');
  assert.ok(options['--profile'] && options['--profile-sha256'], 'operator-frozen measured profile and SHA256 required');
  const path = options['--profile']; assert.equal(resolve(path), path);
  if (!options['--prepare']) { trustedDirectory(dirname(path)); regular(path); }
  const bytes = fs.readFileSync(path, 'utf8'); assert.ok(Buffer.byteLength(bytes) <= 65536);
  const p = admission(JSON.parse(bytes), options['--prepare'] ? 'linux' : process.platform, options['--profile-sha256'], bytes);
  if (options['--initialize-journal']) {
    verifyHost(p); assert.ok(!options['--run-id'] && !options['--case']);
    assert.deepEqual(fs.readdirSync(`${BASE}/evidence`), [], 'initialization only on pristine evidence directory; lost journal cannot be recreated');
    fs.mkdirSync(`${BASE}/evidence/journal`, { mode: 0o700 }); syncDirectory(`${BASE}/evidence`);
    new Journal(`${BASE}/evidence/journal`).append({ kind: 'genesis', runId: '', profileHash: options['--profile-sha256'], bootId: p.identity.bootId, detail: { procedure: PROCEDURE } }, true);
    return { result: 'initialized', unattendedSupported: false };
  }
  const runId = options['--run-id'], testCase = options['--case'] as Case;
  assert.match(runId ?? '', /^[a-f0-9]{24}$/); assert.ok(CASES.includes(testCase));
  return options['--prepare'] ? prepare(p, options['--profile-sha256'], runId, testCase) : execute(p, options['--profile-sha256'], runId, testCase);
}
if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main(process.argv.slice(2)).then(result => console.log(JSON.stringify(result, null, 2))).catch(error => {
    console.error(JSON.stringify({ result: 'blocked', unattendedSupported: false, error: String(error) })); process.exitCode = 1;
  });
}
