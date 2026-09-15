import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { prepareCase } from '../case01.ts';
import { profile } from '../../native-evaluation/fixtures.mjs';
import { digest, settingsDigest } from '../../harness/native/client.ts';
import { baselineManifest, baselineStart, baselineTrace } from './synthetic-control.ts';

test('case admission binds the frozen declaration, base, settings, limits and packet before a single start', async () => {
  const directory=await mkdtemp(join(tmpdir(),'baseline-admission-'));
  try {
    const {registration,descriptor}=await prepareCase({directory},'a'.repeat(40),{}), n=profile();
    n.authorization.caseRef='case01';n.scope={...n.scope,caseRef:'case01',phase:'baseline',authorizationDigest:digest(n.authorization),maxInferenceAttempts:32,elapsedMs:900000};
    n.toolPaths=['ci/first.yml','ci/second.yml'];n.harness.settings=settingsDigest('allow');
    n.local={requestBytes:65536,responseBytes:1048576,concurrency:1,requestCount:32,totalMs:120000,firstOutputMs:90000,idleMs:45000,attemptMs:900000};
    assert.deepEqual(baselineManifest(descriptor,[n]),descriptor);
    for(const mutate of [(x:any)=>x.evidence='reviewed-deployment',(x:any)=>x.harness.settings='0'.repeat(64),(x:any)=>x.local.totalMs=300000,(x:any)=>x.scope.elapsedMs=900001,(x:any)=>x.toolPaths.push('lock.json')]){
      const invalid=structuredClone(n);mutate(invalid);assert.throws(()=>baselineManifest(descriptor,[invalid]));
    }
    for(const mutate of [(x:any)=>x.origin='operator',(x:any)=>x.phases.workerMs++,(x:any)=>x.writePaths=['lock.json'],(x:any)=>x.extra=true]){
      const invalid=structuredClone(descriptor);mutate(invalid);assert.throws(()=>baselineManifest(invalid,[n]));
    }
    registration.execution.packetDigest='b'.repeat(64);registration.execution.nativeProfileDigest=digest(n);baselineTrace.registrationDigest=null;
    for(const mutate of [(x:any)=>x.execution.packetDigest='c'.repeat(64),(x:any)=>x.execution.nativeProfileDigest='d'.repeat(64),(x:any)=>x.fixture.treeDigest='e'.repeat(64),(x:any)=>x.paths.write.push('lock.json'),(x:any)=>x.fixture.context.sha256='f'.repeat(64)]){
      const invalid=structuredClone(registration);mutate(invalid);assert.throws(()=>baselineStart(invalid,descriptor,'b'.repeat(64),{native:n}));assert.equal(baselineTrace.registrationDigest,null);
    }
    assert.equal(baselineStart(registration,descriptor,'b'.repeat(64),{native:n}).registrationDigest,digest(registration));
    assert.throws(()=>baselineStart(registration,descriptor,'b'.repeat(64),{native:n}),/already_started/);
  } finally {baselineTrace.registrationDigest=null;await rm(directory,{recursive:true,force:true});}
});
