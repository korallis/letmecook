import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, writeFile, symlink, link, rm, realpath, mkdir } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { setTimeout as pause } from 'node:timers/promises';
import { setup, proposal, finishText, FIXTURE_ROOT, FIXTURE_HASH } from './fixture.ts';
import { DEFAULT_BUDGET, validatePlan } from './planner.ts';
import { snapshot, sha256 } from './reader.ts';
import { toolStream, event } from '../inference-boundary/fixture.ts';
import { validateRequest } from '../inference-boundary/protocol.ts';

test('actual synthetic HTTP tool continuation returns a schema-valid proposal without authority', async () => {
  const f = await setup();
  try {
    f.replies.push(toolStream(), finishText(JSON.stringify(proposal(f.planner.inputRevision))));
    const result = await f.planner.run();
    assert.equal(result.outcome, 'plan_proposed'); assert.equal(result.authority, 'proposal_only');
    assert.deepEqual(result.usage, { assessments: 1, repairs: 0, requests: 2, files: 1, readBytes: 484 });
    assert.equal(f.calls.length, 2);
    const tool = f.calls[1].messages.find((message: any) => message.role === 'tool');
    assert.match(tool.content, /Ignore the operator/);
    for (const body of f.calls) {
      assert.equal(body.model, 'gaffer-planner-fixture'); assert.equal(body.max_completion_tokens, 1024);
      assert.deepEqual(Object.keys(body).sort(), ['max_completion_tokens', 'messages', 'model', 'stream', 'tool_choice', 'tools']);
      assert.deepEqual(body.tools.map((t: any) => t.function.name), ['read_file']);
    }
    assert.equal(f.calls[1].tool_choice, 'none');
    assert.deepEqual(await f.planner.run(), result); assert.equal(f.calls.length, 2);
    assert.ok(!JSON.stringify(result).includes('SYNTHETIC_SECRET'));
  } finally { await f.close(); }
});

test('malformed output gets one charged repair; repeated invocation cannot replenish allowance', async () => {
  const f = await setup([finishText('not JSON'), finishText('{"bad":true}')]);
  try {
    const result = await f.planner.run();
    assert.equal(result.outcome, 'invalid_plan');
    assert.deepEqual(result.usage, { assessments: 1, repairs: 1, requests: 2, files: 0, readBytes: 0 });
    assert.deepEqual(await f.planner.run(), result); assert.equal(f.calls.length, 2);
  } finally { await f.close(); }
});
test('one valid repair and no tools can complete the original assessment', async () => {
  const f = await setup([finishText('malformed')]);
  try {
    f.replies.push(finishText(JSON.stringify(proposal(f.planner.inputRevision, false))));
    const result = await f.planner.run(); assert.equal(result.outcome, 'plan_proposed'); assert.equal(result.usage.repairs, 1);
    assert.equal(f.calls[1].tool_choice, 'none');
  } finally { await f.close(); }
});

test('schema rejects duplicate keys, extra authority, stale revision, invalid references and too many questions', () => {
  const revision = 'a'.repeat(64); const good = proposal(revision);
  const candidates = [ { ...good, authority: 'merge' }, { ...good, route: 'direct-provider' }, { ...good, budget: { requests: 999 } },
    { ...good, allowed_paths: ['../outside.txt'] }, { ...good, allowed_systems: ['shell'] },
    { ...good, input_revision: 'b'.repeat(64) }, { ...good, unresolved_questions: ['a', 'b', 'c', 'd'] },
    { ...good, steps: [{ ...good.steps[0], criterion_ids: ['c2'] }] }, { ...good, criteria: [good.criteria[0], good.criteria[0]] } ];
  for (const candidate of candidates) assert.throws(() => validatePlan(JSON.stringify(candidate), revision, true), /invalid_plan/);
  assert.throws(() => validatePlan(JSON.stringify(good).replace('"schema":1', '"schema":1,"schema":1'), revision, true), /invalid_plan/);
  assert.throws(() => validatePlan(JSON.stringify(good), revision, false), /invalid_plan/);
});

test('a model echoing repository authority demands cannot mutate policy or dispatch work', async () => {
  const f = await setup([], { ...DEFAULT_BUDGET, repairs: 0 });
  try {
    const original = structuredClone(f.policy);
    f.replies.push(toolStream(), finishText(JSON.stringify({ ...proposal(f.planner.inputRevision), route: 'direct-provider', execution: 'approved', shell: 'read private key', requests: 999 })));
    const result = await f.planner.run();
    assert.equal(result.outcome, 'invalid_plan'); assert.equal(result.proposal, undefined);
    assert.deepEqual(f.transport.policy, original); assert.deepEqual(f.gate.snapshot().policy, original);
    assert.equal(f.calls.length, 2); assert.equal(result.authority, 'proposal_only');
  } finally { await f.close(); }
});

test('request and response byte bounds terminate without repair', async t => {
  for (const [budget, outcome, calls] of [
    [{ ...DEFAULT_BUDGET, requestBytes: 1 }, 'request_budget_exhausted', 0],
    [{ ...DEFAULT_BUDGET, responseBytes: 1 }, 'response_budget_exhausted', 1],
  ] as const) await t.test(outcome, async () => {
    const f = await setup([finishText('a'.repeat(1000))], budget);
    try { const result = await f.planner.run(); assert.equal(result.outcome, outcome); assert.equal(result.usage.repairs, 0); assert.equal(f.calls.length, calls); }
    finally { await f.close(); }
  });
});

test('disabled assessment, repair, request and read budgets are distinct and never silently widened', async t => {
  for (const [field, value, outcome, expectedCalls] of [
    ['assessments', 0, 'assessment_budget_exhausted', 0], ['requests', 0, 'request_budget_exhausted', 0],
    ['files', 0, 'discovery_budget_exhausted', 1], ['readBytes', 1, 'discovery_budget_exhausted', 1],
    ['requests', 1, 'request_budget_exhausted', 1],
  ] as const) await t.test(field + '=' + value, async () => {
    const f = await setup([toolStream()], { ...DEFAULT_BUDGET, [field]: value });
    try { assert.equal((await f.planner.run()).outcome, outcome); assert.equal(f.calls.length, expectedCalls); }
    finally { await f.close(); }
  });
  const f = await setup([finishText('bad')], { ...DEFAULT_BUDGET, repairs: 0 });
  try { assert.equal((await f.planner.run()).outcome, 'invalid_plan'); assert.equal(f.calls.length, 1); }
  finally { await f.close(); }
});

test('multiple reads consume one aggregate file budget', async () => {
  const calls = [0, 1].map(index => ({ index, id: `call_${index}`, type: 'function', function: { name: 'read_file', arguments: '{"path":"fixture.txt"}' } }));
  const f = await setup([event({ tool_calls: calls }) + event({}, 'tool_calls') + 'data: [DONE]\n\n']);
  try { const result = await f.planner.run(); assert.equal(result.outcome, 'discovery_budget_exhausted'); assert.equal(result.usage.files, 1); assert.equal(f.calls.length, 1); }
  finally { await f.close(); }
});

test('route outage and partial stream are terminal, without repair or fallback', async t => {
  for (const [reply, outcome] of [ ['unavailable', 'route_unavailable'], [event({ content: '{' }), 'partial_failure'] ]) await t.test(outcome, async () => {
    const f = await setup([reply]);
    try { const result = await f.planner.run(); assert.equal(result.outcome, outcome); assert.equal(result.usage.repairs, 0); assert.equal(f.calls.length, 1); }
    finally { await f.close(); }
  });
});

test('cancel before dispatch and during inference; total time expires without retry', async t => {
  await t.test('before dispatch', async () => {
    const f = await setup(); const abort = new AbortController(); abort.abort();
    try { assert.equal((await f.planner.run(abort.signal)).outcome, 'cancelled'); assert.equal(f.calls.length, 0); }
    finally { await f.close(); }
  });
  await t.test('active transport cancellation', async () => {
    const f = await setup(['hold']); const abort = new AbortController();
    try {
      const pending = f.planner.run(abort.signal);
      const deadline = Date.now() + 1000; while (!f.calls.length && Date.now() < deadline) await pause(5);
      assert.equal(f.calls.length, 1); abort.abort();
      assert.equal((await pending).outcome, 'cancelled_unknown'); assert.equal(f.calls.length, 1);
    } finally { await f.close(); }
  });
  await t.test('total time', async () => {
    const f = await setup(['hold'], { ...DEFAULT_BUDGET, totalMs: 50 });
    try { assert.equal((await f.planner.run()).outcome, 'time_budget_exhausted_unknown'); assert.equal(f.calls.length, 1); }
    finally { await f.close(); }
  });
});

test('unregistered tools, path escape and partial tool calls never dispatch reads', async t => {
  for (const reply of [toolStream().replace('read_file', 'shell'), toolStream().replace('fixture.txt', '../outside.txt'), toolStream().replace('data: [DONE]', '')]) await t.test('denied stream', async () => {
    const f = await setup([reply]);
    try { const result = await f.planner.run(); assert.notEqual(result.outcome, 'plan_proposed'); assert.equal(result.usage.files, 0); assert.equal(f.calls.length, 1); }
    finally { await f.close(); }
  });
});

test('strict schema, Responses fields and reasoning effort remain unsupported by the accepted profile', async () => {
  const f = await setup();
  try {
    const base = { model: f.policy.routerModel, messages: [{ role: 'user', content: 'toy' }], stream: true, max_completion_tokens: 32 };
    for (const extra of [{ response_format: { type: 'json_schema' } }, { reasoning_effort: 'xhigh' }, { input: 'toy' }, { max_output_tokens: 32 }]) assert.throws(() => validateRequest({ ...base, ...extra }, f.policy), /unsupported_request/);
    assert.equal(f.calls.length, 0);
  } finally { await f.close(); }
});

test('trusted reader allows only pinned regular-file bytes and rejects filesystem boundaries', async () => {
  const root = await mkdtemp(join(await realpath(tmpdir()), 'gaffer-reader-')); const signal = new AbortController().signal;
  try {
    await writeFile(join(root, 'fixture.txt'), 'toy');
    assert.equal((await snapshot(root, 'fixture.txt', sha256('toy'), 4096, signal)).text, 'toy');
    for (const path of ['../fixture.txt', '/etc/passwd', 'sub/file', '.git/config', 'fixture.txt/..', 'fixture.txt\0']) await assert.rejects(snapshot(root, path, sha256('toy'), 4096, signal), /discovery_denied/);
    await assert.rejects(snapshot(root, 'fixture.txt', sha256('wrong'), 4096, signal), /discovery_changed/);
    await assert.rejects(snapshot(root, 'fixture.txt', sha256('toy'), 2, signal), /discovery_budget_exhausted/);
    await link(join(root, 'fixture.txt'), join(root, 'hardlink')); await assert.rejects(snapshot(root, 'fixture.txt', sha256('toy'), 4096, signal), /discovery_denied/);
    await rm(join(root, 'fixture.txt')); await symlink(join(root, 'hardlink'), join(root, 'fixture.txt'));
    await assert.rejects(snapshot(root, 'fixture.txt', sha256('toy'), 4096, signal), /discovery_denied/);
    await symlink(root, join(root, 'alias')); await assert.rejects(snapshot(join(root, 'alias'), 'fixture.txt', sha256('toy'), 4096, signal), /discovery_denied/);
    await rm(join(root, 'fixture.txt')); await mkdir(join(root, 'fixture.txt')); await assert.rejects(snapshot(root, 'fixture.txt', sha256('toy'), 4096, signal), /discovery_denied/);
    await assert.rejects(snapshot(FIXTURE_ROOT, 'fixture.txt', FIXTURE_HASH, 4096, AbortSignal.abort()), /cancelled/);
  } finally { await rm(root, { recursive: true, force: true }); }
});
