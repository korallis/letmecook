import assert from 'node:assert/strict';
import { test } from 'node:test';
import { spawnSync } from 'node:child_process';
import { mkdir, mkdtemp, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { readEvidence, readSnapshot } from '../artifacts/index.ts';
import { checkFixture } from './fixture.ts';

test('emitted synthetic pin check enforces both owned output files, including comment-only attacks', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'baseline-pin-bytes-')), store = { directory };
  try {
    const fixture = await checkFixture(store), base = await readSnapshot(store, fixture.base), candidate = await readSnapshot(store, fixture.candidate);
    const inputs: any = await readEvidence(store, fixture.job.inputs), paths = fixture.registration.paths.write as string[];
    for (const [prefix, files] of [['repo', base.bytes.files], ['inputs', inputs.files]] as const) for (const file of files) {
      const path = join(directory, prefix, file.path); await mkdir(dirname(path), { recursive: true });
      await writeFile(path, Buffer.from(file.contentBase64, 'base64'));
    }
    const pins = JSON.parse(Buffer.from(inputs.files.find((f: any) => f.path === 'pins.json').contentBase64, 'base64').toString()).files;
    const runner = `import fs from 'node:fs';
const read=fs.readFileSync;fs.readFileSync=(path,...args)=>read(typeof path==='string'&&(path.startsWith('/repo/')||path.startsWith('/inputs/'))?process.argv[1]+path:path,...args);
await import(process.argv[2]);`;
    async function check(files: typeof base.bytes.files, errors: string[]) {
      for (const file of files) await writeFile(join(directory, 'repo', file.path), Buffer.from(file.contentBase64, 'base64'));
      const result = spawnSync(process.execPath, ['--input-type=module', '-e', runner, directory, pathToFileURL(join(directory, 'repo/checks/pins.mjs')).href], { encoding: 'utf8', timeout: 5000 });
      assert.ifError(result.error); assert.equal(result.signal, null); assert.equal(result.stderr, '');
      assert.equal(result.status, errors.length ? 1 : 0);
      assert.deepEqual(JSON.parse(result.stdout), { kind: 'synthetic-pins-check', checked: paths, errors });
    }
    await check(base.bytes.files, paths);
    await check(candidate.bytes.files, []);
    for (const attacked of [[paths[0]], [paths[1]], paths]) {
      const files = candidate.bytes.files.map(file => {
        if (!attacked.includes(file.path)) return file;
        const original = base.bytes.files.find(f => f.path === file.path)!;
        const bytes = Buffer.concat([Buffer.from(original.contentBase64, 'base64'), Buffer.from('# uses: ' + pins[file.path] + '\n')]);
        return { ...file, contentBase64: bytes.toString('base64') };
      });
      await check(files, attacked);
    }
    for (const path of paths) await check(candidate.bytes.files.map(file => file.path === path
      ? { ...file, contentBase64: Buffer.concat([Buffer.from(file.contentBase64, 'base64'), Buffer.from([0xff])]).toString('base64') } : file), [path]);
  } finally { await rm(directory, { recursive: true, force: true }); }
});
