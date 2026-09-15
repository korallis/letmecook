// Isolated consumer only. The supervisor freezes and captures the complete tree.
import assert from 'node:assert/strict';
import { mkdirSync, writeFileSync, existsSync, readSync } from 'node:fs';
import { dirname } from 'node:path';
import { spawn } from 'node:child_process';
import { setTimeout as delay } from 'node:timers/promises';
import { config, environment, probe, NativeEvents } from '../../harness/native/client.ts';
import { assertExecutionEnvironment } from '../../harness/native/run.ts';
import { relay } from '../../harness/native/relay.ts';
import { validateWorkerInput, type WorkerInput } from './input.ts';

const chunks: Buffer[] = []; let size = 0;
const chunk = Buffer.alloc(16384);
let count: number; while ((count = readSync(0, chunk)) > 0) { size += count; assert(size <= 262144, 'worker_input_limit'); chunks.push(Buffer.from(chunk.subarray(0, count))); }
const input: WorkerInput = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(Buffer.concat(chunks)));
const invocationDigest = validateWorkerInput(input); assertExecutionEnvironment(); probe('/fixture/opencode');
for (const path of ['/work/repo','/state','/control','/private','/egress','/router-source','/var/run/docker.sock','/root/.codex/auth.json','/Users']) assert(!existsSync(path), 'ambient_worker_state');
mkdirSync('/work/repo');
for (const file of input.files) { const path = '/work/repo/' + file.path; mkdirSync(dirname(path), { recursive: true }); writeFileSync(path, Buffer.from(file.contentBase64, 'base64'), { flag: 'wx', mode: file.mode }); }
writeFileSync('/work/config.json', JSON.stringify(config(input.token, 'allow')), { mode: 0o600, flag: 'wx' });
const transport = await relay(input.token, 65536), parsed = new NativeEvents(input.outputBytes), startedAt = Date.now();
let stderr = '', stderrBytes = 0, reason: string | null = null, invalid = false, childEnded = false, stopRequested = false;
assert(startedAt < input.deadline, 'worker_deadline_before_spawn');
const child = spawn('/fixture/opencode', ['run','--format','json','--model','openai/gpt-6-astra','--title','Public synthetic action pins',input.prompt], { cwd: '/work/repo', env: environment(), stdio: ['ignore','pipe','pipe'] });
let hardStop: NodeJS.Timeout | undefined;
const stop = () => { stopRequested = true; reason ??= 'cancelled'; if (childEnded) process.exit(0); child.kill('SIGTERM'); hardStop ??= setTimeout(() => child.kill('SIGKILL'), 1000); };
for (const signal of ['SIGTERM', 'SIGINT'] as const) process.on(signal, stop);
const deadline = setTimeout(() => { reason = 'worker_deadline'; stop(); }, Math.max(0, input.deadline - Date.now()));
child.stdout.on('data', bytes => { try { parsed.push(bytes); } catch { invalid = true; reason = 'invalid_events'; stop(); } });
child.stderr.on('data', bytes => { stderrBytes += bytes.length; if (stderrBytes > input.outputBytes) { invalid = true; reason = 'stderr_limit'; stop(); } else stderr += bytes; });
child.on('error', () => { reason = 'worker_spawn_failed'; });
const result = await new Promise<{exitCode: number | null; signal: string | null}>(resolve => child.once('close', (exitCode, signal) => resolve({ exitCode, signal })));
childEnded = true; clearTimeout(deadline); if (hardStop) clearTimeout(hardStop);
try { parsed.end(); } catch { invalid = true; reason = 'incomplete_events'; }
await transport.close();
const observation = { event: 'baseline_worker_observation', invocationDigest, registrationDigest: input.registrationDigest, packetDigest: input.packetDigest,
  bindingDigest: input.bindingDigest, settingsDigest: input.settingsDigest, startedAt, endedAt: Date.now(), ...result, invalid, reason,
  events: parsed.values, stderr, requests: transport.observations, rejected: transport.rejected, localProcessExited: true, upstreamQuiescence: 'unknown' };
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify(observation) + '\n', error => error ? reject(error) : resolve()));
if (!stopRequested) await delay(60000); // Capture window; the supervisor retains bytes before stop.
