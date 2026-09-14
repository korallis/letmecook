import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { OPENCODE_PROFILE, OPENCODE_ROUTER_PROFILE, openCodeTools, validateArguments } from '../inference-boundary/profiles.ts';
import { validateRouterPolicy, hashDocument } from '../inference-boundary/router-policy.ts';
import { routerPolicyFixture } from '../inference-boundary/router-fixture.ts';
import { ChatStream, validateRequest } from '../inference-boundary/protocol.ts';
import { canonical } from '../inference-boundary/json.ts';
import { config } from './adapter.ts';
const captures = JSON.parse(readFileSync(new URL('../../tests/fixtures/harness/usage-disabled.requests.json', import.meta.url), 'utf8'));
function policy() {
  const p = routerPolicyFixture(); assert(p.schema === 2); p.profile = OPENCODE_ROUTER_PROFILE; p.graph.profile = '9router-0.5.75-synthetic-opencode-edit-v1'; p.authority.graphDigest = hashDocument(p.graph); p.limits.outputTokens = 128;
  p.envelope.consumer = 'opencode-1.18.30-usage-disabled-edit-v1'; p.envelope.cap = 'max_tokens-preserved-v1'; return p;
}
test('actual usage-disabled binary captures preserve cap, choice, exact tools and empty assistant tool history', () => {
  const p = policy(); validateRouterPolicy(p);
  for (const capture of captures) {
    const request = { ...capture, model: p.routerModel };
    assert.equal(canonical(JSON.parse(validateRequest(request, p))), canonical(request));
    assert.equal(request.max_tokens, 128); assert.equal(request.tool_choice, 'auto'); assert(!Object.hasOwn(request, 'stream_options'));
    assert.equal(canonical(request.tools), canonical(openCodeTools()));
    for (const native of [false, true]) assert.throws(() => validateRequest(request, routerPolicyFixture(native)));
  }
  assert.equal(captures[1].messages.find((m: any) => m.role === 'assistant').content, '');
  assert.equal(config('f'.repeat(64), 'allow', p).provider.scoped.options.includeUsage, false);
  assert(!Object.hasOwn(config('f'.repeat(64), 'allow').provider.scoped.options, 'includeUsage'));
});
test('separate graph/envelope profile refuses native, common and mismatched cap declarations', () => {
  for (const change of [(p: any) => p.graph.profile = '9router-0.5.75-synthetic-compatible-chat-v1', (p: any) => p.profile = 'router-native-chat-translation-synthetic-v1', (p: any) => p.envelope.cap = 'max_completion_tokens-to-max_tokens-v1', (p: any) => p.envelope.consumer = 'chat-read-file-v1']) {
    const p = policy(); change(p); p.authority.graphDigest = hashDocument(p.graph); assert.throws(() => validateRouterPolicy(p));
  }
});
test('OpenCode router profile refuses added settings, schema mutations, unsupported history and tool authority', () => {
  const p = policy(), request = { ...captures[0], model: p.routerModel };
  const mutations: ((r: any) => void)[] = [
    ...['none', 'required', null, { type: 'function', function: { name: 'edit' } }].map(v => (r: any) => r.tool_choice = v),
    r => delete r.tool_choice, r => r.stream_options = { include_usage: true }, r => r.reasoning_effort = 'xhigh', r => r.temperature = 0,
    r => r.tools[0].function.strict = true, r => r.tools[0].function.description += ' altered', r => r.tools = [], r => delete r.tools,
    r => r.max_tokens = 129, r => r.max_completion_tokens = 128,
    r => r.messages.push({ role: 'assistant', content: '' }), r => r.messages.push({ role: 'assistant', content: null }),
  ];
  for (const mutate of mutations) { const r = structuredClone(request); mutate(r); assert.throws(() => validateRequest(r, p)); }
  const continuation = { ...captures[1], model: p.routerModel };
  for (const mutate of [(r: any) => r.messages.pop(), (r: any) => r.messages.at(-1).tool_call_id = 'call_orphan', (r: any) => r.messages.find((m: any) => m.tool_calls).tool_calls[0].function.arguments = '{"filePath":"/work/home/auth.json","content":"bad"}']) { const r = structuredClone(continuation); mutate(r); assert.throws(() => validateRequest(r, p)); }
  for (const profile of [OPENCODE_PROFILE, OPENCODE_ROUTER_PROFILE]) for (const args of ['{"filePath":"/work/repo/greeting.txt","oldString":"a","oldString":"b","newString":"c"}', '{"filePath":"/work/repo/greeting.txt","oldString":"a","newString":"b","replaceAll":true}']) assert.throws(() => validateArguments('edit', args, profile));
});
test('receipt-gated OpenCode codec admits exact edit/write but keeps missing usage unknown and rejects usage-only frames', () => {
  const frame = (delta: unknown, finish_reason: unknown = null) => `data: ${JSON.stringify({ choices: [{ index: 0, delta, finish_reason }] })}\n\n`;
  const valid = frame({ tool_calls: [{ index: 0, id: 'call_edit', type: 'function', function: { name: 'edit', arguments: '{"filePath":"/work/repo/greeting.txt","oldString":"hello","newString":"hello from harness"}' } }] }) + frame({}, 'tool_calls');
  const stream = new ChatStream('request', 'model', true, 'router-done', OPENCODE_ROUTER_PROFILE);
  assert.deepEqual(stream.push(Buffer.from(valid + 'data: [DONE]\n\ndata: [DONE]\n\n')).output, []);
  const output = stream.end().join(''); assert.match(output, /call_request_0/); assert.doesNotMatch(output, /usage/);
  const invalid = new ChatStream('request', 'model', true, 'router-done', OPENCODE_ROUTER_PROFILE);
  assert(invalid.push(Buffer.from(valid + 'data: {"choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}\n\n')).error);
});
