import { readFileSync } from 'node:fs';
import { canonical, keys, object, parseJSON } from './json.ts';

export const OPENCODE_PROFILE = 'opencode-1.18.30-chat-edit-v1' as const;
export type ProtocolProfile = 'chat-text-tools-v1' | typeof OPENCODE_PROFILE | 'router-chat-text-tools-synthetic-v1' | 'router-native-chat-translation-synthetic-v1';
// Exact public binary request, not a general-purpose JSON Schema validator.
export const OPENCODE_TOOLS: unknown = JSON.parse(readFileSync(new URL('./opencode-tools.json', import.meta.url), 'utf8'));
export const FIXTURE_PATH = '/work/repo/greeting.txt';
export function validateArguments(name: string, text: unknown, profile: ProtocolProfile) {
  if (typeof text !== 'string' || Buffer.byteLength(text) > 16384) throw new Error('unsupported_tool');
  const args = parseJSON(text); object(args);
  if (profile !== OPENCODE_PROFILE) {
    if (name !== 'read_file' || canonical(args) !== canonical({ path: 'fixture.txt' })) throw new Error('unsupported_tool');
    return;
  }
  if (args.filePath !== FIXTURE_PATH) throw new Error('unsupported_path');
  const bounded = (value: unknown) => typeof value === 'string' && Buffer.byteLength(value) <= 4096 && !value.includes('\0');
  if (name === 'edit') {
    keys(args, ['filePath', 'oldString', 'newString', 'replaceAll'], ['filePath', 'oldString', 'newString']);
    if (!bounded(args.oldString) || !bounded(args.newString) || !args.oldString || args.oldString === args.newString || args.replaceAll !== undefined && args.replaceAll !== false) throw new Error('unsupported_edit');
  } else if (name === 'write') {
    keys(args, ['filePath', 'content'], ['filePath', 'content']);
    if (!bounded(args.content)) throw new Error('unsupported_write');
  } else throw new Error('unsupported_tool');
}
