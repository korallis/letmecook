// Exact captured OpenCode 1.18.30 tools, independently versioned compatible ingress.
import { createHash } from 'node:crypto';
import { strictJSON } from './chat-terminal.mjs';
export const OPENCODE_TOOLS_DIGEST = 'e34702b3918045a84911b7900abfc4626fdd2c50b1e638d50573b5c6c776d277';
const canonical = value => JSON.stringify(value, (_, v) => v && typeof v === 'object' && !Array.isArray(v) ? Object.fromEntries(Object.keys(v).sort().map(k => [k,v[k]])) : v);
const requireThat = (value) => { if (!value) throw new Error('unsupported_opencode_envelope'); };
export function validateOpenCodeTools(tools) {
  requireThat(createHash('sha256').update(canonical(tools)).digest('hex') === OPENCODE_TOOLS_DIGEST);
}
export function validateOpenCodeArguments(name, text) {
  requireThat(typeof text === 'string' && Buffer.byteLength(text) <= 16384);
  const a = strictJSON(text, 4);
  requireThat(a && typeof a === 'object' && !Array.isArray(a) && a.filePath === '/work/repo/greeting.txt');
  const bounded = v => typeof v === 'string' && Buffer.byteLength(v) <= 4096 && !v.includes('\0');
  if (name === 'edit') {
    requireThat(Object.keys(a).every(k => ['filePath','oldString','newString','replaceAll'].includes(k)) && bounded(a.oldString) && bounded(a.newString) && a.oldString && a.oldString !== a.newString && (a.replaceAll === undefined || a.replaceAll === false));
  } else {
    requireThat(name === 'write' && Object.keys(a).every(k => ['filePath','content'].includes(k)) && bounded(a.content));
  }
}
