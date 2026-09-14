// Compact public evidence from full offline observations. Never accepts a live
// report or emits credentials, request prose, private source paths or account IDs.
import assert from 'node:assert/strict';
import { readFile,writeFile,mkdir } from 'node:fs/promises';
import { resolve,join,dirname,basename } from 'node:path';
import { createHash } from 'node:crypto';
import { digest } from '../router-authority-extension/overlay/native-profile.mjs';
const [matrixPath,gatewayPath,legacyPath,clarificationPath,outputArgument]=process.argv.slice(2);assert(matrixPath&&gatewayPath&&legacyPath&&clarificationPath);
const root=resolve(import.meta.dirname,'../..'),output=resolve(outputArgument??join(import.meta.dirname,'evidence/native-integration-run.json'));
const sha=(bytes:string|Buffer)=>createHash('sha256').update(bytes).digest('hex');
const [matrixRaw,gatewayRaw,legacyRaw,clarificationRaw]=await Promise.all([matrixPath,gatewayPath,legacyPath,clarificationPath].map(p=>readFile(p,'utf8'))),matrix=JSON.parse(matrixRaw),gateway=JSON.parse(gatewayRaw),legacy=JSON.parse(legacyRaw),clarification=JSON.parse(clarificationRaw);
assert.equal(matrix.realProviderCalled,false);assert.equal(matrix.cleanupVerified,true);assert.equal(matrix.cases.length,17);assert(matrix.cases.every((c:any)=>c.exitCode===0&&c.observation.result==='passed'&&c.observation.live===false));
for(const g of [gateway,clarification]){assert.equal(g.result,'passed');assert.equal(g.live,false);assert.equal(g.cleanup,true);assert.equal(g.supervisorRejections.length,14);assert.equal(g.invalidCandidateSubmissions,8);}
assert.equal(gateway.consumer.result.outcome,'plan_proposed');assert.equal(clarification.scenario,'clarification');assert.equal(clarification.consumer.result.outcome,'clarification_proposed');assert.equal(clarification.accepted.outcome,'acknowledged_clarification_proposal');assert.equal(clarification.evidence.scope.spent,1);assert.equal(clarification.consumer.result.usage.requests,1);
assert.equal(legacy.result,'synthetic-planner-router-passed');assert.equal(legacy.realProviderCalled,false);assert.equal(legacy.cases.length,43);assert(legacy.cases.every((c:any)=>c.result==='passed'));assert.equal(legacy.cleanup.verified,true);
for(const source of [matrix.sourceDigests,gateway.sources,clarification.sources])for(const [path,hash]of Object.entries(source))assert.equal(sha(await readFile(join(root,path))),hash,'changed integrated source: '+path);
const sources={...matrix.sourceDigests,...gateway.sources,...clarification.sources};
const cases=matrix.cases.map((c:any)=>{
 const x=c.observation;return {scenario:c.scenario,result:x.result,plannerOutcome:x.planner.outcome,usage:x.planner.usage,settings:x.planner.settings,proposalPresent:!!x.planner.proposal,
  physicalRequests:x.physicalSends.map((s:any)=>({path:s.path,bodyDigest:digest(s.body),model:s.body.model,reasoning:s.body.reasoning,store:s.body.store,stream:s.body.stream,include:s.body.include,toolChoice:s.body.tool_choice,tools:s.body.tools.map((t:any)=>({name:t.name,parameters:t.parameters,strictPresent:Object.hasOwn(t,'strict')})),providerCapFields:Object.keys(s.body).filter(k=>/^max_.*tokens$/.test(k)),inputItems:s.body.input.length})),
  completions:x.planner.completions,decisions:x.journal.decisions.map((d:any)=>({requestId:d.requestId,verdict:d.verdict,delivery:d.delivery,requestDigest:d.router.requestDigest,decisionDigest:digest(d),receiptDigest:d.evidence.receiptDigest,nativeOutputDigest:d.nativeOutput?digest(d.nativeOutput):null})),
  receipts:x.receipts.map((r:any)=>({requestId:r.id,known:r.known,quiescent:r.quiescent,handlerDone:r.handler_done,localStop:r.local_stop,operations:r.operations.map((o:any)=>({ordinal:o.ordinal,model:o.model,terminal:o.terminal,localStop:o.local_stop,bodyDigest:o.body_digest,outputDigest:o.output_digest}))})),
  reservations:x.journal.reservations.length,scope:{phase:x.scope.phase,started:x.scope.started,deadline:x.scope.deadline,spent:x.scope.spent,state:x.scope.state},decisionTimes:x.decisionTimes,durabilityEnded:x.durabilityEnded,fault:x.fault};
});
const summarizeGateway=(g:any)=>({scenario:g.scenario,result:g.result,plannerOutcome:g.consumer.result.outcome,stagedFiles:g.stagedFiles,consumerProfile:g.consumerProfile,containment:g.consumer.containment,usage:g.consumer.result.usage,settings:g.consumer.result.settings,supervisorRejections:g.supervisorRejections,invalidCandidateSubmissions:g.invalidCandidateSubmissions,acknowledgement:g.acknowledgement,accepted:g.accepted,durableCandidateDigest:g.durable.digest,immutableAfterGatewayStop:g.durable.digest===g.acknowledgement.digest,scopeBefore:g.evidence.scope,scopeAfterSelection:g.workerSelection.scope,generationBefore:g.evidence.selectedPolicy.authority.generation,generationAfter:g.workerSelection.policy.authority.generation,cleanup:g.cleanup});
const result={schema:1,observedAt:new Date().toISOString(),result:'offline_native_planner_integration_passed',issueComplete:false,liveInferenceAttempts:0,liveEligible:false,independentReview:'required',
 sourceDigests:sources,runtime:{matrix:matrix.runtime,consumer:gateway.consumer.runtime},
 rawReports:[{name:basename(dirname(matrixPath))+'/run.json',sha256:sha(matrixRaw)},{name:basename(dirname(gatewayPath))+'/run.json',sha256:sha(gatewayRaw)},{name:basename(legacyPath),sha256:sha(legacyRaw)},{name:basename(dirname(clarificationPath))+'/run.json',sha256:sha(clarificationRaw)}],
 nativeCases:cases,
 gateway:summarizeGateway(gateway),clarificationGateway:summarizeGateway(clarification),
 legacy:{cases:legacy.cases.map((c:any)=>({profile:c.profile,scenario:c.scenario,result:c.result})),runtime:legacy.runtime,cleanup:legacy.cleanup,sourceDigests:legacy.sources},
 limitations:['Synthetic HTTP only; no model or deployed subscription conformance.','Standalone matrix uses a trusted combined gateway/consumer fixture; staged containment and acknowledgement have a separate production gateway proof.','Planner-to-worker registry proof verifies selection and scope continuity; worker binary execution is covered by its own adapter suite.','Initial live suite requires all consumer and concrete deployment reviews before one shared 10-attempt/600000ms clock.']};
await mkdir(dirname(output),{recursive:true});await writeFile(output,JSON.stringify(result,null,2)+'\n');console.log(JSON.stringify({result:result.result,nativeCases:cases.length,legacyCases:legacy.cases.length,sources:Object.keys(sources).length,output}));
