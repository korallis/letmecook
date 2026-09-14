// Trusted supervisor projection from the private gateway evidence API. The
// consumer's plan is data; original receipts and decisions come from the gateway.
import { canonical } from '../inference-boundary/json.ts';
import { validateBinding, type Binding } from '../inference-boundary/types.ts';
import { assertNativeBinding, assertPlannerPacket, validateNativeRouterPolicy } from '../inference-boundary/native-policy.ts';
import { classifyReceipt, hashDocument } from '../inference-boundary/router-policy.ts';
import { validateNativeRequest, validateNativeOutput } from '../router-authority-extension/overlay/native-responses.mjs';
import { PLANNER_PROTOCOL, plannerRequestState, validatePlannerCandidate } from '../router-authority-extension/overlay/native-planner.mjs';
import type { Result } from './planner.ts';
const check=(value:unknown)=>{if(!value)throw Error('planner_candidate_denied');};
export function nativePlannerCandidate(result:Result,evidence:any,binding:Binding){
 const p=validateNativeRouterPolicy(evidence.selectedPolicy);validateBinding(binding);assertNativeBinding(binding,p);
 check(p.native.protocol===PLANNER_PROTOCOL&&binding.role==='planner'&&evidence.schema===1&&evidence.attemptId===binding.attemptId&&/^[a-f0-9]{64}$/.test(evidence.packetDigest));
 const decisions=evidence.decisions;check(Array.isArray(decisions)&&decisions.length>0&&decisions.length<=3&&evidence.reservations.length===0&&evidence.receipts.length===decisions.length&&evidence.decisionTimes.length===decisions.length&&result.completions.length===decisions.length);
 check(result.outcome==='plan_proposed'&&result.authority==='proposal_only'&&result.routeId===p.routeId&&result.proposal);
 check(canonical(result.settings)===canonical({profile:'router-native-responses-local-v1',protocol:PLANNER_PROTOCOL,model:'gpt-6-astra',reasoning:{effort:'xhigh',summary:'auto'},store:false,stream:true,providerOutputTokens:null,providerMonetaryCap:null}));
 let previous:null|{request:any;output:any[]}=null;
 for(const [index,d] of decisions.entries()){
  check(d.verdict==='validated_success'&&d.delivery==='completed'&&d.attemptId===binding.attemptId&&d.taskId===binding.taskId&&canonical(d.router.policy)===canonical(p)&&canonical(d.router.binding)===canonical(binding)&&d.router.send==='send_possible');
  const body=validateNativeRequest(d.router.nativeRequest,p.native,p.routerModel,previous);assertPlannerPacket(body,p);validateNativeOutput(d.nativeOutput,p.native,body);
  check(d.router.requestDigest===hashDocument(JSON.stringify(body)));
  const actual=classifyReceipt(evidence.receipts.find((r:any)=>r.id===d.requestId),d.requestId,d.router),completion=result.completions[index],persisted=evidence.decisionTimes.find((x:any)=>x.requestId===d.requestId);
  check(actual.disposition==='original_success'&&canonical(actual)===canonical(d.evidence)&&actual.operations.at(-1)?.output_digest===hashDocument(d.nativeOutput));
  check(completion.requestId===d.requestId&&Number.isSafeInteger(completion.acceptedAt)&&completion.acceptedAt<=Date.now()&&persisted&&Number.isSafeInteger(persisted.at)&&persisted.at>0&&completion.acceptedAt>=persisted.at&&canonical(completion.calls)===canonical(d.nativeOutput.filter((x:any)=>x.type==='function_call').map((x:any)=>x.call_id)));
  previous={request:body,output:d.nativeOutput};
 }
 const last=decisions.at(-1),state=plannerRequestState(last.router.nativeRequest,p.native),content=JSON.stringify(result.proposal);
 check(result.inputRevision===state.inputRevision&&canonical(result.usage)===canonical({assessments:1,repairs:state.repairs,requests:state.requests,files:state.fileRead?1:0,readBytes:state.fileRead?state.packet.evidence.bytes:0}));
 const artifact={path:'plan-proposal.json',content,metadata:{inputRevision:state.inputRevision,authority:'proposal_only'}};
 const text=last.nativeOutput.filter((x:any)=>x.type==='message').flatMap((x:any)=>x.content.map((part:any)=>part.text)).join('');
 check(validatePlannerCandidate({text,policy:p,binding,decisions,artifact}));
 return {schema:1,consumer:PLANNER_PROTOCOL,kind:'plan_proposal',attemptId:binding.attemptId,bindingDigest:hashDocument(binding),policyDigest:hashDocument(p),packetDigest:evidence.packetDigest,scopeDigest:hashDocument(evidence.scope),requestIds:decisions.map((d:any)=>d.requestId),decisionDigests:decisions.map(hashDocument),receiptDigests:decisions.map((d:any)=>d.evidence.receiptDigest),artifact};
}
export function acknowledgeNativePlanner(candidate:ReturnType<typeof nativePlannerCandidate>,ack:any,artifacts:any[]){
 const digest=hashDocument(candidate);check(ack?.acknowledged===true&&ack.digest===digest&&typeof ack.file==='string'&&artifacts.some(a=>a.digest===digest&&a.file===ack.file&&a.path===candidate.artifact.path));
 return {outcome:'acknowledged_plan_proposal',artifactDigest:digest,requestIds:candidate.requestIds,authority:'proposal_only',independentlyAccepted:false,published:false};
}
