// Pure evaluation-scope shape and limit validation; no authority or persistence.
const canonical = x => JSON.stringify(x, (_, v) => v && typeof v === 'object' && !Array.isArray(v) ? Object.fromEntries(Object.keys(v).sort().map(k => [k,v[k]])) : v);
const check = (v, why) => { if (!v) throw new Error(why); };
const ref = x => typeof x === 'string' && /^[a-z][a-z0-9_-]{0,63}$/.test(x);
const exact = (v, names) => check(v && typeof v === 'object' && !Array.isArray(v) && canonical(Object.keys(v).sort()) === canonical(names.sort()), 'invalid_evaluation_scope');
export function validateScope(spec) {
  exact(spec, ['id','caseRef','phase','authorizationDigest','maxInferenceAttempts','maxRefreshOperations','elapsedMs']);
  check(ref(spec.id) && ref(spec.caseRef) && typeof spec.authorizationDigest==='string' && /^[a-f0-9]{64}$/.test(spec.authorizationDigest), 'invalid_evaluation_scope');
  check(['initial','baseline'].includes(spec.phase), 'invalid_evaluation_phase');
  const max = spec.phase === 'initial' ? [10,600000] : [32,900000];
  check(Number.isSafeInteger(spec.maxInferenceAttempts) && spec.maxInferenceAttempts > 0 && spec.maxInferenceAttempts <= max[0], 'invalid_evaluation_attempts');
  check(Number.isSafeInteger(spec.elapsedMs) && spec.elapsedMs > 0 && spec.elapsedMs <= max[1], 'invalid_evaluation_deadline');
  // Initial implementation is an explicit no-refresh profile, not an untracked allowance.
  check(spec.maxRefreshOperations === 0, 'refresh_not_authorized');
  return structuredClone(spec);
}
