import { createHash } from 'node:crypto';
import { spawn, execFileSync, type ChildProcess } from 'node:child_process';
import { readFileSync, writeFileSync, openSync, readSync, closeSync, readdirSync, lstatSync } from 'node:fs';
import { parseJSON, object, keys } from '../inference-boundary/json.ts';
import { OPENCODE_PROFILE, OPENCODE_ROUTER_PROFILE } from '../inference-boundary/profile-ids.ts';
import { validateRouterPolicy, type RouterPolicy } from '../inference-boundary/router-policy.ts';
import { validateBinding, type Binding } from '../inference-boundary/types.ts';

export const pins = JSON.parse(readFileSync(new URL('./pins.json', import.meta.url), 'utf8'));
export const describe = () => ({ schema: 1, adapter: 'opencode-m0-v1', harness: pins.version,
  nativeProtocol: pins.nativeProtocol, profile: OPENCODE_PROFILE, routerProfile: OPENCODE_ROUTER_PROFILE, protocol: 'chat/completions',
  structuredEvents: true, tokenStreamingEvents: false, providerStructuredOutput: 'unproved',
  approvals: 'CLI rejects asks; blocked outcome; no interactive approval or auto-approve',
  resume: false, settings: 'no reasoning effort admitted', usage: 'native observation only; missing upstream usage stays unknown',
  isolation: pins.isolationVariant, liveReadiness: 'blocked' });
export function probe(binary: string) {
  const hash = createHash('sha256'), fd = openSync(binary, 'r'), buffer = Buffer.alloc(1048576);
  try { let bytes: number; while ((bytes = readSync(fd, buffer)) > 0) hash.update(buffer.subarray(0, bytes)); } finally { closeSync(fd); }
  const digest = hash.digest('hex');
  if (digest !== pins.binarySHA256) throw new Error('unsupported_binary');
  return { descriptor: describe(), binarySHA256: digest, phase: 'read-only-binary-integrity',
    capability: 'synthetic-runtime-proof-required', liveBlockers: [
      'Accepted synthetic authority still needs deployed writer-fence and original-work conformance',
      'Exact selected named route, model/settings and every fallback need compatible live evidence',
      'Known-shape Astra/Terra/Opus xhigh preference is not an eligible route; no effort admitted by this profile',
    ] };
}
export interface RunRequest {
  schema: 1; binding: Binding; profile: typeof OPENCODE_PROFILE | typeof OPENCODE_ROUTER_PROFILE; approvedPolicy?: RouterPolicy;
  baseSHA: string; brief: string; settings: Record<string, never>; approval: 'allow' | 'ask';
  limits: { wallMs: number; outputBytes: number }; token: string;
}
export interface NativeEvent { schema: 1; adapter: 'opencode-m0-v1'; nativeProtocol: string; sequence: number; type: string; native: any; raw: string }
export type HarnessOutcome = 'completed_candidate' | 'approval_blocked' | 'tool_failed' | 'harness_failed' | 'invalid_events' | 'artifact_mismatch' | 'cancelled_unknown';
export interface RunResult { outcome: HarnessOutcome; exitCode: number | null; signal: string | null; events: NativeEvent[]; stderr: string; usage: 'unverified_native_observation'; localProcessExited: boolean; upstreamQuiescence: 'unknown'; reason?: string }
const REJECTION = 'The user rejected permission to use this specific tool call.';
export class NativeEvents {
  private decoder = new TextDecoder('utf-8', { fatal: true });
  private pending = ''; private bytes = 0; private session: string | undefined;
  readonly values: NativeEvent[] = [];
  private readonly limit: number;
  constructor(limit = 262144) { this.limit = limit; }
  push(chunk: Uint8Array) {
    this.bytes += chunk.length; if (this.bytes > this.limit) throw new Error('event_limit');
    this.pending += this.decoder.decode(chunk, { stream: true });
    let i: number;
    while ((i = this.pending.indexOf('\n')) >= 0) {
      const raw = this.pending.slice(0, i); this.pending = this.pending.slice(i + 1);
      if (!raw) throw new Error('invalid_native_event');
      const native = parseJSON(raw); object(native);
      if (!['step_start', 'tool_use', 'step_finish', 'text', 'reasoning', 'error'].includes(native.type) || !Number.isSafeInteger(native.timestamp)) throw new Error('unsupported_native_event');
      if (typeof native.sessionID !== 'string' || !/^ses_[a-zA-Z0-9]{1,64}$/.test(native.sessionID) || this.session && this.session !== native.sessionID) throw new Error('invalid_session');
      this.session = native.sessionID;
      if (native.type === 'error') object(native.error);
      else { object(native.part); if (native.part.sessionID !== this.session) throw new Error('invalid_part_session'); }
      if (native.type === 'tool_use' && (!['edit', 'write'].includes(native.part.tool) || !['completed', 'error'].includes(native.part.state?.status))) throw new Error('invalid_tool_event');
      if (native.type === 'step_finish' && !['stop', 'tool-calls', 'unknown'].includes(native.part.reason)) throw new Error('invalid_finish');
      this.values.push({ schema: 1, adapter: 'opencode-m0-v1', nativeProtocol: pins.nativeProtocol, sequence: this.values.length + 1, type: native.type, native, raw });
    }
  }
  end() { this.pending += this.decoder.decode(); if (this.pending) throw new Error('truncated_native_event'); }
}
export function classify(events: NativeEvent[], code: number | null, signal: string | null, cancelled: boolean, invalid: boolean, artifactMatches: boolean): HarnessOutcome {
  if (cancelled) return 'cancelled_unknown';
  if (invalid) return 'invalid_events';
  if (events.some(e => e.type === 'tool_use' && e.native.part.state.status === 'error' && e.native.part.state.error === REJECTION)) return 'approval_blocked';
  if (events.some(e => e.type === 'tool_use' && e.native.part.state.status === 'error')) return 'tool_failed';
  if (signal || code !== 0 || events.some(e => e.type === 'error')) return 'harness_failed';
  if (!events.some(e => e.type === 'tool_use' && e.native.part.state.status === 'completed') || events.at(-1)?.type !== 'step_finish' || events.at(-1)?.native.part.reason !== 'stop' || !artifactMatches) return 'artifact_mismatch';
  return 'completed_candidate';
}
export function environment() {
  const env: Record<string, string> = { PATH: '/usr/local/bin:/usr/bin:/bin', HOME: '/work/home',
    XDG_CONFIG_HOME: '/work/home/config', XDG_DATA_HOME: '/work/home/data', XDG_CACHE_HOME: '/work/home/cache', XDG_STATE_HOME: '/work/home/state',
    OPENCODE_CONFIG: '/work/config.json', OPENCODE_EXPERIMENTAL_OUTPUT_TOKEN_MAX: '128', OPENCODE_EXPERIMENTAL_NATIVE_LLM: 'false' };
  for (const k of ['DISABLE_PROJECT_CONFIG', 'PURE', 'DISABLE_DEFAULT_PLUGINS', 'DISABLE_EXTERNAL_SKILLS', 'DISABLE_CLAUDE_CODE', 'DISABLE_LSP_DOWNLOAD', 'DISABLE_MODELS_FETCH', 'DISABLE_AUTOUPDATE', 'DISABLE_AUTOCOMPACT', 'EXPERIMENTAL_DISABLE_FILEWATCHER']) env['OPENCODE_' + k] = '1';
  return env;
}
export function config(token: string, approval: 'allow' | 'ask', policy?: RouterPolicy) {
  if (policy && validateRouterPolicy(policy).profile !== OPENCODE_ROUTER_PROFILE) throw new Error('unsupported_profile');
  const model = policy?.routerModel ?? 'gaffer-coding';
  return { enabled_providers: ['scoped'], model: 'scoped/' + model, small_model: 'scoped/' + model, share: 'disabled', autoupdate: false, snapshot: false, formatter: false, lsp: false, mcp: {}, plugin: [], instructions: [], permission: { '*': 'deny', edit: approval },
    provider: { scoped: { npm: '@ai-sdk/openai-compatible', name: 'Scoped synthetic route', options: { ...(policy ? { includeUsage: false } : {}), baseURL: 'http://127.0.0.1:8787/v1', apiKey: token }, models: { [model]: { name: 'Synthetic bounded coding', tool_call: true, limit: { context: 8192, output: 128 }, modalities: { input: ['text'], output: ['text'] } } } } }, experimental: { openTelemetry: false } };
}
export function validateWorkspaceInventory(files: string[], hooks: string[]) {
  if (!files.includes('greeting.txt') || files.some(name => !['.git', 'greeting.txt', 'AGENTS.md'].includes(name)) || hooks.some(name => !name.endsWith('.sample'))) throw new Error('ambient_repository_configuration');
}
export function assertFixtureRepository() {
  validateWorkspaceInventory(readdirSync('/work/repo'), readdirSync('/work/repo/.git/hooks'));
  for (const name of readdirSync('/work/repo')) {
    const state = lstatSync('/work/repo/' + name);
    if (state.isSymbolicLink() || (name === '.git' ? !state.isDirectory() : !state.isFile() || state.size > 4096)) throw new Error('unsupported_workspace_entry');
  }
}
export interface RunHandle { events(after?: number): NativeEvent[]; done: Promise<RunResult>; cancel(): Promise<{ localProcessExited: boolean; upstreamQuiescence: 'unknown'; outcome: 'cancelled_unknown' }> }
export function start(request: RunRequest): RunHandle {
  keys(request, ['schema', 'binding', 'profile', 'approvedPolicy', 'baseSHA', 'brief', 'settings', 'approval', 'limits', 'token'], ['schema', 'binding', 'profile', 'baseSHA', 'brief', 'settings', 'approval', 'limits', 'token']);
  validateBinding(request.binding); keys(request.settings, []); keys(request.limits, ['wallMs', 'outputBytes'], ['wallMs', 'outputBytes']);
  const p = request.approvedPolicy && validateRouterPolicy(request.approvedPolicy);
  const router = request.profile === OPENCODE_ROUTER_PROFILE;
  if (router ? !p || p.profile !== request.profile || request.binding.routerId !== p.routerId || request.binding.routeId !== p.routeId || request.binding.revision !== p.revision || request.binding.epoch !== p.epoch || p.limits.outputTokens !== 128 : p !== undefined) throw new Error('unsupported_binding');
  if (request.schema !== 1 || !router && (request.profile !== OPENCODE_PROFILE || request.binding.routerId !== 'fixture_router' || request.binding.routeId !== 'coding') || request.binding.role !== 'worker' || !['allow', 'ask'].includes(request.approval) || !/^[a-f0-9]{40}$/.test(request.baseSHA) || typeof request.brief !== 'string' || Buffer.byteLength(request.brief) > 4096 || !/^[a-f0-9]{64}$/.test(request.token)) throw new Error('unsupported_request');
  if (!Number.isInteger(request.limits.wallMs) || request.limits.wallMs < 1 || request.limits.wallMs > 30000 || !Number.isInteger(request.limits.outputBytes) || request.limits.outputBytes < 1 || request.limits.outputBytes > 262144) throw new Error('unsupported_limits');
  // The outer Docker launcher verifies topology; this refuses accidental host execution.
  if (process.platform !== 'linux' || !readFileSync('/proc/self/status', 'utf8').includes('NoNewPrivs:\t1') || !readFileSync('/proc/self/mountinfo', 'utf8').includes('/router')) throw new Error('unverified_execution_environment');
  assertFixtureRepository();
  probe('/fixture/opencode');
  if (execFileSync('git', ['-C', '/work/repo', 'rev-parse', 'HEAD'], { encoding: 'utf8' }).trim() !== request.baseSHA) throw new Error('base_mismatch');
  writeFileSync('/work/config.json', JSON.stringify(config(request.token, request.approval, p || undefined)), { mode: 0o600 });
  const parsed = new NativeEvents(request.limits.outputBytes); let invalid = false, reason: string | undefined, cancelled = false, stderr = '', stderrBytes = 0;
  let child: ChildProcess; let kill: NodeJS.Timeout | undefined;
  const terminate = () => { child.kill('SIGTERM'); kill ??= setTimeout(() => child.kill('SIGKILL'), 1000); };
  child = spawn('/fixture/opencode', ['run', '--format', 'json', '--model', 'scoped/' + (p?.routerModel ?? 'gaffer-coding'), '--title', 'Bounded synthetic fixture', request.brief], { cwd: '/work/repo', env: environment(), stdio: ['ignore', 'pipe', 'pipe'] });
  const deadline = setTimeout(() => { cancelled = true; reason = 'wall_deadline'; terminate(); }, request.limits.wallMs);
  child.stdout!.on('data', chunk => { try { parsed.push(chunk); } catch (error: any) { invalid = true; reason = error.message; terminate(); } });
  child.stderr!.on('data', chunk => { stderrBytes += chunk.length; if (stderrBytes > request.limits.outputBytes) { invalid = true; reason = 'stderr_limit'; terminate(); } else stderr += chunk.toString(); });
  const done = new Promise<RunResult>(resolve => {
    child.once('error', () => { invalid = true; reason = 'spawn_failed'; });
    child.once('close', (code, signal) => {
      clearTimeout(deadline); if (kill) clearTimeout(kill);
      try { parsed.end(); } catch (error: any) { invalid = true; reason = error.message; }
      let matches = false;
      try {
        matches = readFileSync('/work/repo/greeting.txt', 'utf8') === 'hello from harness\n' &&
          execFileSync('git', ['-C', '/work/repo', 'status', '--porcelain'], { encoding: 'utf8' }).trim() === 'M greeting.txt';
      } catch { reason = 'artifact_unreadable'; }
      resolve({ outcome: classify(parsed.values, code, signal, cancelled, invalid, matches), exitCode: code, signal, events: parsed.values, stderr, usage: 'unverified_native_observation', localProcessExited: true, upstreamQuiescence: 'unknown', ...(reason ? { reason } : {}) });
    });
  });
  return { events(after = 0) { if (!Number.isInteger(after) || after < 0) throw new Error('invalid_cursor'); return structuredClone(parsed.values.filter(e => e.sequence > after)); }, done,
    async cancel() { cancelled = true; reason = 'cancel_requested'; terminate(); const result = await done; return { localProcessExited: result.localProcessExited, upstreamQuiescence: 'unknown', outcome: 'cancelled_unknown' }; } };
}
