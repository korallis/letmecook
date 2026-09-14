import { canonical } from '../inference-boundary/json.ts';
import { digest, settingsDigest } from '../harness/native/client.ts';
import { validateRouterPolicy } from '../inference-boundary/router-policy.ts';
import { assertNativeBinding } from '../inference-boundary/native-policy.ts';
import { successfulRequest } from './control.mjs';
import { classify, unchanged, validateRequest, type Request } from './client.ts';
import { FIRST, SECOND, TRANSITION } from './constants.ts';
const check = (value: unknown, reason: string) => { if (!value) throw Error(reason); };
export function qualify(input: { policy: any; binding: any; request: Request; observation: any; observedArtifact: any; evidence: any }) {
  const { binding,request,observation,observedArtifact,evidence } = input, policy = validateRouterPolicy(input.policy);
  if(policy.schema !== 3) throw Error('continuity_policy'); assertNativeBinding(binding,policy as any); validateRequest(request);
  check([FIRST,SECOND].includes(binding.attemptId) && request.bindingDigest === digest(binding) && request.settingsDigest === settingsDigest('ask') && canonical(policy.native) === canonical(evidence.selectedPolicy.native),'continuity_binding');
  const result = observation.result;
  check(result.bindingDigest === request.bindingDigest && result.settingsDigest === request.settingsDigest && result.outcome === 'continuity_transport_completed' && result.localProcessExited === true,'continuity_worker');
  check(classify(result.events,result.exitCode,result.signal,false,false,observedArtifact,request.baseSHA) === 'continuity_transport_completed' && unchanged(observedArtifact,request.baseSHA),'continuity_artifact');
  const authoritative = successfulRequest(evidence,policy,binding.attemptId), decision = authoritative.decision;
  check(canonical(decision.router.binding) === canonical(binding) && observation.requests.length === 1 && !observation.rejected.length,'continuity_requests');
  const observed = observation.requests[0];
  check(observed.requestId === decision.requestId && observed.status === 200 && observed.endedAt && digest(observed.rawBody) === decision.router.requestDigest && canonical(JSON.parse(observed.rawBody)) === canonical(decision.router.nativeRequest),'continuity_request_identity');
  check(evidence.decisionTimes.some((x: any) => x.requestId === decision.requestId && x.at <= observed.firstResponseAt),'continuity_durable_release');
  return {caseRef:binding.attemptId,requestId:decision.requestId,connectionId:authoritative.operation.connection_id,receiptDigest:digest(authoritative.receipt),decisionDigest:digest(decision),policyDigest:digest(policy),profileDigest:digest(policy.native),packetDigest:evidence.packetDigest,scope:evidence.scope,outcome:'continuity_transport_completed',fixtureArtifactDigest:digest(observedArtifact)};
}
export function qualifyPair(first: any,second: any,transition: any) {
  check(first.caseRef === FIRST && second.caseRef === SECOND && first.connectionId !== second.connectionId,'continuity_selection');
  for (const field of ['policyDigest','profileDigest','packetDigest']) check(first[field] === second[field],'continuity_identity');
  for (const field of ['id','boot','started','deadline','spec']) check(first.scope[field] === second.scope[field],'continuity_scope');
  check(second.scope.spent === first.scope.spent + 1 && transition.acknowledged && transition.digest === digest(transition.result) && transition.result.transitionId === TRANSITION && transition.result.packetDigest === first.packetDigest && transition.result.targetConnection === first.connectionId && transition.result.profileDigest === first.profileDigest && transition.result.controlledHealthState === true && transition.result.naturalQuotaEvidence === false && transition.result.scopeDigest === digest(first.scope),'continuity_transition');
  // Public output uses fixed opaque refs, never raw policy/connection/account data.
  return {schema:1,evidence:'actual-native-harness-router-synthetic',live:false,issueComplete:false,result:'passed',profileDigest:first.profileDigest,packetDigest:first.packetDigest,selection:['connection_a','connection_b'],requests:[first,second].map((x,i) => ({caseRef:x.caseRef,connectionRef:i===0?'connection_a':'connection_b',receiptDigest:x.receiptDigest,decisionDigest:x.decisionDigest,outcome:x.outcome})),scope:{ref:'initial_checks',started:first.scope.started,deadline:first.scope.deadline,spentBefore:first.scope.spent-1,spentAfter:second.scope.spent,maxInferenceAttempts:10,elapsedMs:600000},transition:{digest:transition.digest,kind:'controlled_health_state',naturalQuotaEvidence:false},distinctLiveSubscriptions:'pending',usage:'unknown',headroom:'unknown',paidFallback:'denied',refresh:'denied'};
}
