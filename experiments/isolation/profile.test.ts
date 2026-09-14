import { test } from 'node:test';
import assert from 'node:assert/strict';
import { assertRuntime, cleanupOwned, containerArgs, LABEL, selectedProfile } from './profile.ts';

const version = { Server: { Version: selectedProfile.engineVersion, Os: 'linux', Arch: selectedProfile.architecture, KernelVersion: selectedProfile.kernelVersion } };
const info = { CgroupVersion: '2', MemoryLimit: true, SwapLimit: true, CpuCfsQuota: true, PidsLimit: true, SecurityOptions: ['name=seccomp,profile=builtin'] };
test('unsupported runtime or missing kernel enforcement blocks before launch', () => {
  assertRuntime(version, info);
  for (const key of ['Version', 'Os', 'Arch', 'KernelVersion']) {
    const changed = structuredClone(version);
    changed.Server[key as keyof typeof changed.Server] = 'unsupported';
    assert.throws(() => assertRuntime(changed, info));
  }
  for (const key of ['CgroupVersion', 'MemoryLimit', 'SwapLimit', 'CpuCfsQuota', 'PidsLimit', 'SecurityOptions']) {
    const changed: any = { ...info, [key]: null };
    assert.throws(() => assertRuntime(version, changed));
  }
});
test('worker has no IP network and only read-only fixture/socket mounts', () => {
  const args = containerArgs('test', 'id', '/task-fixture', 'task-socket');
  assert.equal(args[args.indexOf('--network') + 1], 'none');
  const mounts = args.filter((_, i) => args[i - 1] === '--mount');
  assert.equal(mounts.length, 2);
  assert.ok(mounts.every(m => m.endsWith(',readonly')));
});
test('failed termination still attempts all owned cleanup and fails closed', async () => {
  const calls: string[][] = [];
  await assert.rejects(cleanupOwned(['task-one', 'task-two'], 'task-volume', async args => {
    calls.push(args);
    if (args[0] === 'rm' && args.at(-1) === 'task-one') throw new Error('runtime unavailable');
    return JSON.stringify([{ Config: { Labels: { [LABEL]: 'run' } }, Labels: { [LABEL]: 'run' } }]);
  }, 'run'), /Cannot verify\/remove owned container task-one/);
  assert.deepEqual(calls.filter(args => args.includes('rm')), [['rm', '-f', 'task-one'], ['rm', '-f', 'task-two'], ['volume', 'rm', 'task-volume']]);
});
test('failed volume cleanup is a containment failure', async () => {
  await assert.rejects(cleanupOwned([], 'task-volume', async () => { throw new Error('volume busy'); }, 'run'), /Cannot verify\/remove owned volume/);
});
test('cleanup refuses containers and volumes with a different ownership label', async () => {
  const calls: string[][] = [];
  await assert.rejects(cleanupOwned(['unrelated-container'], 'unrelated-volume', async args => {
    calls.push(args);
    return JSON.stringify([{ Config: { Labels: {} }, Labels: {} }]);
  }, 'run'), /Cannot verify\/remove/);
  assert.ok(calls.every(args => !args.includes('rm')));
});
