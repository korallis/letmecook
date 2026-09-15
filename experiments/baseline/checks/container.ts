import { spawn } from 'node:child_process';
import { randomUUID } from 'node:crypto';

export const IMAGE = 'sha256:f95ae5b2218838d3da613273705b7914a149593c400a33087e9c383c2e12650a';
export const NODE = '/usr/local/bin/node';
export const ENVIRONMENT = Object.freeze({ arch: 'arm64', platform: 'linux', execPath: NODE, env: Object.freeze({
  PATH: '/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin', HOSTNAME: 'baseline-check',
  NODE_VERSION: '24.21.0', YARN_VERSION: '1.22.22', HOME: '/work/home', NODE_OPTIONS: '',
}) });
type Command = { code: number | null; stdout: Buffer; stderr: Buffer; reason: string | null };
export async function docker(args: string[], deadline: number, bytes = 65536, signal?: AbortSignal): Promise<Command> {
  if (signal?.aborted || Date.now() >= deadline) return { code: null, stdout: Buffer.alloc(0), stderr: Buffer.alloc(0), reason: signal?.aborted ? 'aborted' : 'deadline' };
  return new Promise(resolve => {
    const child = spawn('docker', [...(process.env.GAFFER_DOCKER_CONTEXT ? ['--context', process.env.GAFFER_DOCKER_CONTEXT] : []), ...args], {
      stdio: ['ignore', 'pipe', 'pipe'], shell: false,
    });
    const stdout: Buffer[] = [], stderr: Buffer[] = []; let size = 0, reason: string | null = null;
    const stop = (why: string) => { reason ??= why; child.kill('SIGKILL'); };
    const timer = setTimeout(() => stop('deadline'), Math.max(1, deadline - Date.now()));
    const abort = () => stop('aborted'); signal?.addEventListener('abort', abort, { once: true });
    const capture = (list: Buffer[], chunk: Buffer) => { const available = bytes - size; if (available > 0) { const retained = Buffer.from(chunk.subarray(0, available)); list.push(retained); size += retained.length; } if (chunk.length > available) stop('output-limit'); };
    child.stdout.on('data', chunk => capture(stdout, chunk)); child.stderr.on('data', chunk => capture(stderr, chunk));
    child.on('error', () => { reason ??= 'docker-command-error'; });
    child.on('close', code => { clearTimeout(timer); signal?.removeEventListener('abort', abort); if (Date.now() >= deadline) reason ??= 'deadline'; resolve({ code, stdout: Buffer.concat(stdout), stderr: Buffer.concat(stderr), reason }); });
  });
}

function inspectSettings(value: any, mounts: { source: string; target: string }[], argv: string[], cwd: string) {
  const h = value.HostConfig, c = value.Config;
  const imageEnv = Object.entries(ENVIRONMENT.env);
  if (value.Image !== IMAGE || h.NetworkMode !== 'none' || h.Memory !== 134217728 || h.MemorySwap !== 134217728 || h.PidsLimit !== 32 || h.NanoCpus !== 500000000 ||
    h.ReadonlyRootfs !== true || h.Init !== true || h.Privileged !== false || h.PublishAllPorts !== false || h.CapAdd?.length || h.Devices?.length || h.DeviceRequests?.length || h.Binds?.length ||
    h.PidMode !== '' || h.IpcMode !== 'private' || h.CgroupnsMode !== 'private' || h.UTSMode !== '' || h.RestartPolicy.Name !== 'no' || h.LogConfig.Type !== 'none' || JSON.stringify(h.CapDrop) !== '["ALL"]' ||
    JSON.stringify(h.SecurityOpt) !== '["no-new-privileges"]' || JSON.stringify(c.Healthcheck?.Test) !== '["NONE"]' || c.User !== '65532:65532' || c.Hostname !== 'baseline-check' || c.WorkingDir !== cwd ||
    JSON.stringify(c.Entrypoint) !== JSON.stringify([NODE]) || JSON.stringify(c.Cmd) !== JSON.stringify(argv.slice(1)) ||
    value.Mounts.length !== mounts.length || mounts.some(m => !value.Mounts.some((actual: any) => actual.Type === 'bind' && actual.Source === m.source && actual.Destination === m.target && actual.RW === false)) ||
    Object.keys(h.Tmpfs ?? {}).length !== 1 || h.Tmpfs['/tmp'] !== 'rw,noexec,nosuid,nodev,size=16777216,mode=1777' ||
    c.Env.length !== imageEnv.length || imageEnv.some(([key, v]) => !c.Env.includes(`${key}=${v}`))) throw new Error('container-isolation-mismatch');
}

export type ContainerResult = {
  id: string | null; name: string; startedAt: number; endedAt: number; exitCode: number | null; reason: string | null;
  cleanup: boolean; cleanupVerificationFaultInjected: boolean; stdoutBase64: string; stderrBase64: string; outputBytes: number;
  inspection: unknown; state: unknown; diagnostics: { phase: string; code: number | null; reason: string | null; stdoutBase64: string; stderrBase64: string }[];
};

export async function runContainer(argv: string[], cwd: string, mounts: { source: string; target: string }[], deadline: number, outputBytes: number, signal?: AbortSignal, failCleanupVerification = false): Promise<ContainerResult> {
  const name = 'gaffer-baseline-check-' + randomUUID();
  const result: ContainerResult = { id: null, name, startedAt: Date.now(), endedAt: Date.now(), exitCode: null, reason: null, cleanup: false, cleanupVerificationFaultInjected: failCleanupVerification, stdoutBase64: '', stderrBase64: '', outputBytes: 0, inspection: null, state: null, diagnostics: [] };
  if (signal?.aborted || Date.now() >= deadline) return { ...result, reason: signal?.aborted ? 'aborted' : 'deadline', cleanup: true };
  const record = (phase: string, command: Command) => { result.diagnostics.push({ phase, code: command.code, reason: command.reason, stdoutBase64: command.stdout.subarray(0, 8192).toString('base64'), stderrBase64: command.stderr.subarray(0, 8192).toString('base64') }); };
  let identifier = name;
  try {
    const create = await docker(['create', '--pull=never', '--name', name, '--network', 'none', '--read-only', '--init', '--memory', '128m', '--memory-swap', '128m', '--pids-limit', '32', '--cpus', '0.5',
      '--ipc', 'private', '--cgroupns', 'private', '--log-driver', 'none', '--no-healthcheck',
      '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges', '--user', '65532:65532', '--hostname', 'baseline-check', '--workdir', cwd,
      '--tmpfs', '/tmp:rw,noexec,nosuid,nodev,size=16777216,mode=1777', ...mounts.flatMap(m => ['--mount', `type=bind,src=${m.source},dst=${m.target},readonly`]),
      ...Object.entries(ENVIRONMENT.env).flatMap(([key, value]) => ['--env', `${key}=${value}`]), '--entrypoint', NODE, IMAGE, ...argv.slice(1)], deadline, 8192, signal);
    record('create', create);
    if (create.reason || create.code !== 0 || !/^[a-f0-9]{64}\n?$/.test(create.stdout.toString())) throw new Error(create.reason ?? 'container-create-failed');
    identifier = result.id = create.stdout.toString().trim();
    const inspected = await docker(['inspect', identifier], deadline, 131072, signal);
    if (inspected.reason || inspected.code !== 0) record('inspect', inspected);
    if (inspected.reason || inspected.code !== 0) throw new Error(inspected.reason ?? 'container-inspect-failed');
    const actual = JSON.parse(inspected.stdout.toString())[0]; inspectSettings(actual, mounts, argv, cwd);
    result.inspection = { image: actual.Image, user: actual.Config.User, network: actual.HostConfig.NetworkMode, memory: actual.HostConfig.Memory, memorySwap: actual.HostConfig.MemorySwap,
      pids: actual.HostConfig.PidsLimit, readonlyRoot: actual.HostConfig.ReadonlyRootfs, capDrop: actual.HostConfig.CapDrop, securityOpt: actual.HostConfig.SecurityOpt,
      mounts: actual.Mounts.map((m: any) => ({ target: m.Destination, readonly: !m.RW })), argv, cwd };
    const started = await docker(['start', '--attach', identifier], deadline, outputBytes, signal);
    record('start', { ...started, stdout: Buffer.alloc(0), stderr: Buffer.alloc(0) });
    result.stdoutBase64 = started.stdout.toString('base64'); result.stderrBase64 = started.stderr.toString('base64'); result.outputBytes = started.stdout.length + started.stderr.length;
    result.reason = started.reason;
    const state = await docker(['inspect', '--format', '{{json .State}}', identifier], Math.min(deadline, Date.now() + 3000), 8192, signal);
    record('state', state);
    if (state.reason || state.code !== 0) result.reason ??= state.reason ?? 'container-state-unknown';
    else {
      const value = JSON.parse(state.stdout.toString()); result.state = value;
      if (value.Status !== 'exited' || value.Running || value.Dead || value.Error) result.reason ??= 'container-state-unknown';
      else if (!Number.isSafeInteger(value.ExitCode) || value.ExitCode < 0 || value.ExitCode > 255) result.reason ??= 'container-exit-unknown';
      else {
        result.exitCode = value.ExitCode;
        if (value.OOMKilled) result.reason ??= 'container-oom';
        if (started.code !== value.ExitCode) result.reason ??= 'container-attach-exit-mismatch';
      }
    }
  } catch (error) { result.reason ??= error instanceof Error && /^[a-z-]+$/.test(error.message) ? error.message : 'container-operation-failed'; }
  finally {
    // The whole container, including detached descendants, is stopped. Cleanup
    // has a separate bounded grace period even when the check deadline expires.
    const until = Date.now() + 5000;
    const removed = await docker(['rm', '--force', identifier], until, 8192);
    const absent = await docker(['inspect', identifier], until, 8192);
    record('remove', removed); record('absence', absent);
    // An interrupted create may finish in the daemon after an absence query.
    // Only an acknowledged ID allows removal to establish cleanup.
    result.cleanup = result.id !== null && !removed.reason && !absent.reason && absent.code === 1 && /no such (object|container)/i.test(absent.stderr.toString()) && !failCleanupVerification;
    if (!result.cleanup) result.reason ??= 'container-cleanup-unconfirmed';
    result.endedAt = Date.now();
  }
  return result;
}
