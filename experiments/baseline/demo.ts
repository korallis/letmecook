import assert from 'node:assert/strict';
import { resolve } from 'node:path';
import { writeExport } from './collect.ts';
import { fixture } from './fixture.ts';
try {
  assert.equal(process.argv.length, 3);
  const result = await writeExport(fixture(), resolve(process.argv[2]));
  console.log(JSON.stringify({ syntheticOnly: true, actualBaselineCases: result.cohort.registered, syntheticExcluded: result.syntheticExcluded }));
} catch { console.error('Synthetic export failed; use a new output directory inside a private owned directory.'); process.exitCode = 1; }
