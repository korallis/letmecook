// Compact public evidence from full offline observations. Never accepts a live
// report or emits credentials, request prose, private source paths or account IDs.
import assert from 'node:assert/strict';
import { readFile,writeFile,mkdir } from 'node:fs/promises';
import { resolve,join,dirname,basename } from 'node:path';
import { createHash } from 'node:crypto';
import { digest } from '../router-authority-extension/overlay/native-profile.mjs';
const [matrixPath,gatewayPath,legacyPath,outputArgument]=process.argv.slice(2);assert(matrixPath&&gatewayPath&&legacyPath);
const root=resolve(import.meta.dirname,'../..'),output=resolve(outputArgument??join(import.meta.dirname,'evidence/native-integration-run.json'));
const sha=(bytes:string|Buffer)=>createHash('sha256').update(bytes).digest('hex');
const [matrixRaw,gatewayRaw,legacyRaw]=await Promise.all([matrixPath,gatewayPath,legacyPath].map(p=>readFile(p,'utf8'))),matrix=JSON.parse(matrixRaw),gateway=JSON.parse(gatewayRaw),legacy=JSON.parse(legacyRaw);
assert.equal(matrix.realProviderCalled,false);assert.equal(matrix.cleanupVerified,true);assert.equal(matrix.cases.length,16);assert(matrix.cases.every((c:any)=>c.exitCode===0&&c.observation.result==='passed'&&c.observation.live===false));
assert.equal(gateway.result,'passed');assert.equal(gateway.live,false);assert.equal(gateway.cleanup,true);assert.equal(gateway.supervisorRejections.length,11);
assert.equal(legacy.result,'synthetic-planner-router-passed');assert.equal(legacy.realProviderCalled,false);assert.equal(legacy.cases.length,43);assert(legacy.cases.every((c:any)=>c.result==='passed'));assert.equal(legacy.cleanup.verified,true);
for(const source of [matrix.sourceDigests,gateway.sources])for(const [path,hash]of Object.entries(source))assert.equal(sha(await readFile(join(root,path))),hash,'changed integrated source: '+path);
const sources={...matrix.sourceDigests,...gateway.sources};
const cases=matrix.cases.map((c:any)=>{
 const x=c.observation;return {scenario:c.scenario,result:x.result,plannerOutcome:x.planner.outcome,usage:x.planner.usage,settings:x.planner.settings,proposalPresent:!!x.planner.proposal,
  physicalRequests:x.physicalSends.map((s:any)=>({path:s.path,bodyDigest:digest(s.body),model:s.body.model,reasoning:s.body.reasoning,store:s.body.store,stream:s.body.stream,include:s.body.include,toolChoice:s.body.tool_choice,tools:s.body.tools.map((t:any)=>({name:t.name,parameters:t.parameters,strictPresent:Object.hasOwn(t,'strict')})),providerCapFields:Object.keys(s.body).filter(k=>/^max_.*tokens$/.test(k)),inputItems:s.body.input.length})),
  completions:x.planner.completions,decisions:x.journal.decisions.map((d:any)=>({requestId:d.requestId,verdict:d.verdict,delivery:d.delivery,requestDigest:d.router.requestDigest,decisionDigest:digest(d),receiptDigest:d.evidence.receiptDigest,nativeOutputDigest:d.nativeOutput?digest(d.nativeOutput):null})),
  receipts:x.receipts.map((r:any)=>({requestId:r.id,known:r.known,quiescent:r.quiescent,handlerDone:r.handler_done,localStop:r.local_stop,operations:r.operations.map((o:any)=>({ordinal:o.ordinal,model:o.model,terminal:o.terminal,localStop:o.local_stop,bodyDigest:o.body_digest,outputDigest:o.output_digest}))})),
  reservations:x.journal.reservations.length,scope:{phase:x.scope.phase,started:x.scope.started,deadline:x.scope.deadline,spent:x.scope.spent,state:x.scope.state},decisionTimes:x.decisionTimes,durabilityEnded:x.durabilityEnded,fault:x.fault};
});
const result={schema:1,observedAt:new Date().toISOString(),result:'offline_native_planner_integration_passed',issueComplete:false,liveInferenceAttempts:0,liveEligible:false,independentReview:'required',
 sourceDigests:sources,runtime:{matrix:matrix.runtime,consumer:gateway.consumer.runtime},
 rawReports:[{name:basename(dirname(matrixPath))+'/run.json',sha256:sha(matrixRaw)},{name:basename(dirname(gatewayPath))+'/run.json',sha256:sha(gatewayRaw)},{name:basename(legacyPath),sha256:sha(legacyRaw)}],
 nativeCases:cases,
 gateway:{result:gateway.result,stagedFiles:gateway.stagedFiles,consumerProfile:gateway.consumerProfile,containment:gateway.consumer.containment,usage:gateway.consumer.result.usage,settings:gateway.consumer.result.settings,supervisorRejections:gateway.supervisorRejections,invalidCandidateSubmissions:6,acknowledgement:gateway.acknowledgement,accepted:gateway.accepted,durableCandidateDigest:gateway.durable.digest,immutableAfterGatewayStop:gateway.durable.digest===gateway.acknowledgement.digest,scopeBefore:gateway.evidence.scope,scopeAfterSelection:gateway.workerSelection.scope,generationBefore:gateway.evidence.selectedPolicy.authority.generation,generationAfter:gateway.workerSelection.policy.authority.generation,cleanup:gateway.cleanup},
 legacy:{cases:legacy.cases.map((c:any)=>({profile:c.profile,scenario:c.scenario,result:c.result})),runtime:legacy.runtime,cleanup:legacy.cleanup,sourceDigests:legacy.sources},
 limitations:['Synthetic HTTP only; no model or deployed subscription conformance.','Standalone matrix uses a trusted combined gateway/consumer fixture; staged containment and acknowledgement have a separate production gateway proof.','Planner-to-worker registry proof verifies selection and scope continuity; worker binary execution is covered by its own adapter suite.','Initial live suite requires all consumer and concrete deployment reviews before one shared 10-attempt/600000ms clock.']};
await mkdir(dirname(output),{recursive:true});await writeFile(output,JSON.stringify(result,null,2)+'\n');console.log(JSON.stringify({result:result.result,nativeCases:cases.length,legacyCases:legacy.cases.length,sources:Object.keys(sources).length,output}));
