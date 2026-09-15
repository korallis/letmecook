import assert from 'node:assert/strict';
import { test } from 'node:test';
import * as fs from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { admission, validateProfile, CONTROLS, PROCEDURE, BASE, hash, Journal, exclusive, assertEffective, assertOwned, prepare, main } from './run.ts';

// Deliberately synthetic objects for pure logic. No host inventory, native entrypoint,
// system service, namespace, mount, network socket or pressure operation runs here.
function synthetic() {
  const digest = hash('SYNTHETIC TEST BYTES, NOT MEASURED HOST EVIDENCE');
  const paths = ['/usr/bin/systemctl', '/usr/bin/systemd-run', '/usr/bin/systemd-analyze', '/usr/bin/node', '/usr/bin/git',
    '/usr/bin/unshare', '/usr/bin/nsenter', '/usr/bin/mount', '/usr/bin/ping', '/usr/bin/true', '/usr/bin/sleep', '/usr/bin/dash', '/usr/bin/env',
    `${BASE}/rootfs/fixture/probe.ts`, `${BASE}/controller/run.ts`, `${BASE}/image-provenance.json`, `${BASE}/hypervisor-provenance.json`];
  return {
    schema: 1, id: 'm0-debian13-amd64-systemd-native-v1', status: 'measured', unattendedSupported: false, procedureSha256: PROCEDURE,
    identity: { osReleaseSha256: digest, kernelRelease: '6.12.0-SYNTHETIC', kernelConfigSha256: digest,
      bootId: '00000000-0000-0000-0000-000000000001', machineIdSha256: digest, systemdVersion: 'systemd 257 (SYNTHETIC)',
      nodeVersion: 'v24.0.0', architecture: 'x64', imageSha256: digest, hypervisorSha256: digest, rootfsSha256: digest,
      controllerNamespaces: Object.fromEntries(['user', 'pid', 'net', 'ipc', 'mnt', 'cgroup'].map(name => [name, `${name}:[1]`])) },
    ids: { workerUid: 2001, workerGid: 2001, mockUid: 2002, mockGid: 2002, socketGid: 2003 },
    storage: { evidenceDevice: 1, evidenceInode: 2, evidenceFsType: 0xef53, rootfsDevice: 1, rootfsInode: 3 },
    artifacts: paths.map(path => ({ path, sha256: digest })), controls: { ...CONTROLS },
    headroom: { controllerBytes: 128 * 1024 ** 2, osBytes: 256 * 1024 ** 2, diskReserveBytes: 16 * 1024 ** 2, inodeReserve: 256,
      controllerSliceDevice: 1, controllerSliceInode: 4 },
  };
}
function temporary(action: (directory: string) => void): void {
  const directory = fs.mkdtempSync(join(tmpdir(), 'gaffer-89-offline-'));
  try { return action(directory); } finally { fs.rmSync(directory, { recursive: true, force: true }); }
}
const identity = { profileHash: hash('SYNTHETIC PROFILE'), bootId: '00000000-0000-0000-0000-000000000001' };
const runId = 'a'.repeat(24), nextId = 'b'.repeat(24);
function initialized(directory: string) {
  const path = join(directory, 'journal'); fs.mkdirSync(path, { mode: 0o700 });
  const journal = new Journal(path, process.getuid!());
  journal.append({ ...identity, kind: 'genesis', runId: '', detail: {} }, true);
  return journal;
}
test('default, non-Linux, digest drift and all missing identity/control fields deny admission', () => {
  const p = synthetic(), bytes = JSON.stringify(p);
  assert.equal(admission(p, 'linux', hash(bytes), bytes).unattendedSupported, false);
  assert.throws(() => admission(p, 'darwin', hash(bytes), bytes), /Linux/);
  assert.throws(() => admission(p, 'linux', hash('wrong'), bytes), /digest mismatch/);
  assert.throws(() => validateProfile(JSON.parse(fs.readFileSync(new URL('./profile.json', import.meta.url), 'utf8'))), /ineligible/);
  for (const field of Object.keys(p.identity)) {
    const missing = structuredClone(p); delete (missing.identity as any)[field]; assert.throws(() => validateProfile(missing), field);
  }
  for (const field of Object.keys(p.controls)) {
    const missing = structuredClone(p); delete (missing.controls as any)[field]; assert.throws(() => validateProfile(missing), field);
  }
  for (const mutate of [
    (p: any) => { p.identity.kernelRelease = '*'; },
    (p: any) => { p.identity.rootfsSha256 = '0'.repeat(64); },
    (p: any) => { p.controls.pidsMax = 'max'; },
    (p: any) => { p.storage.evidenceFsType = 0x01021994; },
    (p: any) => { p.ids.workerUid = 0; },
    (p: any) => { p.ids.workerUid = p.ids.mockUid; },
    (p: any) => { p.ids.socketGid = p.ids.workerGid; },
    (p: any) => { p.force = true; },
    (p: any) => { p.artifacts[0].path = '/tmp/untrusted-systemctl'; },
  ]) { const bad = structuredClone(p); mutate(bad); assert.throws(() => validateProfile(bad)); }
});
test('effective pids.max=max rejects before simulated repository start; cleanup requires exact identity', () => {
  const effective = { 'cpu.max': '50000 100000', 'memory.max': '134217728', 'memory.swap.max': '0', 'pids.max': '64' };
  let repositoryStarted = false;
  assert.throws(() => { assertEffective({ ...effective, 'pids.max': 'max' }); repositoryStarted = true; }, /pids.max/);
  assert.equal(repositoryStarted, false); assertEffective(effective);
  for (const field of Object.keys(effective)) { const missing = { ...effective }; delete (missing as any)[field]; assert.throws(() => assertEffective(missing)); }
  const owned = { unit: 'synthetic', invocation: 'one', cgroup: '/synthetic', device: 1, inode: 2 };
  assertOwned(owned, { ...owned });
  for (const field of Object.keys(owned)) assert.throws(() => assertOwned(owned, { ...owned, [field]: 'reused' }));
});
test('missing, corrupt, torn and symlinked journals fail closed', () => temporary(directory => {
  assert.throws(() => new Journal(join(directory, 'missing'), process.getuid!()).assertLaunch(identity.profileHash, runId));
  const journal = initialized(directory), path = join(journal.directory, 'journal.jsonl');
  const bytes = fs.readFileSync(path);
  fs.appendFileSync(path, '{'); assert.throws(() => journal.assertLaunch(identity.profileHash, runId), /torn/);
  fs.writeFileSync(path, bytes.toString().replace('genesis', 'corrupt')); assert.throws(() => journal.assertLaunch(identity.profileHash, runId));
  fs.unlinkSync(path); const other = join(directory, 'other'); fs.writeFileSync(other, bytes); fs.symlinkSync(other, path);
  assert.throws(() => journal.assertLaunch(identity.profileHash, runId), /unsafe file/);
}));
test('intent survives a new Journal instance and denies next launch, including changed boot', () => temporary(directory => {
  const journal = initialized(directory); journal.assertLaunch(identity.profileHash, runId);
  journal.append({ ...identity, kind: 'intent', runId, detail: { units: ['synthetic'] } });
  const afterLoss = new Journal(journal.directory, process.getuid!());
  assert.throws(() => afterLoss.assertLaunch(identity.profileHash, nextId), /unresolved/);
  assert.throws(() => afterLoss.assertLaunch(identity.profileHash, runId), /replayed/);
  assert.equal(afterLoss.read().at(-1)!.bootId, identity.bootId);
}));
test('stale competing admission cannot append another intent', () => temporary(directory => {
  const first = initialized(directory), second = new Journal(first.directory, process.getuid!());
  first.assertLaunch(identity.profileHash, runId); second.assertLaunch(identity.profileHash, nextId);
  first.append({ ...identity, kind: 'intent', runId, detail: {} });
  assert.throws(() => second.append({ ...identity, kind: 'intent', runId: nextId, detail: {} }), /unresolved/);
  assert.throws(() => first.assertLaunch(identity.profileHash, nextId), /interrupted/);
}));
test('failed receipts remain exclusive and hash-bound; resolution never replays failed run', () => temporary(directory => {
  const journal = initialized(directory), receipt = join(directory, 'failed.json'), body = '{"result":"failed","unattendedSupported":false}\n';
  journal.append({ ...identity, kind: 'intent', runId, detail: {} });
  exclusive(receipt, body);
  journal.append({ ...identity, kind: 'failed', runId, detail: { receipt, sha256: hash(body) } });
  assert.throws(() => exclusive(receipt, '{"result":"pass"}'), /EEXIST/);
  assert.equal(hash(fs.readFileSync(receipt)), hash(body));
  assert.throws(() => journal.assertLaunch(identity.profileHash, nextId), /unresolved/);
  const failure = journal.read().at(-1)!;
  journal.append({ ...identity, kind: 'resolution', runId, detail: { failedRecordDigest: failure.digest, replayAllowed: false,
    cleanupVerified: true, resources: [{ populated: 0, processes: [], active: 'inactive' }] } });
  assert.throws(() => journal.assertLaunch(identity.profileHash, runId), /replayed/);
  journal.assertLaunch(identity.profileHash, nextId); assert.equal(fs.readFileSync(receipt, 'utf8'), body);
}));
test('journal supports bounded full case campaign without a sixteen-run/file-count shortcut', () => temporary(directory => {
  const journal = initialized(directory);
  for (let n = 1; n <= 32; n++) {
    const id = n.toString(16).padStart(24, '0'); journal.assertLaunch(identity.profileHash, id);
    journal.append({ ...identity, kind: 'intent', runId: id, detail: {} });
    journal.append({ ...identity, kind: 'completed', runId: id, detail: { syntheticOnly: true } });
  }
  assert.equal(journal.read().length, 65); assert.deepEqual(fs.readdirSync(journal.directory), ['journal.jsonl']);
}));
test('prepare emits bounded argv data, refuses injection; no native execution', async () => {
  const p = validateProfile(synthetic()), output = prepare(p, identity.profileHash, runId, 'missing-pids-limit');
  assert.equal(output.result, 'NOT RUN'); assert.equal(output.unattendedSupported, false);
  assert.throws(() => prepare(p, identity.profileHash, 'x; touch /tmp/unsafe', 'baseline'));
  await assert.rejects(main(['--execute', '--force']), /unknown option/);
  if (process.platform !== 'linux') {
    await assert.rejects(main(['--execute', '--case', 'missing-pids-limit']), /Linux/);
    // Real CLI subprocess reaches only platform refusal; never a native host or deployment.
    const cli = spawnSync(process.execPath, [fileURLToPath(new URL('./run.ts', import.meta.url)), '--execute', '--case', 'missing-pids-limit'], { encoding: 'utf8', env: { PATH: '/usr/bin:/bin' } });
    assert.equal(cli.status, 1); assert.match(cli.stderr, /requires Linux/);
    const probe = spawnSync(process.execPath, [fileURLToPath(new URL('./probe.ts', import.meta.url)), '--bootstrap'], { encoding: 'utf8', env: { PATH: '/usr/bin:/bin' } });
    assert.equal(probe.status, 1); assert.match(probe.stderr, /requires Linux/);
  }
});
