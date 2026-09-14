// Bound the byte input before parsing. JSON.parse alone silently accepts duplicate keys.
export function parseJSON(text: string, allowReservedKeys = false): unknown {
  let i = 0;
  const bad = (): never => { throw new Error('invalid_json'); };
  const space = () => { while (/[ \t\r\n]/.test(text[i] ?? 'x')) i++; };
  const string = (): string => {
    const start = i++;
    while (i < text.length) {
      if (text[i] === '\\') { i += 2; continue; }
      if (text[i++] === '"') {
        try { return JSON.parse(text.slice(start, i)); } catch { return bad(); }
      }
    }
    return bad();
  };
  const value = (depth: number): unknown => {
    if (depth > 32) return bad();
    space();
    if (text[i] === '"') return string();
    if (text[i] === '{') {
      i++; space();
      const result: Record<string, unknown> = Object.create(null);
      if (text[i] === '}') { i++; return result; }
      while (true) {
        space(); if (text[i] !== '"') return bad();
        const key = string();
        if (Object.hasOwn(result, key) || !allowReservedKeys && ['__proto__', 'constructor', 'prototype'].includes(key)) return bad();
        space(); if (text[i++] !== ':') return bad();
        result[key] = value(depth + 1); space();
        const separator = text[i++];
        if (separator === '}') return result;
        if (separator !== ',') return bad();
      }
    }
    if (text[i] === '[') {
      i++; space(); const result: unknown[] = [];
      if (text[i] === ']') { i++; return result; }
      while (true) {
        result.push(value(depth + 1)); space();
        const separator = text[i++];
        if (separator === ']') return result;
        if (separator !== ',') return bad();
      }
    }
    const match = /^(?:true|false|null|-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?)/.exec(text.slice(i));
    if (!match) return bad();
    i += match[0].length;
    const result = JSON.parse(match[0]);
    if (typeof result === 'number' && !Number.isFinite(result)) return bad();
    return result;
  };
  const result = value(0); space();
  if (i !== text.length) return bad();
  return result;
}

export function canonical(value: unknown): string {
  if (Array.isArray(value)) return '[' + value.map(canonical).join(',') + ']';
  if (value !== null && typeof value === 'object') {
    const object = value as Record<string, unknown>;
    return '{' + Object.keys(object).sort().map(key => JSON.stringify(key) + ':' + canonical(object[key])).join(',') + '}';
  }
  return JSON.stringify(value);
}

export function object(value: unknown): asserts value is Record<string, any> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('invalid_object');
}
export function keys(value: unknown, allowed: string[], required: string[] = []) {
  object(value);
  if (Object.keys(value).some(key => !allowed.includes(key)) || required.some(key => !Object.hasOwn(value, key))) throw new Error('unsupported_field');
}
