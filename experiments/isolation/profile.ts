import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

export const selectedProfile = JSON.parse(readFileSync(process.env.GAFFER_ISOLATION_PROFILE ?? new URL('./profile.json', import.meta.url), 'utf8'));
assert.deepEqual(Object.keys(selectedProfile).sort(), ['architecture', 'engineVersion', 'id', 'image', 'kernelVersion']);
assert.match(selectedProfile.id, /^[a-z0-9-]+$/);
assert.ok(['arm64', 'amd64'].includes(selectedProfile.architecture));
assert.match(selectedProfile.image, /^[a-zA-Z0-9./:_-]+@sha256:[a-f0-9]{64}$/);
assert.equal(typeof selectedProfile.engineVersion, 'string');
assert.equal(typeof selectedProfile.kernelVersion, 'string');
export const IMAGE: string = selectedProfile.image;
export const PROFILE: string = selectedProfile.id;
export const LABEL = 'dev.gaffer.isolation-experiment';

// A new runtime is unsupported until this proof is rerun and independently reviewed.
export function assertRuntime(version: any, info: any) {
  assert.equal(version.Server.Version, selectedProfile.engineVersion);
  assert.equal(version.Server.Os, 'linux');
  assert.equal(version.Server.Arch, selectedProfile.architecture);
  assert.equal(version.Server.KernelVersion, selectedProfile.kernelVersion);
  assert.equal(info.CgroupVersion, '2');
  for (const key of ['MemoryLimit', 'SwapLimit', 'CpuCfsQuota', 'PidsLimit']) {
    assert.equal(info[key], true, `Missing ${key}`);
  }
  assert.ok(info.SecurityOptions.includes('name=seccomp,profile=builtin'));
}

export function containerArgs(name: string, runId: string, staging: string, volume: string, gateway = false) {
  return ['create', '--name', name, '--label', `${LABEL}=${runId}`,
    '--platform', `linux/${selectedProfile.architecture}`, '--pull', 'never', '--network', 'none',
    '--user', '1000:1000', '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges=true',
    '--read-only', '--init', '--cgroupns', 'private', '--ipc', 'private',
    '--cpus', '0.5', '--memory', '128m', '--memory-swap', '128m', '--pids-limit', '64',
    '--ulimit', 'nofile=256:256', '--ulimit', 'core=0:0', '--shm-size', '4m',
    '--tmpfs', '/work:rw,exec,nosuid,nodev,size=32m,nr_inodes=4096,uid=1000,gid=1000,mode=0700',
    '--tmpfs', '/tmp:rw,noexec,nosuid,nodev,size=8m,nr_inodes=1024,uid=1000,gid=1000,mode=0700',
    '--mount', `type=bind,source=${staging},target=/fixture,readonly`,
    '--mount', `type=volume,source=${volume},target=/router,volume-nocopy${gateway ? '' : ',readonly'}`,
    '--workdir', '/work', '--env', 'HOME=/work', '--env', 'NODE_OPTIONS=',
    '--log-driver', 'local', '--log-opt', 'max-size=1m', '--log-opt', 'max-file=1', '--log-opt', 'compress=false',
    '--restart', 'no'];
}

export function assertContainer(state: any, staging: string, volume: string, gateway = false) {
  const h = state.HostConfig;
  assert.equal(state.Config.Image, IMAGE);
  assert.equal(state.Config.User, '1000:1000');
  assert.equal(h.NetworkMode, 'none');
  assert.equal(h.Privileged, false);
  assert.equal(h.ReadonlyRootfs, true);
  assert.deepEqual(h.CapDrop, ['ALL']);
  assert.ok(!h.CapAdd?.length);
  assert.deepEqual(h.SecurityOpt, ['no-new-privileges=true']);
  assert.equal(h.Init, true);
  assert.equal(h.PidMode, '');
  assert.equal(h.IpcMode, 'private');
  assert.equal(h.CgroupnsMode, 'private');
  assert.equal(h.Memory, 128 * 1024 * 1024);
  assert.equal(h.MemorySwap, h.Memory);
  assert.equal(h.NanoCpus, 500_000_000);
  assert.equal(h.PidsLimit, 64);
  assert.equal(h.ShmSize, 4 * 1024 * 1024);
  assert.equal(h.RestartPolicy.Name, 'no');
  assert.equal(h.LogConfig.Type, 'local');
  assert.deepEqual(h.LogConfig.Config, { 'max-file': '1', 'max-size': '1m', compress: 'false' });
  assert.deepEqual(Object.keys(h.Tmpfs).sort(), ['/tmp', '/work']);
  assert.equal(h.Tmpfs['/work'], 'rw,exec,nosuid,nodev,size=32m,nr_inodes=4096,uid=1000,gid=1000,mode=0700');
  assert.equal(h.Tmpfs['/tmp'], 'rw,noexec,nosuid,nodev,size=8m,nr_inodes=1024,uid=1000,gid=1000,mode=0700');
  assert.equal(state.Mounts.length, 2);
  assert.ok(state.Mounts.some((m: any) => m.Type === 'bind' && m.Source === staging && m.Destination === '/fixture' && !m.RW));
  assert.ok(state.Mounts.some((m: any) => m.Type === 'volume' && m.Name === volume && m.Destination === '/router' && m.RW === gateway));
  assert.ok(!h.Devices?.length && !h.DeviceRequests?.length && !Object.keys(h.PortBindings ?? {}).length);
}

export async function cleanupOwned(
  ids: string[], volume: string | undefined,
  docker: (args: string[]) => Promise<string>,
  runId: string,
) {
  // Failure propagates: no pass or unattended-support claim if termination/removal is uncertain.
  const errors: string[] = [];
  for (const id of ids) {
    try {
      const state = JSON.parse(await docker(['inspect', id]))[0];
      assert.equal(state.Config.Labels[LABEL], runId, 'Container ownership mismatch');
      await docker(['rm', '-f', id]);
    } catch { errors.push(`Cannot verify/remove owned container ${id}`); }
  }
  if (volume) {
    try {
      const state = JSON.parse(await docker(['volume', 'inspect', volume]))[0];
      assert.equal(state.Labels[LABEL], runId, 'Volume ownership mismatch');
      await docker(['volume', 'rm', volume]);
    } catch { errors.push(`Cannot verify/remove owned volume ${volume}`); }
  }
  if (errors.length) throw new Error(errors.join('; '));
}
