import { run } from 'node:test';
import { mkdir, open, readFile, readdir, rename } from 'node:fs/promises';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createHash } from 'node:crypto';

const directory = dirname(fileURLToPath(import.meta.url));
const output = resolve(process.argv[2] ?? join(directory, '../../docs/evidence/inference-boundary-run.json'));
const evidence = { schema: 1, observedAt: new Date().toISOString(), result: 'blocked', synthetic: true, liveRouterCalled: false, unattendedSupported: false,
  profile: 'chat-text-tools-v1', transport: 'task-owned Unix-domain HTTP sockets', command: 'npm --prefix experiments/inference-boundary run prove',
  runtime: { node: process.version, platform: process.platform, architecture: process.arch },
  sourceDigests: {} as Record<string, string>, tests: [] as { name: string; result: 'passed' | 'failed' }[],
  limitations: ['No live 9Router, provider or harness conformance; only the synthetic authority proves fixture quiescence.', 'No inference credential, provider credential, caller token, raw response/error or private origin is written to this report.', 'Chat Completions text/tools with max_completion_tokens only. Other protocols/features need a separate tested profile.'] };
async function write() {
  await mkdir(dirname(output), { recursive: true });
  const file = await open(output + '.next', 'w', 0o600);
  try { await file.writeFile(JSON.stringify(evidence, null, 2) + '\n'); await file.sync(); } finally { await file.close(); }
  await rename(output + '.next', output);
  const dir = await open(dirname(output), 'r'); try { await dir.sync(); } finally { await dir.close(); }
}
await write();
const files = (await readdir(directory)).filter(name => name.endsWith('.ts') || ['package.json', 'package-lock.json', 'tsconfig.json'].includes(name));
for (const name of files.sort()) evidence.sourceDigests[name] = createHash('sha256').update(await readFile(join(directory, name))).digest('hex');
const stream = run({ files: [join(directory, 'boundary.test.ts')], timeout: 30000 });
let success = false;
for await (const event of stream) {
  if (event.type === 'test:pass' || event.type === 'test:fail') evidence.tests.push({ name: event.data.name, result: event.type === 'test:pass' ? 'passed' : 'failed' });
  if (event.type === 'test:summary') success = event.data.success;
  // Raw assertion errors/stdout/stderr are deliberately excluded from the evidence artifact.
}
evidence.result = success && evidence.tests.length > 0 && evidence.tests.every(t => t.result === 'passed') ? 'synthetic-boundary-passed' : 'blocked';
await write();
console.log(JSON.stringify({ result: evidence.result, passed: evidence.tests.filter(t => t.result === 'passed').length, evidence: output, liveRouterCalled: false }));
if (evidence.result === 'blocked') process.exitCode = 1;
