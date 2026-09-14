// Local physical-operation authority, deliberately separate from usage/quota.
// Uses the router's existing synchronous FULL SQLite transaction and boot fence.
import { createHash } from 'node:crypto';
const canonical = x => JSON.stringify(x, (_, v) => v && typeof v === 'object' && !Array.isArray(v) ? Object.fromEntries(Object.keys(v).sort().map(k => [k,v[k]])) : v);
const hash = x => createHash('sha256').update(canonical(x)).digest('hex');
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
export function evaluationScopes(db, boot, { wall = Date.now, mono = () => performance.now() } = {}) {
  db.exec(`CREATE TABLE IF NOT EXISTS gaffer_scopes (id TEXT PRIMARY KEY, phase TEXT NOT NULL, spec TEXT NOT NULL, boot TEXT NOT NULL, started INTEGER NOT NULL, deadline INTEGER NOT NULL, spent INTEGER NOT NULL, state TEXT NOT NULL);
    CREATE UNIQUE INDEX IF NOT EXISTS gaffer_initial_scope ON gaffer_scopes(phase) WHERE phase='initial';
    CREATE UNIQUE INDEX IF NOT EXISTS gaffer_case_scope ON gaffer_scopes(json_extract(spec,'$.caseRef'));
    CREATE TABLE IF NOT EXISTS gaffer_scope_operations (request_id TEXT NOT NULL, ordinal INTEGER NOT NULL, scope_id TEXT NOT NULL, kind TEXT NOT NULL, body_digest TEXT NOT NULL, output_digest TEXT, PRIMARY KEY(request_id,ordinal));`);
  // Elapsed clocks cannot be reconstructed across boots. Recovery preserves the
  // budget and closes it; a restart never resumes or renews the initial allowance.
  db.run("UPDATE gaffer_scopes SET state='closed' WHERE state='active'");
  const clocks = new Map();
  const read = id => db.get('SELECT * FROM gaffer_scopes WHERE id=?',[id]);
  function register(input, quiescent) {
    const spec = validateScope(input), now = wall();
    check(Number.isSafeInteger(now) && now > 0, 'invalid_clock');
    return db.transaction(() => {
      check(quiescent() && !db.get("SELECT id FROM gaffer_scopes WHERE state='active'"), 'evaluation_serial_scope');
      check(!read(spec.id), 'evaluation_scope_already_used');
      db.run("INSERT INTO gaffer_scopes VALUES(?,?,?,?,?,?,0,'active')",[spec.id,spec.phase,canonical(spec),boot,now,now+spec.elapsedMs]);
      clocks.set(spec.id,{start:mono(),lastWall:now,lastMono:mono()});
      return read(spec.id);
    });
  }
  function assertCurrent(id, authorizationDigest, tokenDeadline = Infinity) {
    const row = read(id), clock = clocks.get(id), now = wall(), current = mono();
    check(row && row.boot === boot && row.state === 'active' && clock, 'evaluation_scope_closed');
    const spec = JSON.parse(row.spec);
    check(spec.authorizationDigest === authorizationDigest, 'evaluation_authorization_mismatch');
    // Monotonic elapsed is primary. Forward wall jumps close early; backwards
    // movement closes rather than extend either the scope or token validity.
    if(!(Number.isFinite(current) && current >= clock.lastMono && now >= clock.lastWall)){clocks.delete(id);db.run("UPDATE gaffer_scopes SET state='closed' WHERE id=?",[id]);throw Error('evaluation_clock_changed');}
    clock.lastWall = now; clock.lastMono = current;
    if(!(current - clock.start < spec.elapsedMs && now < row.deadline && now < tokenDeadline)){clocks.delete(id);db.run("UPDATE gaffer_scopes SET state='closed' WHERE id=?",[id]);throw Error('evaluation_deadline');}
    return {row,spec,remainingMs:Math.min(spec.elapsedMs-(current-clock.start),row.deadline-now,tokenDeadline-now)};
  }
  function debit({id,authorizationDigest,tokenDeadline,requestId,ordinal,kind,bodyDigest}) {
    // Caller MUST include this in the operation's transaction before network.
    const {row,spec} = assertCurrent(id,authorizationDigest,tokenDeadline);
    check(kind === 'inference', 'refresh_not_authorized');
    check(row.spent < spec.maxInferenceAttempts, 'evaluation_attempt_limit');
    check(typeof bodyDigest==='string' && /^[a-f0-9]{64}$/.test(bodyDigest), 'invalid_physical_body_digest');
    db.run('UPDATE gaffer_scopes SET spent=spent+1 WHERE id=?',[id]);
    db.run('INSERT INTO gaffer_scope_operations(request_id,ordinal,scope_id,kind,body_digest) VALUES(?,?,?,?,?)',[requestId,ordinal,id,kind,bodyDigest]);
  }
  function close(id, quiescent) {
    return db.transaction(() => { check(read(id) && quiescent(), 'evaluation_unknown'); db.run("UPDATE gaffer_scopes SET state='closed' WHERE id=?",[id]); });
  }
  return Object.freeze({register,assertCurrent,debit,close,read,digest:spec=>hash(validateScope(spec))});
}
