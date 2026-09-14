// Private supervisor controls. Worker reports cannot create receipt authority.
import { canonical, digest, nativeConsumerRole } from '../router-authority-extension/overlay/native-profile.mjs';
import { classifyReceipt, validateRouterPolicy } from '../inference-boundary/router-policy.ts';
const check = (value, reason) => { if (!value) throw Error(reason); };
const exact = (value, fields) => check(value && typeof value === 'object' && !Array.isArray(value) && canonical(Object.keys(value).sort()) === canonical([...fields].sort()), 'invalid_candidate_fields');
const ref = value => typeof value === 'string' && /^[a-z][a-z0-9_-]{0,63}$/.test(value);
export function evidenceControls({ policy, gate, authority, packetDigest, observed, persist, validateProposal = () => false }) {
  function evidence(attemptId) {
    check(ref(attemptId), 'invalid_evidence_attempt');
    const journal = gate().snapshot(), decisions = (journal.decisions ?? []).filter(d => d.attemptId === attemptId), reservations = journal.reservations.filter(r => r.attemptId === attemptId);
    const ids = [...new Set([...decisions, ...reservations].map(r => r.requestId))];
    return structuredClone({ schema: 1, packetDigest, selectedPolicy: policy(), attemptId, decisions, receipts: ids.map(id => authority.receipt(id)), reservations,
      decisionTimes: observed.decisions.filter(d => ids.includes(d.requestId)), artifacts: observed.artifacts.filter(a => a.attemptId === attemptId), scope: authority.evaluationScope(policy().native.scope.id) ?? null });
  }
  function candidate(value) {
    exact(value, ['schema', 'consumer', 'kind', 'attemptId', 'bindingDigest', 'policyDigest', 'requestIds', 'decisionDigests', 'receiptDigests', 'artifact']);
    check(value.schema === 1 && ref(value.attemptId) && Buffer.byteLength(JSON.stringify(value)) <= 1572864, 'invalid_candidate');
    const snapshot = evidence(value.attemptId), p = validateRouterPolicy(policy()), decisions = snapshot.decisions;
    check(p.schema === 3 && value.consumer === p.native.protocol && value.policyDigest === digest(p), 'candidate_policy_mismatch');
    check(decisions.length > 0 && !snapshot.reservations.length && authority.quiescent(decisions.map(d => d.requestId)), 'candidate_unresolved_work');
    check(canonical(value.requestIds) === canonical(decisions.map(d => d.requestId)) && canonical(value.decisionDigests) === canonical(decisions.map(d => digest(d))) && canonical(value.receiptDigests) === canonical(decisions.map(d => d.evidence.receiptDigest)), 'candidate_evidence_mismatch');
    const binding = decisions[0].router.binding;
    check(value.bindingDigest === digest(binding) && binding.role === nativeConsumerRole(p.native), 'candidate_binding_mismatch');
    for (const d of decisions) {
      check(d.verdict === 'validated_success' && d.delivery === 'completed' && canonical(d.router.policy) === canonical(p) && canonical(d.router.binding) === canonical(binding), 'candidate_incomplete_decision');
      const receipt = snapshot.receipts.find(r => r.id === d.requestId), actual = classifyReceipt(receipt, d.requestId, d.router);
      check(actual.disposition === 'original_success' && canonical(actual) === canonical(d.evidence) && actual.operations.at(-1)?.output_digest === digest(d.nativeOutput), 'candidate_receipt_mismatch');
    }
    exact(value.artifact, ['path', 'content', 'metadata']);
    const artifact = value.artifact;
    check(typeof artifact.path === 'string' && typeof artifact.content === 'string' && Buffer.byteLength(artifact.content) <= 1048576 && artifact.metadata && typeof artifact.metadata === 'object' && !Array.isArray(artifact.metadata), 'invalid_candidate_artifact');
    const last = decisions.at(-1).nativeOutput;
    check(Array.isArray(last) && last.some(x => x.type === 'message') && !last.some(x => x.type === 'function_call'), 'candidate_terminal_missing');
    if (value.kind === 'repository_change') check(binding.role === 'worker' && p.native.protocol === 'opencode-1.18.30-responses-apply-patch-v1' && p.native.toolPaths.includes(artifact.path), 'candidate_write_path');
    else if (value.kind === 'plan_proposal') {
      const text = last.filter(x => x.type === 'message').flatMap(x => x.content.map(c => c.text)).join('');
      check(binding.role === 'planner' && p.native.protocol === 'planner-probe-responses-read-file-v1' && artifact.path === 'plan-proposal.json' && canonical(JSON.parse(artifact.content)) === canonical(JSON.parse(text)) && validateProposal({ text, policy: p, binding, decisions, artifact }) === true, 'candidate_plan_mismatch');
    } else throw Error('unsupported_candidate_kind');
    const id = digest(value), existing = observed.artifacts.find(a => a.digest === id);
    if (!existing) {
      // Persist the entire linked packet, including the artifact, before ack.
      persist('/state/candidate-' + id + '.json', { ...structuredClone(value), digest: id });
      observed.artifacts.push({ attemptId: value.attemptId, path: artifact.path, digest: id, kind: value.kind });
    }
    return { acknowledged: true, digest: id };
  }
  return { evidence, candidate };
}
