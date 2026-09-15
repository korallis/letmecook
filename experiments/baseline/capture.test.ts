import assert from 'node:assert/strict';
import { readFile, mkdtemp, writeFile, rm, unlink } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import { createHash } from 'node:crypto';
import { canonicalJSON } from './artifacts/store.ts';
import { readEvidence, readSnapshot } from './artifacts/index.ts';
import { verifyBaselineTranscript } from './native/transcript.ts';
import { collectDataset } from './collect.ts';
import { validateRetainedRun } from './retained.ts';

test('retained real synthetic captures requalify transcripts, full candidate bytes and honest measurement exports offline', async () => {
  const capture=JSON.parse(await readFile(new URL('../../docs/evidence/baseline-native-case01-run.json',import.meta.url),'utf8'));
  assert.equal(capture.live,false);assert.equal(capture.realBaselineCases,0);assert.equal(capture.cases.length,2);
  for(const example of capture.cases){
    const directory=await mkdtemp(join(tmpdir(),'baseline-requalification-')),store={directory};
    try{
      for(const [name,record]of Object.entries(example.records)){
        assert.match(name,/^[a-z][a-z0-9-]*-[a-f0-9]{64}\.json$/);const bytes=canonicalJSON(record);
        assert(name.endsWith('-'+createHash('sha256').update(bytes).digest('hex')+'.json'));
        await writeFile(join(directory,name),bytes,{flag:'wx',mode:0o600});
      }
      const journal:any=await readEvidence(store,example.journal);assert.equal(journal.result,'passed');assert.equal(journal.cleanup,true);assert.equal(journal.repositoryDestroyed,true);
      await validateRetainedRun(store,journal,'replay');
      const transcript=verifyBaselineTranscript(await readEvidence(store,journal.verificationInput));
      assert.deepEqual(transcript,await readEvidence(store,journal.transcript));assert.equal(transcript.physicalAttempts,example.scenario==='two-requests'?2:3);
      const candidate:any=await readEvidence(store,journal.candidate);
      await readSnapshot(store,candidate.base);await readSnapshot(store,candidate.candidate);
      const measurement=collectDataset(await readEvidence(store,journal.dataset)).publicJSON;
      assert.deepEqual(measurement,journal.export);assert.equal(measurement.cohort.registered,0);assert.equal(measurement.syntheticExcluded,1);
      assert.deepEqual(measurement.rows[0].checks,{passed:1,failed:3,'not-run':0,unknown:0});assert.equal(measurement.rows[0].outputLabel,null);
      assert.equal(measurement.rows[0].trialEffort.totalSeconds,null);assert.equal(measurement.rows[0].physicalFailureCount,0);
    }finally{await rm(directory,{recursive:true,force:true});}
  }
});

test('every retained capture record is required: deletion and substituted bytes reject replay', async () => {
  const capture=JSON.parse(await readFile(new URL('../../docs/evidence/baseline-native-case01-run.json',import.meta.url),'utf8'));
  for(const example of capture.cases) {
    const directory=await mkdtemp(join(tmpdir(),'baseline-incomplete-')),store={directory};
    try {
      for(const [name,record] of Object.entries(example.records)) await writeFile(join(directory,name),canonicalJSON(record),{mode:0o600});
      const replay=async()=>validateRetainedRun(store,await readEvidence(store,example.journal),'replay');
      await replay();
      for(const [name,record] of Object.entries(example.records)) {
        await unlink(join(directory,name));
        await assert.rejects(replay(), /ENOENT/, 'missing '+name);
        await writeFile(join(directory,name),'{}',{mode:0o600});
        await assert.rejects(replay(), /evidence_digest_mismatch/, 'substituted '+name);
        await writeFile(join(directory,name),canonicalJSON(record));
      }
      // Reproduce the independent 46-of-62 replay, without resealing any record.
      for(const name of Object.keys(example.records).filter(n=>/^(baseline-ack|check-observation|invocation|worker-observation|router-evidence)-/.test(n))) await unlink(join(directory,name));
      await assert.rejects(replay(), /ENOENT/);
    } finally { await rm(directory,{recursive:true,force:true}); }
  }
});

test('valid content identities cannot substitute mismatched joins or synthetic placeholders', async () => {
  const capture=JSON.parse(await readFile(new URL('../../docs/evidence/baseline-native-case01-run.json',import.meta.url),'utf8'));
  const example=capture.cases[0],other=capture.cases[1],directory=await mkdtemp(join(tmpdir(),'baseline-joins-')),store={directory};
  try {
    for(const c of capture.cases) for(const [name,record] of Object.entries(c.records)) await writeFile(join(directory,name),canonicalJSON(record),{mode:0o600});
    const journal:any=await readEvidence(store,example.journal),foreign:any=await readEvidence(store,other.journal);
    for(const key of ['registration','packet','invocation','observation','routerEvidence','verificationInput','transcript','candidate','acknowledgement','dataset']) {
      await assert.rejects(validateRetainedRun(store,{...journal,[key]:foreign[key]},'replay'),key);
      await assert.rejects(validateRetainedRun(store,{...journal,[key]:{ref:'synthetic:intended-outcome-approval',sha256:example.unresolvedSyntheticDeclarations['synthetic:intended-outcome-approval']}},'replay'),key+' placeholder');
    }
    for(const change of [
      (j:any)=>{j.checks.pop();},
      (j:any)=>{j.checks[0].check.observation=j.checks[1].check.observation;},
      (j:any)=>{j.checks[0].check.status='passed';j.checks[0].check.exitCode=0;},
      (j:any)=>{j.registrationCommit='0'.repeat(40);},
      (j:any)=>{j.scopeStarted.id='substituted';},
      (j:any)=>{j.sources={};},
      (j:any)=>{j.export.rows[0].checks.passed=4;},
    ]) {const changed=structuredClone(journal);change(changed);await assert.rejects(validateRetainedRun(store,changed,'replay'));}
    await validateRetainedRun(store,journal,'replay');
  } finally {await rm(directory,{recursive:true,force:true});}
});
