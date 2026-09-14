import { assertSuiteRequest } from '../router-authority-extension/overlay/initial-suite.mjs';
import { spawn } from 'node:child_process';
import { writeFileSync, existsSync } from 'node:fs';
import { keys } from '../inference-boundary/json.ts';
import { config, environment, settingsDigest, probe, NativeEvents, type NativeEvent } from '../harness/native/client.ts';
import { readArtifact, assertExecutionEnvironment, type Artifact } from '../harness/native/run.ts';
import { PROMPT, EXPECTED, FIXTURE } from './constants.ts';
export interface Request { schema: 1|2;timing?:'initial-suite-v1';packetDigest?:string;profileDigest?:string;sessionDeadline?:number; fixture: typeof FIXTURE; baseSHA: string; settingsDigest: string; bindingDigest: string; token: string; wallMs: number; outputBytes: number }
export function validateRequest(r: Request) {
  const extra=r.schema===2?['timing','packetDigest','profileDigest','sessionDeadline']:[];assertSuiteRequest(r,'continuity');
  keys(r, ['schema','fixture','baseSHA','settingsDigest','bindingDigest','token','wallMs','outputBytes',...extra], ['schema','fixture','baseSHA','settingsDigest','bindingDigest','token','wallMs','outputBytes',...extra]);
  if (![1,2].includes(r.schema) || r.fixture !== FIXTURE || !/^[a-f0-9]{40}$/.test(r.baseSHA) || ![r.settingsDigest,r.bindingDigest,r.token].every(x => typeof x === 'string' && /^[a-f0-9]{64}$/.test(x)) || r.settingsDigest !== settingsDigest('ask') || !Number.isSafeInteger(r.wallMs) || r.wallMs < 1 || r.wallMs > (r.schema===2?90000:30000) || !Number.isSafeInteger(r.outputBytes) || r.outputBytes < 1 || r.outputBytes > 262144) throw Error('continuity_request');
}
export function unchanged(a: Artifact | null, base: string) { return !!a && a.baseSHA === base && a.headSHA === base && a.path === 'greeting.txt' && a.content === 'hello\n' && !a.changedFiles.length && !a.status && !a.diff; }
export function classify(events: NativeEvent[], code: number | null, signal: string | null, invalid: boolean, cancelled: boolean, artifact: Artifact | null, base: string) {
  if (cancelled) return 'cancelled_unknown';
  if (invalid) return 'invalid_events';
  if (events.some(e => e.type === 'tool_use')) return 'continuity_tool_refused';
  if (signal || code !== 0 || events.some(e => e.type === 'error')) return 'harness_failed';
  if (!unchanged(artifact,base) || events.filter(e => e.type === 'text').map(e => e.native.part.text).join('') !== EXPECTED || events.at(-1)?.type !== 'step_finish' || events.at(-1)?.native.part.reason !== 'stop') return 'continuity_mismatch';
  return 'continuity_transport_completed';
}
export function start(input: Request) {
  const r = structuredClone(input); validateRequest(r); assertExecutionEnvironment(); probe('/fixture/opencode');
  if (!unchanged(readArtifact(r.baseSHA),r.baseSHA)) throw Error('continuity_base');
  for (const path of ['/control','/state','/private','/egress','/router-source','/probe','/var/run/docker.sock','/Users','/work/auth.json','/work/config/opencode/auth.json','/work/data/opencode/auth.json']) if (existsSync(path)) throw Error('continuity_ambient');
  writeFileSync('/work/config.json',JSON.stringify(config(r.token,'ask')),{mode:0o600});
  const parser = new NativeEvents(r.outputBytes); let cancelled = false, invalid = false, stderr = '', bytes = 0, kill: NodeJS.Timeout | undefined;
  if(r.schema===2&&Date.now()>=r.sessionDeadline!)throw Error('suite_session_expired');
  const child = spawn('/fixture/opencode',['run','--format','json','--model','openai/gpt-6-astra','--title','Fixed continuity fixture',PROMPT],{cwd:'/work/repo',env:environment(),stdio:['ignore','pipe','pipe']});
  const terminate = () => { if (child.exitCode === null && child.signalCode === null) { child.kill('SIGTERM'); kill ??= setTimeout(() => child.kill('SIGKILL'),1000); } };
  const timer = setTimeout(() => { cancelled = true; terminate(); },Math.max(0,Math.min(r.wallMs,r.sessionDeadline===undefined?Infinity:r.sessionDeadline-Date.now())));
  child.stdout.on('data',chunk => { try { parser.push(chunk); } catch { invalid = true; terminate(); } });
  child.stderr.on('data',chunk => { bytes += chunk.length; if (bytes > r.outputBytes) { invalid = true; terminate(); } else stderr += chunk.toString(); });
  const done = new Promise<any>(resolve => {
    child.once('error',() => { invalid = true; });
    child.once('close',(exitCode,signal) => {
      if(r.schema===2&&Date.now()>=r.sessionDeadline!)cancelled=true;
      clearTimeout(timer); if (kill) clearTimeout(kill);
      try { parser.end(); } catch { invalid = true; }
      let artifact: Artifact | null = null; try { artifact = readArtifact(r.baseSHA); } catch {}
      resolve({outcome:classify(parser.values,exitCode,signal,invalid,cancelled,artifact,r.baseSHA),exitCode,signal,events:parser.values,stderr,artifact,settingsDigest:r.settingsDigest,bindingDigest:r.bindingDigest,localProcessExited:true,upstreamQuiescence:'unknown'});
    });
  });
  return {done,async cancel() { cancelled = true; terminate(); await done; }};
}
