import { run } from 'node:test';
import { readFile, writeFile, rename, mkdir } from 'node:fs/promises';
import { dirname, join, resolve, relative } from 'node:path';
import { sha256 } from './reader.ts';
import { NATIVE_PLANNER_BUDGET, NATIVE_PLANNER_PROTOCOL } from './native-transport.ts';
import { setupNative, nativeRead, nativeText } from './native-fixture.ts';
import { proposal } from './public-fixture.ts';

const directory = import.meta.dirname, root = resolve(directory, '../..');
const output = resolve(process.argv[2] ?? join(directory, 'evidence/native-preparation-run.json'));
const evidence = {
  schema: 1, observedAt: new Date().toISOString(), result: 'blocked',
  evidenceKind: 'in_memory_consumer_contract_only', nativeProtocol: NATIVE_PLANNER_PROTOCOL,
  issueComplete: false, liveRouterCalled: false, syntheticRouterCalled: false, physicalInferenceAttempts: 0,
  sharedNativeAdmission: 'blocked_missing_accepted_planner_profile_and_codec', sharedReceiptConformance: 'not_exercised', deploymentConformance: 'not_exercised',
  command: 'npm --prefix experiments/planner-probe run prove:native',
  runtime: { node: process.version, platform: process.platform, architecture: process.arch }, budget: NATIVE_PLANNER_BUDGET,
  sourceDigests: {} as Record<string, string>, tests: [] as { name: string; result: string }[],
  observation: null as Record<string, unknown> | null,
  cleanup: { kernelResourcesCreated: false, providerResourcesCreated: false, fixture: 'in_memory_only' },
};
async function persist() {
  await mkdir(dirname(output), { recursive: true });
  await writeFile(output + '.next', JSON.stringify(evidence, null, 2) + '\n', { mode: 0o600 });
  await rename(output + '.next', output);
}
await persist();
try {
  for (const file of [
    ...['planner.ts', 'transport.ts', 'native-transport.ts', 'native-fixture.ts', 'native-planner.test.ts', 'native-prove.ts', 'reader.ts', 'public-fixture.ts', 'capabilities.ts', 'package.json', 'package-lock.json', 'tsconfig.json', 'tsconfig.native.json'].map(name => join(directory, name)),
    ...['json.ts', 'types.ts', 'protocol.ts', 'router-policy.ts'].map(name => join(root, 'experiments/inference-boundary', name)),
    ...['fixture.txt', 'plan.schema.json'].map(name => join(root, 'tests/fixtures/planner', name)),
  ]) evidence.sourceDigests[relative(root, file)] = sha256(await readFile(file));
  await persist();
  let success = false;
  for await (const event of run({ files: [join(directory, 'native-planner.test.ts')], timeout: 30000 })) {
    if (event.type === 'test:pass' || event.type === 'test:fail') {
      evidence.tests.push({ name: event.data.name, result: event.type === 'test:pass' ? 'passed' : 'failed' });
      await persist();
    }
    if (event.type === 'test:summary') success = event.data.success;
    // Never retain raw model output, assertion payloads, settings identities or stderr.
  }
  if (success && evidence.tests.length && evidence.tests.every(test => test.result === 'passed')) {
    const f = await setupNative([nativeRead(), nativeText(''), body => nativeText(JSON.stringify(proposal(JSON.parse(body.input[1].content).input_revision)))]);
    const result = await f.planner.run();
    evidence.observation = { outcome: result.outcome, authority: result.authority, usage: result.usage, settings: result.settings,
      nativeCallIdentityPreserved: result.completions[0]?.calls[0] === 'native_original_call_1', repeatedRunCached: JSON.stringify(await f.planner.run()) === JSON.stringify(result),
      toolChoices: f.requests.map(request => request.body.tool_choice), providerCapFields: f.requests.flatMap(request => Object.keys(request.body).filter(key => /^max_.*tokens$/.test(key))) };
    if (result.outcome === 'plan_proposed') evidence.result = 'native_consumer_preparation_passed_shared_admission_blocked';
  }
} catch { evidence.result = 'blocked'; }
finally { await persist(); }
console.log(JSON.stringify({ result: evidence.result, tests: evidence.tests.length, failedTests: evidence.tests.filter(test => test.result === 'failed').map(test => test.name), issueComplete: false, liveRouterCalled: false }));
if (evidence.result === 'blocked') process.exitCode = 1;
