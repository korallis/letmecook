import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fixturePolicy, validatePolicy } from '../inference-boundary/types.ts';
import { ChatStream, validateRequest } from '../inference-boundary/protocol.ts';
import { OPENCODE_PROFILE, validateArguments } from '../inference-boundary/profiles.ts';
const request = () => ({ ...JSON.parse(readFileSync(new URL('../../tests/fixtures/harness/request.json', import.meta.url), 'utf8')), model: 'gaffer-coding' });
const policy = () => ({ ...fixturePolicy(), profile: OPENCODE_PROFILE });
const frame = (delta: unknown, finish_reason: string | null = null) => `data: ${JSON.stringify({ choices: [{ index: 0, delta, finish_reason }] })}\n\n`;
function stream(text: string, profile = OPENCODE_PROFILE) { const s = new ChatStream('a'.repeat(32), 'gaffer-coding', true, profile); const part = s.push(Buffer.from(text)); if (part.error) throw part.error; return [...part.output, ...s.end()].join(''); }
test('captured OpenCode request requires explicit separately pinned policy; legacy remains strict', () => {
  validatePolicy(policy()); assert.doesNotThrow(() => validateRequest(request(), policy()));
  assert.throws(() => validateRequest(request(), fixturePolicy()));
  assert.throws(() => validatePolicy({ ...policy(), profile: 'opencode-any' } as any));
  for (const extra of [{ max_completion_tokens: 128 }, { reasoning_effort: 'xhigh' }, { provider: 'direct' }, { stream_options: { include_usage: false } }, { max_tokens: 4096 }]) assert.throws(() => validateRequest({ ...request(), ...extra }, policy()));
  const altered = request(); altered.tools[0].function.parameters.properties.filePath.enum = ['/etc/passwd']; assert.throws(() => validateRequest(altered, policy()));
});
test('tool arguments reject unsupported effects and permit only bounded fixture operations', () => {
  const good = { filePath: '/work/repo/greeting.txt', oldString: 'hello', newString: 'hi' };
  validateArguments('edit', JSON.stringify(good), OPENCODE_PROFILE);
  for (const args of [{ ...good, filePath: '/etc/passwd' }, { ...good, filePath: '/work/repo/../home/auth.json' }, { ...good, replaceAll: true }, { ...good, shell: 'x' }, { ...good, newString: 'a'.repeat(4097) }]) assert.throws(() => validateArguments('edit', JSON.stringify(args), OPENCODE_PROFILE));
  assert.throws(() => validateArguments('bash', '{}', OPENCODE_PROFILE));
  assert.throws(() => validateArguments('edit', '{"filePath":"/work/repo/greeting.txt","filePath":"/etc/passwd"}', OPENCODE_PROFILE));
});
test('complete split tool calls release only at validated terminal stream; usage is sanitized', () => {
  const tool = frame({ tool_calls: [{ index: 0, id: 'call_edit_1', type: 'function', function: { name: 'edit', arguments: '{"filePath":"/work/repo/greeting.txt",' } }] }) + frame({ tool_calls: [{ index: 0, function: { arguments: '"oldString":"hello","newString":"hi"}' } }] }) + frame({}, 'tool_calls');
  const usage = `data: ${JSON.stringify({ choices: [], usage: { prompt_tokens: 17, completion_tokens: 9, total_tokens: 26 }, private: 'must-not-pass' })}\n\n`;
  const valid = tool + usage + 'data: [DONE]\n\n';
  const s = new ChatStream('a'.repeat(32), 'gaffer-coding', true, OPENCODE_PROFILE);
  for (const byte of Buffer.from(valid)) { const result = s.push(Buffer.of(byte)); assert.equal(result.error, undefined); assert.equal(result.output.length, 0); }
  const output = s.end().join(''); assert.match(output, /call_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa_0/); assert.match(output, /"total_tokens":26/); assert.doesNotMatch(output, /must-not-pass/);
  assert.throws(() => stream(tool));
  assert.throws(() => stream(valid.replace('/work/repo/greeting.txt', '/etc/passwd')));
  assert.throws(() => stream(valid.replace('"total_tokens":26', '"total_tokens":0')));
  assert.throws(() => stream(tool + usage + usage + 'data: [DONE]\n\n'));
  assert.doesNotMatch(stream(tool + 'data: [DONE]\n\n'), /usage/);
});
