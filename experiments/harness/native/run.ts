import { spawn, execFileSync } from 'node:child_process';
import { readFileSync, writeFileSync, readdirSync, lstatSync, existsSync } from 'node:fs';
import { keys } from '../../inference-boundary/json.ts';
import { PROFILE, config, environment, settingsDigest, probe, digest, NativeEvents, classify, type NativeEvent, type Outcome } from './client.ts';

export const BRIEF = 'Change greeting.txt from hello to hello from probe.';
export const CONTENT = 'hello from probe\n';
export interface RunRequest {
  schema: 1; profile: typeof PROFILE; bindingDigest: string; settingsDigest: string; baseSHA: string;
  brief: typeof BRIEF; approval: 'allow' | 'ask'; limits: { wallMs: number; outputBytes: number }; token: string;
}
export interface Artifact { baseSHA: string; headSHA: string; path: 'greeting.txt'; content: string; changedFiles: string[]; status: string; diff: string }
export interface RunResult { outcome: Outcome; exitCode: number | null; signal: string | null; events: NativeEvent[]; stderr: string; artifact: Artifact | null; bindingDigest: string; settingsDigest: string; usage: 'unverified_native_observation'; localProcessExited: boolean; upstreamQuiescence: 'unknown'; reason?: string }
export function validateRunRequest(r: RunRequest) {
  keys(r, ['schema', 'profile', 'bindingDigest', 'settingsDigest', 'baseSHA', 'brief', 'approval', 'limits', 'token'], ['schema', 'profile', 'bindingDigest', 'settingsDigest', 'baseSHA', 'brief', 'approval', 'limits', 'token']);
  keys(r.limits, ['wallMs', 'outputBytes'], ['wallMs', 'outputBytes']);
  if (r.schema !== 1 || r.profile !== PROFILE || !['allow', 'ask'].includes(r.approval) || r.brief !== BRIEF || typeof r.baseSHA !== 'string' || !/^[a-f0-9]{40}$/.test(r.baseSHA) || ![r.bindingDigest, r.settingsDigest, r.token].every(x => typeof x === 'string' && /^[a-f0-9]{64}$/.test(x)) || r.settingsDigest !== settingsDigest(r.approval)) throw Error('unsupported_native_run');
  if (!Number.isSafeInteger(r.limits.wallMs) || r.limits.wallMs < 1 || r.limits.wallMs > 30000 || !Number.isSafeInteger(r.limits.outputBytes) || r.limits.outputBytes < 1 || r.limits.outputBytes > 262144) throw Error('unsupported_native_limits');
}
export function validateInventory(files: string[], hooks: string[]) {
  if (!files.includes('.git') || !files.includes('greeting.txt') || files.some(x => !['.git', 'greeting.txt', 'AGENTS.md'].includes(x)) || hooks.some(x => !x.endsWith('.sample'))) throw Error('ambient_repository_configuration');
}
function assertWorkspace() {
  validateInventory(readdirSync('/work/repo'), readdirSync('/work/repo/.git/hooks'));
  for (const name of readdirSync('/work/repo')) {
    const stat = lstatSync('/work/repo/' + name);
    if (stat.isSymbolicLink() || (name === '.git' ? !stat.isDirectory() : !stat.isFile() || stat.size > 4096)) throw Error('unsupported_workspace_entry');
  }
  for (const path of ['/work/auth.json', '/work/config/opencode/auth.json', '/work/data/opencode/auth.json', '/work/repo/.git/config.worktree', '/state', '/control', '/private', '/egress', '/router-source', '/probe', '/var/run/docker.sock', '/root/.codex/auth.json', '/Users']) if (existsSync(path)) throw Error('ambient_native_state');
}
export const artifactMatches = (artifact: Artifact | null, baseSHA: string) => !!artifact && artifact.baseSHA === baseSHA && artifact.headSHA === baseSHA && artifact.path === 'greeting.txt' && artifact.content === CONTENT && digest(artifact.changedFiles) === digest(['greeting.txt']) && artifact.status.trim() === 'M greeting.txt' && artifact.diff.length > 0;
export interface RunHandle { events(after?: number): NativeEvent[]; done: Promise<RunResult>; cancel(): Promise<{ localProcessExited: boolean; upstreamQuiescence: 'unknown'; outcome: 'cancelled_unknown' }> }
export function assertExecutionEnvironment() {
  if (process.platform !== 'linux' || !readFileSync('/proc/self/status', 'utf8').includes('NoNewPrivs:\t1') || !readFileSync('/proc/self/mountinfo', 'utf8').includes('/router')) throw Error('unverified_execution_environment');
}
export function start(input: RunRequest): RunHandle {
  const r = structuredClone(input); validateRunRequest(r);
  assertExecutionEnvironment();
  assertWorkspace(); probe('/fixture/opencode');
  const git = (...args: string[]) => execFileSync('git', ['-C', '/work/repo', ...args], { encoding: 'utf8', env: { PATH: '/usr/local/bin:/usr/bin:/bin', HOME: '/work', GIT_CONFIG_NOSYSTEM: '1', GIT_CONFIG_GLOBAL: '/dev/null' } });
  if (git('rev-parse', 'HEAD').trim() !== r.baseSHA || git('status', '--porcelain').trim() || readFileSync('/work/repo/greeting.txt', 'utf8') !== 'hello\n') throw Error('base_mismatch');
  writeFileSync('/work/config.json', JSON.stringify(config(r.token, r.approval)), { mode: 0o600 });
  const parsed = new NativeEvents(r.limits.outputBytes); let cancelled = false, invalid = false, reason: string | undefined, stderr = '', stderrBytes = 0;
  const child = spawn('/fixture/opencode', ['run', '--format', 'json', '--model', 'openai/gpt-6-astra', '--title', 'Fixed native fixture', r.brief], { cwd: '/work/repo', env: environment(), stdio: ['ignore', 'pipe', 'pipe'] });
  let kill: NodeJS.Timeout | undefined;
  const terminate = () => { if (child.exitCode === null && child.signalCode === null) { child.kill('SIGTERM'); kill ??= setTimeout(() => child.kill('SIGKILL'), 1000); } };
  const timer = setTimeout(() => { cancelled = true; reason = 'wall_deadline'; terminate(); }, r.limits.wallMs);
  child.stdout.on('data', chunk => { try { parsed.push(chunk); } catch (error: any) { invalid = true; reason = error.message; terminate(); } });
  child.stderr.on('data', chunk => { stderrBytes += chunk.length; if (stderrBytes > r.limits.outputBytes) { invalid = true; reason = 'stderr_limit'; terminate(); } else stderr += chunk.toString(); });
  const done = new Promise<RunResult>(resolve => {
    child.once('error', () => { invalid = true; reason = 'spawn_failed'; });
    child.once('close', (exitCode, signal) => {
      clearTimeout(timer); if (kill) clearTimeout(kill);
      try { parsed.end(); } catch (error: any) { invalid = true; reason = error.message; }
      let artifact: Artifact | null = null;
      try { assertWorkspace(); artifact = { baseSHA: r.baseSHA, headSHA: git('rev-parse', 'HEAD').trim(), path: 'greeting.txt', content: readFileSync('/work/repo/greeting.txt', 'utf8'), changedFiles: git('diff', '--no-ext-diff', '--no-textconv', '--name-only').trim().split('\n').filter(Boolean), status: git('status', '--porcelain'), diff: git('diff', '--no-ext-diff', '--no-textconv') }; } catch { reason ??= 'artifact_unreadable'; }
      resolve({ outcome: classify(parsed.values, exitCode, signal, cancelled, invalid, artifactMatches(artifact, r.baseSHA)), exitCode, signal, events: parsed.values, stderr, artifact, bindingDigest: r.bindingDigest, settingsDigest: r.settingsDigest, usage: 'unverified_native_observation', localProcessExited: true, upstreamQuiescence: 'unknown', ...(reason ? { reason } : {}) });
    });
  });
  return { events(after = 0) { if (!Number.isSafeInteger(after) || after < 0) throw Error('invalid_cursor'); return structuredClone(parsed.values.filter(e => e.sequence > after)); }, done,
    async cancel() { cancelled = true; reason = 'cancel_requested'; terminate(); const result = await done; return { localProcessExited: result.localProcessExited, upstreamQuiescence: 'unknown', outcome: 'cancelled_unknown' }; } };
}
