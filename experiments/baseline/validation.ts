import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';

export type Row = Record<string, any>;
export function exact(value: unknown, fields: string): asserts value is Row {
  assert(value && typeof value === 'object' && !Array.isArray(value), 'record_required');
  assert.deepEqual(Object.keys(value).sort(), fields.split(' ').sort(), 'closed_record_fields');
}
export function list(value: unknown, minimum = 0, maximum = 10000): asserts value is any[] {
  assert(Array.isArray(value) && value.length >= minimum && value.length <= maximum, 'bounded_array');
}
export function text(value: unknown, maximum = 4096): asserts value is string {
  assert(typeof value === 'string' && value.trim().length > 0 && value.length <= maximum && !value.includes('\0'), 'bounded_text');
}
export function id(value: unknown) { text(value, 64); assert(/^[a-z][a-z0-9_-]*$/.test(value), 'invalid_id'); }
export function sha(value: unknown, length = 64) { assert(typeof value === 'string' && new RegExp(`^[a-f0-9]{${length}}$`).test(value), 'invalid_digest'); }
export function number(value: unknown, minimum = 0, maximum = Number.MAX_SAFE_INTEGER): asserts value is number {
  assert(typeof value === 'number' && Number.isFinite(value) && value >= minimum && value <= maximum, 'invalid_number');
}
export function integer(value: unknown, minimum = 0, maximum = Number.MAX_SAFE_INTEGER) { number(value, minimum, maximum); assert(Number.isSafeInteger(value), 'integer_required'); }
export function oneOf(value: unknown, choices: readonly unknown[]) { assert(choices.includes(value), 'invalid_enum'); }
export function time(value: unknown): number {
  assert(typeof value === 'string' && /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$/.test(value), 'utc_time_required');
  const n = Date.parse(value); assert(Number.isFinite(n) && new Date(n).toISOString() === value, 'invalid_time'); return n;
}
export function reference(value: unknown) {
  exact(value, 'ref sha256'); text(value.ref, 512); sha(value.sha256);
}
export function nullableReference(value: unknown) { if (value !== null) reference(value); }
export function references(value: unknown, minimum = 0) { list(value, minimum); value.forEach(reference); }
export function unique(values: any[], field = 'id') { values.forEach(v => id(v[field])); assert.equal(new Set(values.map(v => v[field])).size, values.length, 'duplicate_identity'); }
export function path(value: unknown) {
  text(value, 256); assert(!value.startsWith('/') && !/[\\\s]/.test(value) && value.split('/').every(x => x && !['.', '..', '.git'].includes(x)), 'unsafe_path');
}
export function canonical(value: unknown): string {
  return JSON.stringify(value, (_, v) => v && typeof v === 'object' && !Array.isArray(v) ? Object.fromEntries(Object.keys(v).sort().map(k => [k, v[k]])) : v);
}
export const digest = (value: unknown) => createHash('sha256').update(canonical(value)).digest('hex');
