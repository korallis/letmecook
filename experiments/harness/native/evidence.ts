// Trusted supervisor checks. A worker report is data, not receipt authority.
import { canonical, parseJSON } from '../../inference-boundary/json.ts';
import { validateRouterPolicy, classifyReceipt, hashDocument } from '../../inference-boundary/router-policy.ts';
import { validateBinding, type Binding } from '../../inference-boundary/types.ts';
import { assertNativeBinding, type NativeRouterPolicy } from '../../inference-boundary/native-policy.ts';
import { validateNativeRequest, continuationOutput, validatePatch } from '../../router-authority-extension/overlay/native-responses.mjs';
import { ADAPTER, NativeEvents, classify, digest } from './client.ts';
import { artifactMatches, validateRunRequest, type Artifact, type RunRequest, type RunResult } from './run.ts';
import { validateHeaders, type RequestObservation } from './relay.ts';
const requireValue = (value: unknown, message: string) => { if (!value) throw Error(message); };

export interface CandidateEvidence { policy: NativeRouterPolicy; packetDigest: string; scope: unknown; binding: Binding; request: RunRequest; result: RunResult; requests: RequestObservation[]; decisions: any[]; receipts: unknown[]; pendingReservations: unknown[]; durableDecisionTimes: { requestId: string; at: number }[]; observedArtifact: Artifact }
export function candidateArtifact(e: CandidateEvidence) {
  validateRunRequest(e.request);
  const p = validateRouterPolicy(e.policy); requireValue(p.schema === 3, 'native_policy_required');
  if(e.request.schema===2)requireValue(e.policy.native.schema===2&&e.policy.native.timing?.consumer==='worker'&&e.request.profileDigest===digest(e.policy.native)&&e.request.packetDigest===e.packetDigest&&e.request.sessionDeadline!<=Math.min(e.binding.expiresAt,e.binding.leaseExpiresAt),'suite_candidate_binding');
  else requireValue(e.policy.native.schema===1,'suite_candidate_tag');
  validateBinding(e.binding); assertNativeBinding(e.binding, e.policy);
  requireValue(e.binding.role === 'worker' && e.binding.routerId === p.routerId && e.binding.routeId === p.routeId && e.binding.revision === p.revision && e.binding.epoch === p.epoch && e.request.bindingDigest === digest(e.binding) && e.result.bindingDigest === digest(e.binding) && e.request.settingsDigest === e.policy.native.harness?.settings && e.result.settingsDigest === e.request.settingsDigest, 'candidate_binding_mismatch');
  const parsed = new NativeEvents(e.request.limits.outputBytes);
  for (const event of e.result.events) parsed.push(Buffer.from(event.raw + '\n')); parsed.end();
  requireValue(canonical(parsed.values) === canonical(e.result.events), 'native_event_record_mismatch');
  requireValue(e.result.outcome === 'completed_candidate' && e.result.localProcessExited === true && classify(parsed.values, e.result.exitCode, e.result.signal, false, false, artifactMatches(e.observedArtifact, e.request.baseSHA)) === 'completed_candidate' && canonical(e.result.artifact) === canonical(e.observedArtifact), 'candidate_artifact_mismatch');
  // This first public task is one patch and one final response. Larger tasks need
  // a separately reviewed task/continuation contract, not a relaxed count here.
  requireValue(e.requests.length === 2 && e.decisions.length === 2 && e.receipts.length === 2 && e.pendingReservations.length === 0 && e.durableDecisionTimes.length === 2 && new Set(e.requests.map(r => r.requestId)).size === 2, 'candidate_request_count');
  let previous: { request: any; output: any[] } | null = null;
  const decisionDigests: string[] = [], receiptDigests: string[] = [], requestDigests: string[] = [];
  for (const r of e.requests) {
    requireValue(r.path === '/v1/responses' && r.status === 200 && typeof r.requestId === 'string' && [r.startedAt, r.firstResponseAt, r.endedAt].every(t => Number.isSafeInteger(t) && t! > 0) && r.startedAt <= r.firstResponseAt! && r.firstResponseAt! <= r.endedAt!, 'candidate_request_incomplete');
    requireValue(JSON.stringify(parseJSON(r.rawBody)) === JSON.stringify(r.body), 'candidate_body_mismatch');
    const rawHeaders = Object.entries({ ...r.headers, authorization: 'Bearer ' + e.request.token }).flat();
    validateHeaders(rawHeaders, r.body, e.request.token, r.rawBody);
    const body = validateNativeRequest(r.body, e.policy.native, p.routerModel, previous);
    const d = e.decisions.find(d => d.requestId === r.requestId), receipt = e.receipts.find((x: any) => x.id === r.requestId), persisted = e.durableDecisionTimes.find(d => d.requestId === r.requestId);
    requireValue(d && d.verdict === 'validated_success' && d.delivery === 'completed' && typeof d.completionDigest === 'string' && /^[a-f0-9]{64}$/.test(d.completionDigest) && d.taskId === e.binding.taskId && d.attemptId === e.binding.attemptId && d.router.send === 'send_possible' && canonical(d.router.policy) === canonical(e.policy) && canonical(d.router.binding) === canonical(e.binding) && canonical(d.router.nativeRequest) === canonical(body) && d.router.requestDigest === hashDocument(JSON.stringify(body)), 'candidate_decision_mismatch');
    const evidence = classifyReceipt(receipt, r.requestId!, d.router);
    requireValue(evidence.disposition === 'original_success' && canonical(evidence) === canonical(d.evidence) && evidence.operations.length > 0 && persisted && Number.isSafeInteger(persisted.at) && persisted.at > 0 && r.firstResponseAt! >= persisted.at && r.endedAt! >= persisted.at, 'candidate_receipt_mismatch');
    continuationOutput(d.nativeOutput, e.policy.native);
    requireValue(evidence.operations.at(-1)?.output_digest === digest(d.nativeOutput), 'candidate_output_digest_mismatch');
    previous = { request: body, output: d.nativeOutput };
    decisionDigests.push(digest(d)); receiptDigests.push(evidence.receiptDigest!); requestDigests.push(d.router.requestDigest);
  }
  const first = e.decisions.find(d => d.requestId === e.requests[0].requestId), final = previous!;
  const calls = first.nativeOutput.filter((x: any) => x.type === 'function_call'), nativeTools = parsed.values.filter(x => x.type === 'tool_use');
  requireValue(calls.length === 1 && nativeTools.length === 1 && !final.output.some(x => x.type === 'function_call') && final.output.some(x => x.type === 'message'), 'candidate_native_terminal_mismatch');
  const call = calls[0], tool = nativeTools[0].native.part, result = final.request.input.at(-1), patch = validatePatch(call.arguments, e.policy.native);
  requireValue(tool.state.status === 'completed' && tool.callID === call.call_id && canonical(tool.state.input) === canonical(patch) && result.type === 'function_call_output' && result.call_id === call.call_id && result.output === tool.state.output && tool.state.time?.start >= e.durableDecisionTimes.find(d => d.requestId === e.requests[0].requestId)!.at, 'candidate_tool_link_mismatch');
  const finalText = final.output.filter(x => x.type === 'message').flatMap(x => x.content.map((part: any) => part.text)).join('');
  requireValue(parsed.values.filter(x => x.type === 'text').map(x => x.native.part.text).join('') === finalText, 'candidate_final_text_mismatch');
  return { schema: 1, adapter: ADAPTER, path: e.observedArtifact.path, content: e.observedArtifact.content, baseSHA: e.observedArtifact.baseSHA, headSHA: e.observedArtifact.headSHA,
    artifactDigest: digest(e.observedArtifact), diffDigest: digest(e.observedArtifact.diff), bindingDigest: digest(e.binding), settingsDigest: e.request.settingsDigest,
    requestIds: e.requests.map(r => r.requestId!), requestDigests, decisionDigests, receiptDigests, callIds: calls.map((c: any) => c.call_id), eventsDigest: digest(parsed.values), nativeUsage: 'unverified' };
}
export function candidateEnvelope(e: CandidateEvidence) {
  const packet = candidateArtifact(e), { path, content, ...metadata } = packet;
  return { schema: 1, consumer: e.policy.native.protocol, kind: 'repository_change', attemptId: e.binding.attemptId, bindingDigest: digest(e.binding), policyDigest: digest(e.policy), packetDigest: e.packetDigest, scopeDigest: digest(e.scope), requestIds: packet.requestIds, decisionDigests: packet.decisionDigests, receiptDigests: packet.receiptDigests, artifact: { path, content, metadata } };
}
export function acknowledgeCandidate(artifact: ReturnType<typeof candidateEnvelope>, acknowledgement: any, durableArtifacts: any[]) {
  const expected = digest(artifact);
  requireValue(acknowledgement?.acknowledged === true && acknowledgement.digest === expected && acknowledgement.file === 'candidate-' + expected + '.json' && durableArtifacts.some(a => a.path === artifact.artifact.path && a.digest === expected && a.file === acknowledgement.file && a.attemptId === artifact.attemptId && a.kind === artifact.kind), 'candidate_artifact_not_durable');
  return { outcome: 'completed_candidate', artifactDigest: expected, requestIds: artifact.requestIds, independentlyAccepted: false, published: false };
}
