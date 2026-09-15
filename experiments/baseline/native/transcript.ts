// Supervisor-side consistency proof for the public synthetic case01 only.
import { canonical, keys, object, parseJSON } from '../../inference-boundary/json.ts';
import { classifyReceipt, hashDocument, sha, validateRouterPolicy } from '../../inference-boundary/router-policy.ts';
import { assertNativeBinding } from '../../inference-boundary/native-policy.ts';
import { validateBinding } from '../../inference-boundary/types.ts';
import { continuationOutput, expectedPhysicalRequest, validateNativeRequest, validatePatch } from '../../router-authority-extension/overlay/native-responses.mjs';
import { NativeEvents, PROFILE, digest, settingsDigest } from '../../harness/native/client.ts';
import { validateHeaders } from '../../harness/native/relay.ts';

const check = (value: unknown, reason: string): void => { if (!value) throw Error(reason); };
const exact = (value: unknown, fields: string[]) => keys(value, fields, fields);
const same = (a: unknown, b: unknown) => canonical(a) === canonical(b);
const time = (value: unknown): value is number => Number.isSafeInteger(value) && (value as number) > 0;
const text = (value: unknown): value is string => typeof value === 'string' && value.length > 0 && Buffer.byteLength(value) <= 262144;

/** `expected` must come from the supervisor's retained inputs, never the worker. Throws on unknown or inconsistent evidence. */
export function verifyBaselineTranscript(input: unknown) {
  exact(input, ['schema', 'origin', 'caseId', 'expected', 'policy', 'binding', 'packetDigest', 'scope', 'requests', 'decisions', 'receipts', 'pendingReservations', 'durableDecisionTimes', 'physicalRequests', 'scopeOperations', 'events', 'exitCode', 'signal', 'localProcessExited']);
  object(input); const e = input, x = e.expected;
  exact(x, ['prompt', 'context', 'physicalInstructions', 'policyDigest', 'bindingDigest', 'packetDigest', 'scopeDigest']);
  check(e.schema === 1 && e.origin === 'synthetic' && e.caseId === 'case01' && [x.prompt, x.context, x.physicalInstructions].every(text), 'baseline_contract');
  check([x.policyDigest, x.bindingDigest, x.packetDigest, x.scopeDigest].every(sha), 'baseline_expected_digest');
  const p = validateRouterPolicy(e.policy);
  if (p.schema !== 3) throw Error('baseline_native_policy');
  const n = p.native, b = e.binding, scope = e.scope;
  validateBinding(b); assertNativeBinding(b, p);
  check(p.evidence === 'synthetic' && p.liveAdmission === false && n.protocol === PROFILE && n.scope.phase === 'baseline' && n.scope.caseRef === e.caseId && n.harness?.settings === settingsDigest('allow'), 'baseline_policy_scope');
  check(b.role === 'worker' && ['routerId', 'routeId', 'revision', 'epoch'].every(k => b[k] === p[k as keyof typeof p]) && digest(p) === x.policyDigest && digest(b) === x.bindingDigest && e.packetDigest === x.packetDigest && digest(scope) === x.scopeDigest, 'baseline_binding');
  exact(scope, ['id', 'phase', 'spec', 'boot', 'started', 'deadline', 'spent', 'state']);
  check(typeof scope.spec === 'string' && same(parseJSON(scope.spec), n.scope) && scope.id === n.scope.id && scope.phase === n.scope.phase && scope.boot === p.authority.boot && ['active', 'closed'].includes(scope.state), 'baseline_scope');
  check(time(scope.started) && time(scope.deadline) && scope.deadline === scope.started + n.scope.elapsedMs && Number.isSafeInteger(scope.spent) && scope.spent > 0 && scope.spent <= n.scope.maxInferenceAttempts, 'baseline_scope_limits');
  const deadline = Math.min(scope.deadline, b.expiresAt, b.leaseExpiresAt);
  for (const name of ['requests', 'decisions', 'receipts', 'pendingReservations', 'durableDecisionTimes', 'physicalRequests', 'scopeOperations', 'events']) check(Array.isArray(e[name]), 'baseline_evidence_array');
  const count = e.requests.length;
  check(count >= 2 && count <= Math.min(p.limits.requestCount, n.scope.maxInferenceAttempts) && e.decisions.length === count && e.receipts.length === count && e.durableDecisionTimes.length === count && e.pendingReservations.length === 0, 'baseline_request_count');
  check(e.physicalRequests.length === scope.spent && e.scopeOperations.length === scope.spent && e.exitCode === 0 && e.signal === null && e.localProcessExited === true, 'baseline_unresolved_work');

  const parsed = new NativeEvents();
  for (const event of e.events) { object(event); check(typeof event.raw === 'string', 'baseline_event_raw'); parsed.push(Buffer.from(event.raw + '\n')); }
  parsed.end(); check(same(parsed.values, e.events), 'baseline_event_record');
  // NativeEvents owns the native diagnostic schema; this state machine joins its steps to durable responses.
  const steps: typeof parsed.values[] = []; let step: typeof parsed.values | null = null;
  const partIds = new Set<string>(), messageIds = new Set<string>(); let lastTime = scope.started;
  for (const event of parsed.values) {
    const native = event.native, part = native.part;
    check(event.type !== 'error' && time(native.timestamp) && native.timestamp >= lastTime && native.timestamp < deadline, 'baseline_native_failure');
    lastTime = native.timestamp;
    check(native.sessionID === e.requests[0].body?.prompt_cache_key && typeof part.id === 'string' && /^[a-zA-Z0-9_-]{1,128}$/.test(part.id) && !partIds.has(part.id), 'baseline_event_identity');
    partIds.add(part.id);
    if (event.type === 'step_start') {
      check(step === null && typeof part.messageID === 'string' && /^[a-zA-Z0-9_-]{1,128}$/.test(part.messageID) && !messageIds.has(part.messageID), 'baseline_step_order');
      messageIds.add(part.messageID); step = [];
    }
    check(step !== null && (step!.length === 0 || part.messageID === step![0].native.part.messageID), 'baseline_step_order');
    step!.push(event);
    if (event.type === 'step_finish') { steps.push(step!); step = null; }
  }
  check(step === null && steps.length === count, 'baseline_step_count');

  let previous: { request: any; output: any[] } | null = null, physicalIndex = 0;
  const requestIds = new Set<string>(), outputIds = new Set<string>(), callIds = new Set<string>(), physicalIds = new Set<string>();
  const requestDigests: string[] = [], decisionDigests: string[] = [], receiptDigests: string[] = [];
  let finalText = '';
  for (let i = 0; i < count; i++) {
    const r = e.requests[i], d = e.decisions[i], persisted = e.durableDecisionTimes[i];
    exact(r, ['path', 'headers', 'body', 'rawBody', 'startedAt', 'requestId', 'status', 'firstResponseAt', 'endedAt']);
    check(typeof r.requestId === 'string' && /^[a-f0-9]{32}$/.test(r.requestId) && !requestIds.has(r.requestId), 'baseline_request_identity');
    requestIds.add(r.requestId);
    check(r.path === '/v1/responses' && r.status === 200 && [r.startedAt, r.firstResponseAt, r.endedAt].every(time) && r.startedAt >= (i ? e.requests[i - 1].endedAt : scope.started) && r.startedAt <= r.firstResponseAt && r.firstResponseAt <= r.endedAt && r.endedAt < deadline, 'baseline_request_order');
    check(typeof r.rawBody === 'string' && Buffer.byteLength(r.rawBody) <= p.limits.requestBytes && JSON.stringify(parseJSON(r.rawBody)) === JSON.stringify(r.body), 'baseline_body_mismatch');
    object(r.headers); check(!Object.hasOwn(r.headers, 'authorization') && Object.values(r.headers).every(v => typeof v === 'string'), 'baseline_headers');
    // Authorization was stripped by the relay; a fixed placeholder checks only its reviewed metadata shape.
    const headers = validateHeaders(Object.entries({ ...(r.headers as Record<string, string>), authorization: 'Bearer synthetic' }).flat(), r.body, 'synthetic', r.rawBody);
    check(same(headers, r.headers), 'baseline_headers');
    const body = validateNativeRequest(r.body, n, p.routerModel, previous);
    if (i === 0) check(same(body.input, [{ role: 'developer', content: x.context }, { role: 'user', content: [{ type: 'input_text', text: x.prompt }] }]), 'baseline_prompt_context');
    exact(d, ['requestId', 'taskId', 'attemptId', 'router', 'verdict', 'evidence', 'completionDigest', 'delivery', 'nativeOutput']);
    exact(d.router, ['policy', 'binding', 'requestDigest', 'send', 'nativeRequest']);
    exact(persisted, ['requestId', 'at']);
    check(d.requestId === r.requestId && d.taskId === b.taskId && d.attemptId === b.attemptId && d.verdict === 'validated_success' && d.delivery === 'completed' && sha(d.completionDigest) && d.router.send === 'send_possible' && same(d.router.policy, p) && same(d.router.binding, b) && same(d.router.nativeRequest, body) && d.router.requestDigest === hashDocument(JSON.stringify(body)), 'baseline_decision');
    check(persisted.requestId === r.requestId && time(persisted.at) && persisted.at >= r.startedAt && persisted.at <= r.firstResponseAt, 'baseline_release_before_durable');
    const evidence = classifyReceipt(e.receipts[i], r.requestId, d.router);
    check(evidence.disposition === 'original_success' && evidence.operations.length > 0 && same(evidence, d.evidence), 'baseline_receipt_unknown');
    continuationOutput(d.nativeOutput, n);
    check(evidence.operations.at(-1)?.output_digest === digest(d.nativeOutput), 'baseline_output_digest');
    for (const item of d.nativeOutput) {
      check(!outputIds.has(item.id), 'baseline_output_identity'); outputIds.add(item.id);
      if (item.type === 'function_call') { check(!callIds.has(item.call_id), 'baseline_call_identity'); callIds.add(item.call_id); }
    }
    for (const [j, op] of evidence.operations.entries()) {
      const physical = e.physicalRequests[physicalIndex], debit = e.scopeOperations[physicalIndex++];
      exact(physical, ['request_id', 'ordinal', 'scope_id', 'body']);
      exact(debit, ['request_id', 'ordinal', 'scope_id', 'kind', 'body_digest', 'output_digest']);
      const identity = `${op.request_id}:${op.ordinal}:${op.scope_id}`;
      check(!physicalIds.has(identity) && ['request_id', 'ordinal', 'scope_id'].every(k => physical[k] === op[k as keyof typeof op] && debit[k] === op[k as keyof typeof op]), 'baseline_physical_identity');
      physicalIds.add(identity);
      check(same(physical.body, expectedPhysicalRequest(body, op.model, x.physicalInstructions)) && op.body_digest === digest(physical.body) && debit.kind === 'inference' && debit.body_digest === op.body_digest && debit.output_digest === op.output_digest, 'baseline_physical_debit');
      check(j === evidence.operations.length - 1 ? op.terminal === 'provider_completed' : ['provider_failed', 'provider_incomplete', 'provider_rejected'].includes(op.terminal) && op.output_digest === null, 'baseline_physical_terminal');
    }

    const events = steps[i], calls = d.nativeOutput.filter((item: any) => item.type === 'function_call');
    const tools = events.filter(event => event.type === 'tool_use');
    const isFinal = i === count - 1;
    check(events.every(event => event.native.timestamp >= persisted.at) && events.at(-1)!.native.part.reason === (isFinal ? 'stop' : 'tool-calls') && (isFinal ? calls.length === 0 && d.nativeOutput.some((item: any) => item.type === 'message') : calls.length > 0) && tools.length === calls.length, 'baseline_native_terminal');
    if (!isFinal) check(events.at(-1)!.native.timestamp <= e.requests[i + 1].startedAt, 'baseline_step_request_order');
    for (let j = 0; j < calls.length; j++) {
      const call = calls[j], tool = tools[j].native.part;
      const results = e.requests[i + 1].body.input.slice(-calls.length), result = results[j];
      check(tool.state.status === 'completed' && tool.callID === call.call_id && same(tool.state.input, validatePatch(call.arguments, n)) && result.type === 'function_call_output' && result.call_id === call.call_id && result.output === tool.state.output, 'baseline_tool_result');
      exact(tool.state.time, ['start', 'end']);
      check(time(tool.state.time.start) && time(tool.state.time.end) && tool.state.time.start >= persisted.at && tool.state.time.end >= tool.state.time.start && tool.state.time.end <= tools[j].native.timestamp && tool.state.time.end <= e.requests[i + 1].startedAt, 'baseline_tool_before_durable');
    }
    const outputText = d.nativeOutput.filter((item: any) => item.type === 'message').flatMap((item: any) => item.content.map((part: any) => part.text)).join('');
    check(events.filter(event => event.type === 'text').map(event => event.native.part.text).join('') === outputText, 'baseline_text_mismatch');
    if (isFinal) { check(outputText.length > 0, 'baseline_final_text'); finalText = outputText; }
    previous = { request: body, output: d.nativeOutput };
    requestDigests.push(d.router.requestDigest); decisionDigests.push(digest(d)); receiptDigests.push(evidence.receiptDigest!);
  }
  check(physicalIndex === scope.spent && physicalIds.size === scope.spent && callIds.size > 0, 'baseline_scope_debit_count');
  return { schema: 1 as const, origin: 'synthetic' as const, caseId: 'case01' as const,
    packetDigest: e.packetDigest as string, policyDigest: x.policyDigest as string, bindingDigest: x.bindingDigest as string, scopeDigest: x.scopeDigest as string,
    promptDigest: digest(x.prompt), contextDigest: digest(x.context), requestIds: [...requestIds], requestDigests, decisionDigests, receiptDigests,
    callIds: [...callIds], physicalAttempts: physicalIndex, finalText, eventsDigest: digest(parsed.values), transcriptDigest: digest(e) };
}
