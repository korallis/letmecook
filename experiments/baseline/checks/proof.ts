// Retain actual public-synthetic proof evidence in a new private directory.
import assert from 'node:assert/strict';
import { mkdir } from 'node:fs/promises';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { retainEvidence } from '../artifacts/index.ts';
import { checkFixture, AUDIT_CHECK } from './fixture.ts';
import { measureRuntime, runCheck } from './index.ts';

export async function proof(directory: string) {
  await mkdir(directory, { mode: 0o700 }); const store = { directory }, signal = new AbortController().signal;
  const runtime = await measureRuntime(store, signal, Date.now() + 10000), checks = [];
  for (const [code, checkId] of [[undefined, 'pins'], [AUDIT_CHECK, 'audit']] as const) {
    const f = await checkFixture(store, code, { checkId });
    for (const revision of ['base', 'candidate'] as const) {
      const result = await runCheck({ ...f.job, revision, snapshot: f[revision], deadline: Date.now() + 10000 }, store, signal);
      assert.equal(result.status, checkId === 'pins' && revision === 'candidate' ? 'passed' : 'failed'); assert.equal(result.cleanup, true); checks.push({ revision, result });
    }
  }
  return await retainEvidence(store, 'checks-proof', { schema: 1, kind: 'public-synthetic-checks-proof', runtime, checks, actualOperatorCases: 0, realAdvisoryAudit: false, upstreamCI: false });
}
if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  assert.equal(process.argv.length, 3, 'new-private-output-directory-required'); console.log(JSON.stringify(await proof(resolve(process.argv[2]))));
}
