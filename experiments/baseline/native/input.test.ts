import assert from 'node:assert/strict';
import { test } from 'node:test';
import { makeManifest } from '../artifacts/manifest.ts';
import { settingsDigest } from '../../harness/native/client.ts';
import { bytesDigest, validateWorkerInput, type WorkerInput } from './input.ts';

function fixture(): WorkerInput {
  const files = ['ci/first.yml','ci/second.yml'].map(path => ({ path, mode: 0o644 as const, size: 6, sha256: bytesDigest('hello\n'), contentBase64: Buffer.from('hello\n').toString('base64') }));
  const context = '{"public":"fixture"}', prompt = 'Update two pins.\n\nFrozen public context:\n' + context;
  return {schema:1,kind:'synthetic-baseline-worker',caseId:'synthetic-case01',registrationDigest:'a'.repeat(64),packetDigest:'b'.repeat(64),profileDigest:'c'.repeat(64),bindingDigest:'d'.repeat(64),settingsDigest:settingsDigest('allow'),baseTreeDigest:makeManifest(files).treeDigest,context,contextDigest:bytesDigest(context),prompt,promptDigest:bytesDigest(prompt),files,token:'e'.repeat(64),deadline:1000,outputBytes:262144};
}
test('the worker accepts a bounded complete snapshot and excludes the scoped token from its invocation digest', () => {
  const input=fixture(), identity=validateWorkerInput(input,1); input.token='f'.repeat(64);assert.equal(validateWorkerInput(input,1),identity);
});
test('changed base, context, settings, undeclared fields, non-synthetic identity and expired deadlines are refused', () => {
  const mutations: ((value:any)=>void)[]=[
    x=>x.files[0].contentBase64=Buffer.from('other\n').toString('base64'),x=>x.files.push({...x.files[0]}),
    x=>x.files[0].path='../outside',x=>x.files[0].mode=0o777,x=>x.baseTreeDigest='0'.repeat(64),
    x=>x.context='changed',x=>x.prompt='changed',x=>x.contextDigest='0'.repeat(64),
    x=>x.settingsDigest='0'.repeat(64),x=>x.caseId='operator-case',x=>x.schema=2,x=>x.extra=true,
    x=>x.deadline=1,x=>x.deadline=600002,x=>x.outputBytes=262145,
  ];
  for(const mutate of mutations){const input=fixture();mutate(input);assert.throws(()=>validateWorkerInput(input,1));}
});
