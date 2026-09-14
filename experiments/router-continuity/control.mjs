// Trusted private fixture control. There is deliberately no account-selection API.
import { canonical, digest, NATIVE_PROTOCOL } from '../router-authority-extension/overlay/native-profile.mjs';
import { classifyReceipt } from '../inference-boundary/router-policy.ts';
import { settings, environment } from '../native-evaluation/opencode-settings.mjs';
import { FIXTURE, FIRST, SECOND, TRANSITION, EXPECTED, PROMPT } from './constants.ts';
const check = (value, reason) => { if (!value) throw Error(reason); };
const exact = (value, keys) => check(value && !Array.isArray(value) && canonical(Object.keys(value).sort()) === canonical([...keys].sort()), 'continuity_fields');
const fixedSettings = settings('<boundary-grant>'); fixedSettings.permission.edit = 'ask';
export const continuitySettingsDigest = digest({ config: fixedSettings, environment });
export function continuityManifest(input, profiles) {
  if (input === null) return null;
  exact(input, ['schema', 'fixture', 'firstAttemptId', 'secondAttemptId', 'transitionId', 'settingsDigest', 'scopeId']);
  check(input.schema === 1 && input.fixture === FIXTURE && input.firstAttemptId === FIRST && input.secondAttemptId === SECOND && input.transitionId === TRANSITION && input.settingsDigest === continuitySettingsDigest, 'continuity_fixture');
  const matches = profiles.filter(p => p.protocol === NATIVE_PROTOCOL && p.harness.settings === input.settingsDigest && p.scope.id === input.scopeId);
  check(matches.length === 1 && matches[0].scope.phase === 'initial' && matches[0].connections.length === 2, 'continuity_profile');
  return { ...structuredClone(input), profileDigest: digest(matches[0]) };
}
export function successfulRequest(evidence, policy, attemptId) {
  check(evidence.attemptId === attemptId && canonical(evidence.selectedPolicy) === canonical(policy) && !evidence.reservations.length && evidence.decisions.length === 1 && evidence.receipts.length === 1, 'continuity_evidence');
  const decision = evidence.decisions[0], receipt = evidence.receipts[0];
  check(decision.attemptId === attemptId && decision.router.binding.attemptId === attemptId && canonical(decision.router.policy) === canonical(policy) && decision.verdict === 'validated_success' && decision.delivery === 'completed', 'continuity_decision');
  const classified = classifyReceipt(receipt, decision.requestId, decision.router);
  check(classified.disposition === 'original_success' && canonical(classified) === canonical(decision.evidence) && classified.operations.length === 1, 'continuity_receipt');
  const operation = classified.operations[0], output = decision.nativeOutput;
  const input = decision.router.nativeRequest?.input, users = input?.filter(x => x.role === 'user');
  check(Array.isArray(input) && input.every(x => ['developer','system','user'].includes(x.role)) && users.length === 1 && canonical(users[0].content) === canonical([{type:'input_text',text:JSON.stringify(PROMPT)}]), 'continuity_prompt');
  check(operation.provider === 'codex' && operation.model === 'gpt-6-astra' && operation.terminal === 'provider_completed' && operation.local_stop === 'original_eof' && operation.output_digest === digest(output), 'continuity_original');
  check(Array.isArray(output) && output.every(x => ['reasoning', 'message'].includes(x.type)) && output.filter(x => x.type === 'message').map(x => x.content.map(c => c.text).join('')).join('') === EXPECTED, 'continuity_output');
  return { decision, receipt, operation };
}
export function continuityControl({ manifest, packetDigest, policy, gate, authority, evidence, persist, recovered = false, health, markUnavailable, fence }) {
  let busy = false, failed = recovered, admissions = 0, completed = null;
  function admissionAllowed() { check(!busy && !failed, 'continuity_fenced'); }
  function wrapGate(current) {
    const original = current.admit.bind(current);
    current.admit = async (...args) => {
      admissionAllowed(); admissions++;
      try { return await original(...args); } finally { admissions--; }
    };
  }
  function controlAllowed(command) { if (!['inspect', 'evidence', 'stop'].includes(command)) admissionAllowed(); }
  function current() {
    const p = policy();
    check(manifest && digest(p.native) === manifest.profileDigest && p.native.harness.settings === manifest.settingsDigest && p.native.scope.id === manifest.scopeId, 'continuity_profile');
    const state = authority.state();
    check(state.phase === 'active' && state.boot === p.authority.boot && state.generation === p.authority.generation && state.revision === p.revision && digest(authority.snapshot()) === p.authority.graphDigest, 'continuity_fence');
    const scope = authority.evaluationScope(manifest.scopeId);
    check(scope && scope.state === 'active' && scope.phase === 'initial' && Date.now() < scope.deadline && scope.spent < p.native.scope.maxInferenceAttempts, 'continuity_scope');
    check(!admissions && !gate().snapshot().reservations.length && authority.quiescent([]), 'continuity_not_quiescent');
    const first = successfulRequest(evidence(FIRST), p, FIRST);
    // Includes scope monotonic/token/lease checks; no receipt clock is substituted.
    authority.assertNativeCurrent(first.decision, true);
    check(!evidence(SECOND).decisions.length && !evidence(SECOND).reservations.length, 'continuity_second_already_started');
    return { p, scope, first };
  }
  async function transition(message) {
    exact(message, ['command', 'packetDigest', 'transitionId']);
    check(manifest && message.command === 'continuity-transition' && message.packetDigest === packetDigest && message.transitionId === manifest.transitionId, 'continuity_command');
    admissionAllowed();
    if (completed) return structuredClone(completed); // Same one-use command, never repeat/extend mutation.
    busy = true;
    let intentStarted = false;
    try {
      const { p, scope, first } = current();
      const before = await health(first.operation.connection_id);
      current();
      check(before.active === true && !before.lockUntil, 'continuity_initial_health');
      const intent = { schema: 1, fixture: manifest, packetDigest, authority: p.authority, scope: { id: scope.id, started: scope.started, deadline: scope.deadline, spent: scope.spent }, firstRequestId: first.decision.requestId, receiptDigest: digest(first.receipt), targetConnection: first.operation.connection_id, kind: 'controlled_health_state', naturalQuotaEvidence: false };
      intentStarted = true;
      const savedIntent = persist('continuity-intent', intent);
      check(savedIntent.digest === digest(intent), 'continuity_intent_storage');
      current();
      await markUnavailable(first.operation.connection_id, scope.deadline);
      const after = await health(first.operation.connection_id), next = current();
      check(after.active === true && after.lockUntil >= scope.deadline - 5 && after.lockUntil <= scope.deadline + 5 && after.status === 'unavailable' && before.credentialsDigest === after.credentialsDigest && canonical(next.scope) === canonical(scope), 'continuity_health_changed');
      const result = { schema: 1, transitionId: manifest.transitionId, packetDigest, intentDigest: savedIntent.digest, targetConnection: first.operation.connection_id, lockUntil: after.lockUntil, scopeDigest: digest(scope), graphDigest: digest(authority.snapshot()), profileDigest: digest(p.native), controlledHealthState: true, naturalQuotaEvidence: false };
      const saved = persist('continuity-result', result);
      check(saved.digest === digest(result), 'continuity_result_storage');
      current();
      completed = { acknowledged: true, digest: saved.digest, file: saved.file, result };
      return structuredClone(completed);
    } catch (error) {
      if (intentStarted) { failed = true; fence(); }
      throw error;
    } finally { busy = false; }
  }
  return { transition, wrapGate, controlAllowed, admissionAllowed, state: () => ({ busy, failed, admissions, completed }) };
}
