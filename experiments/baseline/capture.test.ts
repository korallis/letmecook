import assert from 'node:assert/strict';
import { readFile, mkdtemp, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import { createHash } from 'node:crypto';
import { canonicalJSON } from './artifacts/store.ts';
import { readEvidence, readSnapshot, retainCandidate } from './artifacts/index.ts';
import { verifyBaselineTranscript } from './native/transcript.ts';
import { collectDataset } from './collect.ts';
import { rulesFor } from './case01.ts';

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
      const transcript=verifyBaselineTranscript(await readEvidence(store,journal.verificationInput));
      assert.deepEqual(transcript,await readEvidence(store,journal.transcript));assert.equal(transcript.physicalAttempts,example.scenario==='two-requests'?2:3);
      const candidate:any=await readEvidence(store,journal.candidate),registration=await readEvidence(store,journal.registration);
      await readSnapshot(store,candidate.base);await readSnapshot(store,candidate.candidate);
      assert.deepEqual(await retainCandidate(candidate.base,candidate.candidate,rulesFor(registration),store),journal.candidate);
      const measurement=collectDataset(await readEvidence(store,journal.dataset)).publicJSON;
      assert.deepEqual(measurement,journal.export);assert.equal(measurement.cohort.registered,0);assert.equal(measurement.syntheticExcluded,1);
      assert.deepEqual(measurement.rows[0].checks,{passed:1,failed:3,'not-run':0,unknown:0});assert.equal(measurement.rows[0].outputLabel,null);
      assert.equal(measurement.rows[0].trialEffort.totalSeconds,null);assert.equal(measurement.rows[0].physicalFailureCount,0);
    }finally{await rm(directory,{recursive:true,force:true});}
  }
});
