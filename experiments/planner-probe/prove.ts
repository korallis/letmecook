import { run } from 'node:test';
import { readFile, readdir, writeFile, rename, mkdir } from 'node:fs/promises';
import { dirname, join, resolve, relative } from 'node:path';
import { fileURLToPath } from 'node:url';
import { sha256 } from './reader.ts';
import { demonstrate } from './demo.ts';
import { livePreflight } from './capabilities.ts';
import { DEFAULT_BUDGET } from './planner.ts';

const directory = dirname(fileURLToPath(import.meta.url));
const root = resolve(directory, '../..');
const output = resolve(process.argv[2] ?? join(root, 'docs/evidence/planner-probe-run.json'));
const evidence = {
  schema: 1, observedAt: new Date().toISOString(), result: 'blocked', synthetic: true, liveRouterCalled: false, issueComplete: false, unattendedSupported: false,
  command: 'npm --prefix experiments/planner-probe run prove', runtime: { node: process.version, platform: process.platform, architecture: process.arch },
  budgets: DEFAULT_BUDGET, sourceDigests: {} as Record<string, string>, tests: [] as { name: string; result: string }[],
  integration: null as Awaited<ReturnType<typeof demonstrate>> | null, live: livePreflight(),
};
async function write() {
  await mkdir(dirname(output), { recursive: true });
  await writeFile(output + '.next', JSON.stringify(evidence, null, 2) + '\n', { mode: 0o600 });
  await rename(output + '.next', output);
}
await write();
for (const folder of [directory, join(root, 'experiments/inference-boundary'), join(root, 'tests/fixtures/planner')]) {
  for (const name of (await readdir(folder)).sort()) {
    if (!/\.(ts|json|txt)$/.test(name)) continue;
    const path = join(folder, name); evidence.sourceDigests[relative(root, path)] = sha256(await readFile(path));
  }
}
let success = false;
for await (const event of run({ files: [join(directory, 'planner.test.ts')], timeout: 30000 })) {
  if (event.type === 'test:pass' || event.type === 'test:fail') evidence.tests.push({ name: event.data.name, result: event.type === 'test:pass' ? 'passed' : 'failed' });
  if (event.type === 'test:summary') success = event.data.success;
  // Do not retain raw assertion errors, request bodies, stderr or credentials.
}
if (success && evidence.tests.length && evidence.tests.every(test => test.result === 'passed')) {
  evidence.integration = await demonstrate(); evidence.result = 'synthetic_planner_passed_live_blocked';
}
await write();
console.log(JSON.stringify({ result: evidence.result, tests: evidence.tests.length, failedTests: evidence.tests.filter(test => test.result === 'failed').map(test => test.name), runtime: evidence.runtime, evidence: output, issueComplete: false, liveRouterCalled: false }));
if (evidence.result === 'blocked') process.exitCode = 1;
