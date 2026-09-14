import test from 'node:test';
import assert from 'node:assert/strict';
import { canonical } from '../inference-boundary/json.ts';
import { DEFAULT_BUDGET, PlannerSession } from './planner.ts';
import { ProbeError, sha256 } from './reader.ts';
import { BRIEF, proposal } from './public-fixture.ts';
import { REPAIR_INSTRUCTION } from './transport.ts';
import { NativePlannerTransport, NATIVE_PLANNER_BUDGET, NATIVE_PLANNER_PROTOCOL, NATIVE_PLANNER_TOOL, type ReleasedNativeCompletion } from './native-transport.ts';
import { nativeRead, nativeResponse, nativeText, setupNative, type NativeResponse } from './native-fixture.ts';
const packet = (body: any) => JSON.parse(body.input[1].content);
const valid = (body: any, read = false) => nativeText(JSON.stringify(proposal(packet(body).input_revision, read)));
const pending = <T>() => { let resolve!: (value: T) => void; const promise = new Promise<T>(r => { resolve = r; }); return { promise, resolve }; };

function assertNativeRequest(body: any, tools: boolean) {
  assert.deepEqual(Object.keys(body).sort(), ['model', 'input', 'stream', 'store', 'reasoning', 'include', 'tools', 'tool_choice', 'prompt_cache_key'].sort());
  assert.equal(body.model, 'gaffer-planner-native'); assert.equal(body.stream, true); assert.equal(body.store, false);
  assert.deepEqual(body.reasoning, { effort: 'xhigh', summary: 'auto' }); assert.deepEqual(body.include, ['reasoning.encrypted_content']);
  assert.deepEqual(body.tools, [NATIVE_PLANNER_TOOL]); assert.equal(body.tool_choice, tools ? 'auto' : 'none');
  assert.equal(body.prompt_cache_key, 'ses_planner_fixture1');
  for (const field of ['max_tokens', 'max_output_tokens', 'max_completion_tokens', 'response_format', 'previous_response_id', 'instructions']) assert(!Object.hasOwn(body, field));
}

test('native direct final keeps exact settings and a schema-3 redacted projection', async () => {
  const f = await setupNative([body => valid(body)]), result = await f.planner.run();
  assert.equal(result.outcome, 'plan_proposed'); assert.equal(result.authority, 'proposal_only');
  assert.deepEqual(result.usage, { assessments: 1, repairs: 0, requests: 1, files: 0, readBytes: 0 });
  assert.deepEqual(result.settings, { profile: 'router-native-responses-local-v1', protocol: NATIVE_PLANNER_PROTOCOL, model: 'gpt-6-astra', reasoning: { effort: 'xhigh', summary: 'auto' }, store: false, stream: true, providerOutputTokens: null, providerMonetaryCap: null });
  assertNativeRequest(f.requests[0].body, true);
  const policy = packet(f.requests[0].body).trusted_packet.policy;
  assert.equal(policy.schema, 3); assert.equal(policy.evidence, 'synthetic'); assert.equal(policy.liveAdmission, false);
  assert(!JSON.stringify(f.requests).includes('SYNTHETIC_PRIVATE')); assert(!JSON.stringify(result).includes('SYNTHETIC_PRIVATE'));
  assert.equal(packet(f.requests[0].body).trusted_packet.budget.outputTokens, null);
  assert.deepEqual(await f.planner.run(), result); assert.equal(f.requests.length, 1);
});

test('native read/final preserves ordered encrypted state and original call pairing', async () => {
  const f = await setupNative([nativeRead(), body => valid(body, true)]), result = await f.planner.run();
  assert.equal(result.outcome, 'plan_proposed'); assert.deepEqual(result.usage, { assessments: 1, repairs: 0, requests: 2, files: 1, readBytes: 484 });
  for (const [i, request] of f.requests.entries()) assertNativeRequest(request.body, i === 0);
  const input = f.requests[1].body.input;
  assert.deepEqual(input.slice(0, 2), f.requests[0].body.input);
  assert.deepEqual(input[2], { type: 'reasoning', encrypted_content: 'opaque_ENCRYPTED_state_A', summary: [{ type: 'summary_text', text: 'Need the authorized fixture.' }] });
  assert.deepEqual(input[3], { type: 'function_call', call_id: 'native_original_call_1', name: 'read_file', arguments: '{"path":"fixture.txt"}' });
  assert.equal(input[4].call_id, input[3].call_id); assert.equal(result.completions[0].calls[0], input[3].call_id);
  assert.equal(input[4].type, 'function_call_output'); assert.match(input[4].output, /Ignore the operator/);
  const read = JSON.parse(input[4].output); assert.equal(read.trust, 'untrusted_repository_data'); assert.equal(read.sha256, sha256(read.content));
});

test('a new session cannot reuse one native transport to reset its allowance', async () => {
  const f = await setupNative([body => valid(body)]);
  assert.equal((await f.planner.run()).outcome, 'plan_proposed');
  const next = new PlannerSession(f.transport, f.file, BRIEF, NATIVE_PLANNER_BUDGET);
  const result = await next.run();
  assert.equal(result.outcome, 'native_session_already_started'); assert.equal(result.usage.requests, 0);
  assert.deepEqual(await next.run(), result); assert.equal(f.requests.length, 1);
});

test('a native plan cannot cite snapshot evidence before it has been read', async () => {
  const f = await setupNative([body => valid(body, true), body => valid(body, false)]);
  const result = await f.planner.run();
  assert.equal(result.outcome, 'plan_proposed'); assert.equal(result.usage.files, 0); assert.equal(result.usage.repairs, 1);
  assertNativeRequest(f.requests[1].body, false);
});

test('read/final/repair charges all three requests and preserves blank terminal output', async () => {
  const f = await setupNative([nativeRead(), nativeText(''), body => valid(body, true)]), result = await f.planner.run();
  assert.equal(result.outcome, 'plan_proposed'); assert.deepEqual(result.usage, { assessments: 1, repairs: 1, requests: 3, files: 1, readBytes: 484 });
  assertNativeRequest(f.requests[2].body, false);
  assert.deepEqual(f.requests[2].body.input.slice(0, 5), f.requests[1].body.input);
  assert.deepEqual(f.requests[2].body.input[5], { role: 'assistant', content: [{ type: 'output_text', text: '' }] });
  assert.deepEqual(f.requests[2].body.input[6], { role: 'user', content: REPAIR_INSTRUCTION });
});

test('only one repair is allowed for invalid, blank, stale and duplicate-key plans', async t => {
  const revision = '0'.repeat(64), stale = proposal(revision, false);
  for (const [name, failed] of [ ['malformed', nativeText('bad')], ['empty-output', nativeResponse([])], ['whitespace', nativeText(' ')], ['stale', nativeText(JSON.stringify(stale))], ['duplicate-key', nativeText(JSON.stringify(stale).replace('"schema":1', '"schema":1,"schema":1'))] ] as const) {
    await t.test(name, async () => {
      const f = await setupNative([failed, nativeText('still invalid'), body => valid(body)]), result = await f.planner.run();
      assert.equal(result.outcome, 'invalid_plan'); assert.equal(result.usage.repairs, 1); assert.equal(f.requests.length, 2);
      assertNativeRequest(f.requests[1].body, false); assert.deepEqual(await f.planner.run(), result); assert.equal(f.requests.length, 2);
    });
  }
});

test('native discovery rejects disabled, duplicate, escaped, extra-property and worker tool calls before a read', async t => {
  const worker = nativeRead(); (worker.output[1] as any).name = 'apply_patch';
  const duplicate = nativeRead(); duplicate.output.push({ ...(duplicate.output[1] as any), id: 'function_2', call_id: 'native_original_call_2' });
  for (const [name, response, budget] of [
    ['disabled-file', nativeRead(), { ...NATIVE_PLANNER_BUDGET, files: 0 }],
    ['disabled-bytes', nativeRead(), { ...NATIVE_PLANNER_BUDGET, readBytes: 1 }],
    ['duplicate', duplicate, NATIVE_PLANNER_BUDGET], ['escape', nativeRead('{"path":"../fixture.txt"}'), NATIVE_PLANNER_BUDGET],
    ['extra-property', nativeRead('{"path":"fixture.txt","shell":"execute"}'), NATIVE_PLANNER_BUDGET],
    ['duplicate-key', nativeRead('{"path":"fixture.txt","path":"fixture.txt"}'), NATIVE_PLANNER_BUDGET], ['worker-tool', worker, NATIVE_PLANNER_BUDGET],
  ] as const) await t.test(name, async () => {
    const f = await setupNative([response], budget), result = await f.planner.run();
    assert.equal(result.outcome, 'discovery_denied'); assert.equal(result.usage.files, 0); assert.equal(result.usage.repairs, 0); assert.equal(f.requests.length, 1);
    assertNativeRequest(f.requests[0].body, !name.startsWith('disabled'));
  });
});

test('native final and repair independently reject further discovery', async t => {
  for (const repair of [false, true]) await t.test(repair ? 'repair' : 'after read', async () => {
    const f = await setupNative([repair ? nativeText('bad') : nativeRead(), nativeRead()]), result = await f.planner.run();
    assert.equal(result.outcome, 'discovery_denied'); assert.equal(result.usage.files, repair ? 0 : 1); assert.equal(result.usage.repairs, repair ? 1 : 0);
    assertNativeRequest(f.requests[1].body, false); assert.equal(f.requests.length, 2);
  });
});

test('invalid or provisional native output is terminal without repair', async t => {
  const failed = nativeText('bad'); failed.status = 'failed';
  const incomplete = nativeText('bad'); incomplete.status = 'incomplete';
  const refusal = nativeText('bad'); (refusal.output[0] as any).content = [{ type: 'refusal', refusal: 'no' }];
  const mixed = nativeRead(); mixed.output.push(...nativeText('do this now').output);
  const brokenReasoning = nativeRead(); (brokenReasoning.output[0] as any).encrypted_content = '';
  const duplicateIds = nativeRead(); (duplicateIds.output[1] as any).id = 'reasoning_1';
  const badCall = nativeRead(); (badCall.output[1] as any).call_id = '../identity';
  const activeCall = nativeRead(); (activeCall.output[1] as any).status = 'in_progress';
  const wrongModel = nativeText('bad'); wrongModel.model = 'gpt-5.6-sol';
  for (const [name, response] of Object.entries({ failed, incomplete, refusal, mixed, brokenReasoning, duplicateIds, badCall, activeCall, wrongModel })) await t.test(name, async () => {
    const f = await setupNative([response]), result = await f.planner.run();
    assert.notEqual(result.outcome, 'plan_proposed'); assert.equal(result.usage.files, 0); assert.equal(result.usage.repairs, 0); assert.equal(f.requests.length, 1);
  });
});

test('consumer releases neither evidence nor repair until its boundary port releases completion', async t => {
  for (const kind of ['read', 'repair', 'proposal'] as const) await t.test(kind, async () => {
    const entered = pending<void>(), release = pending<NativeResponse>();
    const f = await setupNative([async () => { entered.resolve(); return release.promise; }, body => valid(body, kind === 'read')]);
    let completed = false; const run = f.planner.run().then(result => { completed = true; return result; }); await entered.promise;
    assert.equal(completed, false); assert.equal(f.requests.length, 1); assert.deepEqual(f.releases, []);
    release.resolve(kind === 'read' ? nativeRead() : kind === 'repair' ? nativeText('invalid') : valid(f.requests[0].body));
    const result = await run; assert.equal(result.outcome, 'plan_proposed'); assert.equal(result.usage.files, kind === 'read' ? 1 : 0); assert.equal(result.usage.repairs, kind === 'repair' ? 1 : 0);
  });
});

test('boundary refusal, unknown work, original receipt and persistence failures never trigger repair or replacement', async t => {
  // These are injected port rejections, not router/SQLite receipt-conformance tests.
  for (const code of ['route_unavailable', 'invalid_stream', 'partial_failure', 'receipt_unverified', 'receipt_mismatched', 'persistence_failed', 'lease_expired', 'unknown_original_work']) await t.test(code, async () => {
    const f = await setupNative([new ProbeError(code), body => valid(body)]), result = await f.planner.run();
    assert.equal(result.outcome, code); assert.equal(result.usage.files, 0); assert.equal(result.usage.repairs, 0); assert.equal(f.requests.length, 1);
    assert.deepEqual(await f.planner.run(), result); assert.equal(f.requests.length, 1);
  });
});

test('native cancellation before request and at delayed release is terminal', async t => {
  await t.test('before dispatch', async () => {
    const f = await setupNative([nativeRead()]), abort = new AbortController(); abort.abort();
    assert.equal((await f.planner.run(abort.signal)).outcome, 'cancelled'); assert.equal(f.requests.length, 0);
  });
  await t.test('after request before release', async () => {
    const entered = pending<void>(), release = pending<NativeResponse>();
    const f = await setupNative([async () => { entered.resolve(); return release.promise; }]), abort = new AbortController();
    const run = f.planner.run(abort.signal); await entered.promise; abort.abort();
    const result = await run; assert.equal(result.outcome, 'cancelled_unknown'); assert.equal(result.usage.files, 0); assert.equal(result.usage.repairs, 0);
    assert.deepEqual(await f.planner.run(), result); assert.equal(f.requests.length, 1);
    release.resolve(nativeRead());
  });
});

test('native deadline is fixed and cannot be reset by invocation or boundary release', async context => {
  const entered = pending<void>(), release = pending<NativeResponse>();
  const f = await setupNative([async () => { entered.resolve(); return release.promise; }], { ...NATIVE_PLANNER_BUDGET, totalMs: 25 });
  context.mock.timers.enable({ apis: ['setTimeout'] });
  try {
    const run = f.planner.run(); await entered.promise; context.mock.timers.tick(25);
    const result = await run; assert.equal(result.outcome, 'time_budget_exhausted_unknown'); assert.equal(result.usage.files, 0);
    assert.deepEqual(await f.planner.run(), result); assert.equal(f.requests.length, 1);
    release.resolve(nativeRead());
  } finally { context.mock.timers.reset(); }
});

test('full native private policy identity participates in revision while settings and policy are immutable copies', async () => {
  const f = await setupNative();
  const first = f.planner.inputRevision;
  const original = f.transport.policy; original.native.fixtureSourceIdentity = 'outside mutation';
  assert.notEqual(original.native.fixtureSourceIdentity, f.transport.policy.native.fixtureSourceIdentity);
  f.policy.native.fixtureSourceIdentity = 'different_private_source';
  const transport = new NativePlannerTransport(f.port), changed = new PlannerSession(transport, f.file, BRIEF, NATIVE_PLANNER_BUDGET);
  assert.notEqual(changed.inputRevision, first);
  f.replies.push(body => valid(body)); const result = await f.planner.run(); assert.equal(result.outcome, 'native_profile_drift'); assert.equal(f.requests.length, 0);
});

test('worker profile, role, schema, path and cap substitution are rejected at native consumer selection', async t => {
  for (const [name, mutate] of Object.entries({
    workerProtocol: (f: any) => { f.policy.native.protocol = 'opencode-1.18.30-responses-apply-patch-v1'; },
    workerRole: (f: any) => { f.binding.role = 'worker'; }, tool: (f: any) => { f.policy.native.tools[0].name = 'apply_patch'; },
    path: (f: any) => { f.policy.native.toolPaths = ['outside.txt']; }, effort: (f: any) => { f.policy.native.authorization.effort = 'high'; },
    model: (f: any) => { f.policy.native.authorization.model = 'gpt-5.6-sol'; }, cap: (f: any) => { f.policy.native.authorization.providerOutput.requirement = 'required'; },
    schema: (f: any) => { f.policy.schema = 2; }, impersonation: (f: any) => { f.binding.sessionId = 'ses_opencode'; },
  })) await t.test(name, async () => {
    const f = await setupNative(); mutate(f); assert.throws(() => new NativePlannerTransport(f.port), /native_profile_denied|unsupported_provider_output_cap/); assert.equal(f.requests.length, 0);
  });
});

test('settings, tool, encrypted continuation and call/result drift are rejected before another port call', async t => {
  for (const [name, mutate] of Object.entries({
    settings: (body: any) => { body.reasoning.effort = 'high'; }, tools: (body: any) => { body.tools[0].name = 'apply_patch'; },
    encrypted: (body: any) => { body.input[2].encrypted_content += 'tampered'; },
    call: (body: any) => { body.input[3].call_id = 'different'; }, result: (body: any) => { body.input[4].call_id = 'different'; },
    session: (body: any) => { body.prompt_cache_key = 'ses_planner_replacement'; },
  })) await t.test(name, async () => {
    const f = await setupNative([nativeRead()]);
    const conversation = f.transport.conversation([{ role: 'system', content: 'restricted' }, { role: 'user', content: 'fixture' }], NATIVE_PLANNER_BUDGET);
    const first = conversation.request(true), completion = await f.transport.complete(first, new AbortController().signal, 32768, true);
    conversation.calls(completion); conversation.read(completion.calls[0], '{"fixture":"data"}');
    const next = conversation.request(false) as any; mutate(next);
    await assert.rejects(f.transport.complete(next, new AbortController().signal, 32768, false), /native_history_denied/); assert.equal(f.requests.length, 1);
  });
});

test('native budgets remain finite, explicit and distinct from unavailable provider caps', async t => {
  assert.equal(DEFAULT_BUDGET.totalMs, 5000); assert.equal(DEFAULT_BUDGET.outputTokens, 1024); assert.equal(NATIVE_PLANNER_BUDGET.totalMs, 5000);
  const f = await setupNative(); assert.throws(() => new PlannerSession(f.transport, f.file, BRIEF), /unsupported_provider_output_cap/);
  assert.throws(() => new PlannerSession(f.transport, f.file, BRIEF, { ...NATIVE_PLANNER_BUDGET, totalMs: 5001 }), /invalid_budget/);
  for (const [field, value, outcome, requests] of [ ['assessments', 0, 'assessment_budget_exhausted', 0], ['requests', 0, 'request_budget_exhausted', 0], ['requestBytes', 1, 'request_budget_exhausted', 0], ['responseBytes', 1, 'response_budget_exhausted', 1], ['requests', 1, 'request_budget_exhausted', 1], ['repairs', 0, 'invalid_plan', 1] ] as const) await t.test(field + '=' + value, async () => {
    const f = await setupNative([field === 'requests' ? nativeRead() : nativeText('invalid')], { ...NATIVE_PLANNER_BUDGET, [field]: value }), result = await f.planner.run();
    assert.equal(result.outcome, outcome); assert.equal(f.requests.length, requests); assert(result.usage.requests <= 3);
  });
});

test('untrusted repository demands cannot change the native contract or dispatch authority', async () => {
  const f = await setupNative([nativeRead(), body => nativeText(JSON.stringify({ ...proposal(packet(body).input_revision), route: 'direct-provider', execution: 'approved', shell: 'run', requests: 99 }))], { ...NATIVE_PLANNER_BUDGET, repairs: 0 });
  const original = canonical(f.policy), result = await f.planner.run();
  assert.equal(result.outcome, 'invalid_plan'); assert.equal(result.proposal, undefined); assert.equal(result.authority, 'proposal_only'); assert.equal(canonical(f.policy), original);
  assertNativeRequest(f.requests[1].body, false); assert.equal(f.requests.length, 2);
});

test('release metadata is checked and policy drift at release prevents read or repair', async t => {
  for (const kind of ['identity', 'acceptedAt', 'drift', 'exception', 'unsafe-code'] as const) await t.test(kind, async () => {
    const f = await setupNative();
    f.port.complete = async () => {
      if (kind === 'drift') f.binding.sessionId = 'ses_planner_changed';
      if (kind === 'exception') throw new Error('SYNTHETIC_PRIVATE_ERROR');
      if (kind === 'unsafe-code') throw new ProbeError('SYNTHETIC_PRIVATE_ERROR');
      return { requestId: kind === 'identity' ? 'unsafe identity' : 'a'.repeat(32), acceptedAt: kind === 'acceptedAt' ? Date.now() + 100000 : Date.now(), response: nativeRead() } satisfies ReleasedNativeCompletion;
    };
    const result = await f.planner.run(); assert.notEqual(result.outcome, 'plan_proposed'); assert.equal(result.usage.files, 0); assert.equal(result.usage.repairs, 0); assert(!JSON.stringify(result).includes('SYNTHETIC_PRIVATE'));
  });
});
