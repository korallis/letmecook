import { createHash } from 'node:crypto';
import { readFileSync, openSync, readSync, closeSync } from 'node:fs';
import { canonical, keys, object, parseJSON } from '../../inference-boundary/json.ts';
import { settings, environment as capturedEnvironment } from '../../native-evaluation/opencode-settings.mjs';

export const PROFILE = 'opencode-1.18.30-responses-apply-patch-v1';
export const ADAPTER = 'opencode-native-m0-v1';
export const ISOLATION = 'native-local-worker768-gateway768-uds-persistent-v1';
export const pins = JSON.parse(readFileSync(new URL('../pins.json', import.meta.url), 'utf8'));
export const digest = (value: unknown) => createHash('sha256').update(canonical(value)).digest('hex');
export const describe = () => ({ schema: 1, adapter: ADAPTER, harness: pins.version, nativeProtocol: pins.nativeProtocol,
  profile: PROFILE, boundaryProfile: 'router-native-responses-local-v1', protocol: 'responses',
  provider: 'openai', sdk: '@ai-sdk/openai', model: 'gpt-6-astra', reasoning: { effort: 'xhigh', summary: 'auto' },
  store: false, providerOutputTokens: 'unavailable', providerMonetaryCap: 'unavailable',
  tools: ['apply_patch'], structuredEvents: true, tokenStreamingEvents: false, resume: false,
  approvals: 'CLI rejects asks; blocked outcome; no interactive approval or auto-approve',
  builtins: 'enabled', externalPlugins: 'disabled plus repository admission', oauth: false, nativeLLM: false, websockets: false,
  isolation: ISOLATION, workerMemoryMiB: 768, gatewayMemoryMiB: 768,
  usage: 'unverified native observation; not provider billing', liveReadiness: 'pending independent integrated evidence' });

export function config(token: string, approval: 'allow' | 'ask') {
  const value = settings(token); value.permission.edit = approval; return value;
}
export const environment = () => structuredClone(capturedEnvironment) as Record<string, string>;
export const settingsDigest = (approval: 'allow' | 'ask') => digest({ config: config('<boundary-grant>', approval), environment: environment() });
export function probe(binary: string) {
  const hash = createHash('sha256'), fd = openSync(binary, 'r'), buffer = Buffer.alloc(1048576);
  try { let count: number; while ((count = readSync(fd, buffer)) > 0) hash.update(buffer.subarray(0, count)); } finally { closeSync(fd); }
  const binarySHA256 = hash.digest('hex'); if (binarySHA256 !== pins.binarySHA256) throw Error('unsupported_binary');
  return { descriptor: describe(), binarySHA256, phase: 'read-only-binary-integrity', capability: 'integrated-runtime-proof-required' };
}

export interface NativeEvent { schema: 1; adapter: typeof ADAPTER; nativeProtocol: string; sequence: number; type: string; native: any; raw: string }
export type Outcome = 'completed_candidate' | 'approval_blocked' | 'tool_failed' | 'harness_failed' | 'invalid_events' | 'artifact_mismatch' | 'cancelled_unknown';
const REJECTION = 'The user rejected permission to use this specific tool call.';
export class NativeEvents {
  private decoder = new TextDecoder('utf-8', { fatal: true });
  private pending = ''; private bytes = 0; private session: string | undefined;
  private calls = new Set<string>();
  readonly values: NativeEvent[] = [];
  private limit: number;
  constructor(limit = 262144) { this.limit = limit; }
  push(chunk: Uint8Array) {
    this.bytes += chunk.length; if (this.bytes > this.limit) throw Error('event_limit');
    this.pending += this.decoder.decode(chunk, { stream: true });
    let index: number;
    while ((index = this.pending.indexOf('\n')) >= 0) {
      const raw = this.pending.slice(0, index); this.pending = this.pending.slice(index + 1);
      if (!raw) throw Error('invalid_native_event');
      const native = parseJSON(raw); object(native);
      if (!['step_start', 'tool_use', 'step_finish', 'text', 'reasoning', 'error'].includes(native.type) || !Number.isSafeInteger(native.timestamp)) throw Error('unsupported_native_event');
      if (typeof native.sessionID !== 'string' || !/^ses_[a-zA-Z0-9]{1,64}$/.test(native.sessionID) || this.session && native.sessionID !== this.session) throw Error('invalid_session');
      this.session = native.sessionID;
      if (native.type === 'error') object(native.error);
      else { object(native.part); if (native.part.sessionID !== this.session) throw Error('invalid_part_session'); }
      if (native.type === 'tool_use') {
        const part = native.part;
        if (part.tool !== 'apply_patch' || !['completed', 'error'].includes(part.state?.status) || typeof part.callID !== 'string' || !/^[a-zA-Z0-9_-]{1,128}$/.test(part.callID) || this.calls.has(part.callID)) throw Error('invalid_tool_event');
        keys(part.state.input, ['patchText'], ['patchText']);
        if (typeof part.state.input.patchText !== 'string' || Buffer.byteLength(part.state.input.patchText) > 65536 || (part.state.status === 'completed' ? typeof part.state.output !== 'string' : typeof part.state.error !== 'string')) throw Error('invalid_tool_event');
        this.calls.add(part.callID);
      }
      if (native.type === 'step_finish' && !['stop', 'tool-calls', 'unknown'].includes(native.part.reason)) throw Error('invalid_finish');
      if (native.type === 'text' && typeof native.part.text !== 'string') throw Error('invalid_text');
      this.values.push({ schema: 1, adapter: ADAPTER, nativeProtocol: pins.nativeProtocol, sequence: this.values.length + 1, type: native.type, native, raw });
    }
  }
  end() { this.pending += this.decoder.decode(); if (this.pending) throw Error('truncated_native_event'); }
}
export function classify(events: NativeEvent[], code: number | null, signal: string | null, cancelled: boolean, invalid: boolean, artifactMatches: boolean): Outcome {
  if (cancelled) return 'cancelled_unknown';
  if (invalid) return 'invalid_events';
  if (events.some(e => e.type === 'tool_use' && e.native.part.state.status === 'error' && e.native.part.state.error === REJECTION)) return 'approval_blocked';
  if (events.some(e => e.type === 'tool_use' && e.native.part.state.status === 'error')) return 'tool_failed';
  if (signal || code !== 0 || events.some(e => e.type === 'error')) return 'harness_failed';
  if (!artifactMatches || !events.some(e => e.type === 'tool_use' && e.native.part.state.status === 'completed') || events.at(-1)?.type !== 'step_finish' || events.at(-1)?.native.part.reason !== 'stop') return 'artifact_mismatch';
  return 'completed_candidate';
}
