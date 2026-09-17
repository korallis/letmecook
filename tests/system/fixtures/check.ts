// Validates the tests/system/fixtures corpus: corpus-v1.json, fault-matrix-v1.json,
// recovery-100-v1.json, repos/make-repos.sh and repos/gen-recovery.ts.
// Node 24+, no dependencies. Run from the repository root:
//   node tests/system/fixtures/check.ts
// This is a structural and cross-reference check of fixture data only. It is
// not an execution harness and no check here constitutes evidence of a run.
import assert from 'node:assert/strict';
import { accessSync, constants, readFileSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';

const here = (p: string): string => new URL(p, import.meta.url).pathname;
const REPOS = ['greeting', 'calc', 'notes'] as const;
const OUTCOMES = ['succeeded', 'failed_verification', 'failed', 'stopped', 'retry_then_succeeded', 'approval_blocked'] as const;
const OPERATIONS = ['edit', 'create', 'delete', 'rename'] as const;
// Fake modes defined by the fake-harness contract; edit-applying modes plus
// fault modes. crash_after_edit is an alias contract extension kept permissive.
const APPLY_MODES = ['edit', 'binary_edit', 'create_empty', 'delete'];
const FAULT_MODES = ['crash', 'approval', 'ignore_term', 'huge_output', 'exit_nonzero', 'detached_child', 'hang', 'crash_after_edit', 'noop'];
const VERIFICATION_PROFILES: Record<string, string[]> = {
  greeting: ['greeting-echo-v1'],
  calc: ['calc-go-v1'],
  notes: ['notes-links-v1'],
};

type Row = Record<string, unknown>;
function record(value: unknown, what: string): asserts value is Row {
  assert(value !== null && typeof value === 'object' && !Array.isArray(value), `${what}: expected record`);
}
function text(value: unknown, what: string): string {
  assert(typeof value === 'string' && value.trim().length > 0, `${what}: required text missing`);
  return value;
}

// --- canonical JSON: no trailing whitespace, single trailing newline ---------
function checkCanonical(name: string, bytes: string): void {
  const endsWithNewline = bytes.endsWith('\n');
  assert(endsWithNewline, `${name}: must end with exactly one newline`);
  const body = endsWithNewline ? bytes.slice(0, -1) : bytes;
  assert(!body.endsWith('\n') && !/[ \t]+(\n|$)/.test(body), `${name}: trailing whitespace or blank lines present`);
  JSON.parse(bytes); // must parse
  // Re-serialize must round-trip (no duplicate keys, valid JSON).
  assert.equal(JSON.stringify(JSON.parse(bytes), null, 2) + '\n', bytes, `${name}: not canonical 2-space JSON`);
}

// --- corpus task validation --------------------------------------------------
function validateEdit(edit: Row, task: Row, taskId: string): string[] {
  const keys = Object.keys(edit);
  const path = text(edit.path, `${taskId} edit path`);
  assert(!path.startsWith('/') && !path.includes('..'), `${taskId}: edit path must be repository-relative: ${path}`);
  const hasContent = 'content' in edit;
  const hasB64 = 'content_base64' in edit;
  const hasDelete = edit.delete === true;
  assert(hasContent + (hasB64 ? 1 : 0) + (hasDelete ? 1 : 0) === 1, `${taskId}: edit must carry exactly one of content, content_base64, delete`);
  if (hasB64) {
    const b64 = text(edit.content_base64, `${taskId} content_base64`);
    assert(/^[A-Za-z0-9+/]+={0,2}$/.test(b64) && Buffer.from(b64, 'base64').toString('base64') === b64, `${taskId}: invalid base64`);
  }
  if (hasContent) assert(typeof edit.content === 'string', `${taskId}: content must be a string`);
  assert(keys.every(k => ['path', 'content', 'content_base64', 'delete'].includes(k)), `${taskId}: unexpected edit keys ${keys.join(',')}`);
  return [path];
}

function validateAttempt(att: Row, task: Row, taskId: string, attemptNo: number): string[] {
  const mode = text(att.mode, `${taskId} attempt ${attemptNo} mode`);
  assert([...APPLY_MODES, ...FAULT_MODES].includes(mode), `${taskId}: unknown fake mode ${mode}`);
  assert(Array.isArray(att.edits) && att.edits.length > 0, `${taskId} attempt ${attemptNo}: edits required`);
  const paths: string[] = [];
  for (const e of att.edits) { record(e, `${taskId} attempt ${attemptNo} edit`); paths.push(...validateEdit(e, task, taskId)); }
  if (mode === 'create_empty') {
    for (const e of att.edits) assert(e.content === '', `${taskId}: create_empty edit must have empty content`);
  }
  if (mode === 'binary_edit') {
    for (const e of att.edits) {
      record(e, `${taskId} edit`);
      if ('content_base64' in e) continue;
      if (e.path.endsWith('.png')) assert.fail(`${taskId}: binary_edit on ${e.path} must use content_base64`);
    }
  }
  if (mode === 'huge_output') {
    assert(typeof att.stream_bytes === 'number' && att.stream_bytes! > 0, `${taskId}: huge_output requires stream_bytes > 0`);
  }
  return paths;
}

function validateVerification(v: Row, repo: string, taskId: string, repoChecks: string[]): void {
  assert.deepEqual(Object.keys(v).sort(), ['commands', 'profile'], `${taskId}: verification keys`);
  assert(VERIFICATION_PROFILES[repo]!.includes(text(v.profile, `${taskId} verification.profile`)), `${taskId}: profile ${v.profile} is not repo ${repo}'s`);
  const cmds = v.commands as Row[];
  assert(Array.isArray(cmds) && cmds.length === repoChecks.length, `${taskId}: verification.commands length`);
  cmds.forEach((c, i) => {
    assert.deepEqual(Object.keys(c).sort(), ['expect', 'index'], `${taskId}: command keys`);
    assert.equal(c.index, i, `${taskId}: command index must be dense`);
    if (c.expect === 'pass' || c.expect === 'fail' || c.expect === 'not_run') return;
    assert.fail(`${taskId}: expect must be pass, fail or not_run`);
  });
}

function validateCorpus(raw: string): void {
  const corpus: Row = JSON.parse(raw);
  assert.equal(corpus.version, 1);
  assert.equal(corpus.fixture, 'corpus-v1');
  const tasks = corpus.tasks as Row[];
  assert.equal(tasks.length, 20, 'corpus must have exactly 20 tasks');
  const ids = tasks.map(t => text(t.id, 'task id'));
  assert.equal(new Set(ids).size, 20, 'task ids must be unique');
  assert.deepEqual(ids, Array.from({ length: 20 }, (_, i) => `task-${String(i + 1).padStart(2, '0')}`), 'ids must be task-01..task-20');
  const usedRepos = new Set<string>();
  const opencodes: string[] = [];
  for (const t of tasks) {
    const id = text(t.id, 'task id');
    const repo = text(t.repo, `${id} repo`);
    assert((REPOS as readonly string[]).includes(repo), `${id}: unknown repo ${repo}`);
    usedRepos.add(repo);
    const brief = text(t.brief, `${id} brief`);
    assert(brief.length <= 400, `${id}: brief exceeds 400 chars (${brief.length})`);
    assert(/[A-Z]/.test(brief[0] ?? '') || /^[a-z]/.test(brief), `${id}: brief should be imperative text`);
    const criteria = t.criteria as Row[];
    assert(Array.isArray(criteria) && criteria.length > 0, `${id}: criteria required`);
    for (const c of criteria) { record(c, `${id} criterion`); text(c.id, `${id} criterion id`); text(c.text, `${id} criterion text`); }
    const paths = t.paths as string[];
    assert(Array.isArray(paths) && paths.length > 0, `${id}: paths required`);
    for (const p of paths) assert(typeof p === 'string' && !p.startsWith('/') && !p.includes('..'), `${id}: bad path ${p}`);
    const ops = t.operations as string[];
    assert(ops.length > 0 && ops.every(o => (OPERATIONS as readonly string[]).includes(o)), `${id}: bad operations`);
    const harness = t.harness as string[];
    assert(harness.includes('fake'), `${id}: fake harness required`);
    if (harness.includes('opencode')) opencodes.push(id);
    const spec = t.fake_spec as Row;
    record(spec, `${id} fake_spec`);
    const attempts = spec.attempts as Row[];
    assert(Array.isArray(attempts) && attempts.length > 0, `${id}: fake_spec.attempts required`);
    const editPaths = new Set<string>();
    attempts.forEach((a, i) => { record(a, `${id} attempt`); validateAttempt(a, t, id, i + 1).forEach(p => editPaths.add(p)); });
    // every declared path must be used by some edit, and vice versa
    for (const p of paths) assert(editPaths.has(p), `${id}: declared path ${p} matches no fake edit`);
    for (const p of editPaths) assert(paths.includes(p), `${id}: edit path ${p} missing from declared paths`);
    assert((OUTCOMES as readonly string[]).includes(t.expected_outcome as string), `${id}: bad expected_outcome ${t.expected_outcome}`);
    const outcome = t.expected_outcome as string;
    // outcome/mode coherence for the special rows
    if (outcome === 'retry_then_succeeded') assert(attempts.length >= 2 && attempts[0]!.mode === 'crash', `${id}: retry_then_succeeded needs crash first attempt`);
    if (outcome === 'stopped') assert(attempts.length === 1 && attempts[0]!.mode === 'hang', `${id}: stopped needs a single hang attempt`);
    if (outcome === 'approval_blocked') assert(attempts.length === 1 && attempts[0]!.mode === 'approval', `${id}: approval_blocked needs approval mode`);
    if (outcome === 'failed') {
      assert(attempts.length === 1 && attempts[0]!.mode === 'huge_output', `${id}: failed row must be the spool-cap case`);
      assert(((t.verification as Row).commands as Row[]).every(c => c.expect === 'not_run'), `${id}: failed row runs no verification`);
    }
    if (outcome === 'failed_verification') {
      const v = t.verification as Row;
      assert((v.commands as Row[]).some(c => c.expect === 'fail'), `${id}: failed_verification must expect a failing check`);
    }
    const repos2 = corpus.repos as Row;
    record(repos2, 'corpus.repos');
    const repoRow = repos2[repo] as Row;
    record(repoRow, `corpus.repos.${repo}`);
    validateVerification(t.verification as Row, repo, id, repoRow.checks as string[]);
    text(t.notes, `${id} notes`);
  }
  assert.equal(usedRepos.size, 3, 'corpus must use all three repos');
  assert.equal(opencodes.length, 3, `exactly 3 tasks must also be runnable as opencode (got ${opencodes.length}: ${opencodes.join(',')})`);
  // required coverage
  const modes = tasks.flatMap(t => (t.fake_spec as Row).attempts as Row[]).map(a => a.mode);
  const flat = tasks.flatMap((t, ti) => ((t.fake_spec as Row).attempts as Row[]).map(a => ({ id: `task-${String(ti + 1).padStart(2, '0')}`, mode: a.mode, t })));
  for (const need of ['edit', 'binary_edit', 'create_empty', 'delete', 'crash', 'approval', 'hang', 'huge_output']) {
    assert(flat.some(x => x.mode === need), `corpus must cover fake mode ${need}`);
  }
  const binary = flat.filter(x => x.mode === 'binary_edit');
  assert(binary.length >= 1, 'binary edit case required');
  const emptyCreate = flat.filter(x => x.mode === 'create_empty');
  assert(emptyCreate.length >= 1, 'empty-file case required');
  const multiFile = tasks.filter(t => ((t.fake_spec as Row).attempts as Row[])[0]!.mode === 'edit' && (((t.fake_spec as Row).attempts as Row[])[0]!.edits as Row[]).length >= 2);
  assert(multiFile.length >= 1, 'multi-file edit case required');
  assert(flat.filter(x => x.mode === 'delete').length >= 1, 'delete case required');
  // every outcome in the enum except rename-only variants is represented or deliberate
  const outcomes = new Set(tasks.map(t => t.expected_outcome as string));
  for (const o of OUTCOMES) assert(outcomes.has(o), `corpus must include expected_outcome ${o}`);
  // pinned base commits in corpus.repos must match the generator pins
  const pins: Record<string, string> = { greeting: '479eec44aa5cda0a5f91a48b2e5b6119c18570ba', calc: 'cd739784278eb03050235d38d07c327c7811b36b', notes: '82cabef608abfb716d1415eb14e9dcb2044f6712' };
  for (const [repo, pin] of Object.entries(pins)) {
    assert.equal((corpus.repos as Row)[repo]!.base_commit, pin, `corpus.repos.${repo}.base_commit must equal the make-repos pin`);
  }
}

function validateFaultMatrix(raw: string): void {
  const fm: Row = JSON.parse(raw);
  assert.equal(fm.fixture, 'fault-matrix-v1');
  const modes = fm.modes as Row[];
  const REQUIRED = ['crash', 'approval', 'ignore_term', 'huge_output', 'create_empty', 'delete', 'exit_nonzero', 'detached_child', 'hang'];
  assert.deepEqual(modes.map(m => text(m.mode, 'fault mode')), REQUIRED, 'fault matrix must contain exactly the S-11 rows in order');
  for (const m of modes) {
    record(m, 'fault row');
    text(m.scenario, `${m.mode} scenario`);
    text(m.attempt_state, `${m.mode} attempt_state`);
    text(m.task_state, `${m.mode} task_state`);
    assert(typeof m.reservation_released === 'boolean', `${m.mode}: reservation_released boolean`);
    text(m.remote_work, `${m.mode} remote_work`);
    text(m.receipt, `${m.mode} receipt`);
    text(m.expected, `${m.mode} expected`);
    const assertions = m.assertions as string[];
    assert(Array.isArray(assertions) && assertions.includes('invariants'), `${m.mode}: must assert the shared invariants`);
    const inv = fm.cross_mode_invariants as string[];
    assert(inv.length >= 5 && inv.every(i => typeof i === 'string' && i.length > 0), 'cross_mode_invariants required');
    for (const key of ['identity', 'revision', 'watermark', 'receipt']) {
      assert(inv.some(i => i.startsWith(`${key}:`)), `cross_mode_invariants must include ${key}`);
    }
    assert(inv.some(i => i.includes('no_second_active_attempt')), 'invariants must include no second active attempt');
  }
}

function validateRecovery(raw: string): void {
  const f: Row = JSON.parse(raw);
  assert.equal(f.fixture, 'recovery-100-v1');
  const tasks = f.tasks as Row[];
  assert.equal(tasks.length, 100, 'recovery fixture must have exactly 100 tasks');
  const ids = tasks.map(t => text(t.id, 'recovery task id'));
  assert.equal(new Set(ids).size, 100, 'recovery task ids must be unique');
  assert.deepEqual(ids, Array.from({ length: 100 }, (_, i) => `recovery-task-${String(i + 1).padStart(3, '0')}`), 'recovery ids must be dense 001..100');
  for (const t of tasks) {
    const id = t.id as string;
    assert((REPOS as readonly string[]).includes(t.repo as string), `${id}: unknown repo`);
    assert.deepEqual(t.harness, ['fake'], `${id}: recovery tasks use the fake harness only`);
    const atts = (t.fake_spec as Row).attempts as Row[];
    assert(atts.length >= 1 && atts.length <= 2, `${id}: 1 or 2 attempts`);
    if (t.expected_outcome === 'retry_then_succeeded') {
      assert.equal(atts.length, 2, `${id}: retry case has two attempts`);
      assert.equal(atts[0]!.mode, 'crash', `${id}: first attempt crash`);
      assert.equal(atts[1]!.mode, 'edit', `${id}: second attempt edit`);
      assert.deepEqual(atts[0]!.edits, atts[1]!.edits, `${id}: crash and retry attempts declare the same edit`);
    } else {
      assert.equal(t.expected_outcome, 'succeeded', `${id}: unexpected outcome`);
      assert.equal(atts.length, 1, `${id}: single attempt`);
      assert.equal(atts[0]!.mode, 'edit', `${id}: single edit attempt`);
    }
    const editPath = (atts[0]!.edits as Row[])[0]!.path as string;
    assert.deepEqual(t.paths, [editPath], `${id}: paths must equal the edit path`);
    for (const a of atts) for (const e of a.edits as Row[]) assert(Object.keys(e).every(k => ['path', 'content', 'content_base64', 'delete'].includes(k)), `${id}: bad edit keys`);
  }
  const rs = f.restart_schedule as Row;
  assert.equal(rs.kill_count, 10, 'exactly 10 kills');
  const kills = rs.kills as Row[];
  assert.equal(kills.length, 10, 'exactly 10 kill rows');
  kills.forEach((k, i) => {
    assert.equal(k.seq, i + 1, 'kill seq dense');
    assert.equal(k.signal, 'SIGKILL', 'SIGKILL kills');
    const idx = k.during_task_index as number;
    assert(Number.isInteger(idx) && idx >= 1 && idx <= 100, 'kill index in range');
    assert.equal(k.task_id, `recovery-task-${String(idx).padStart(3, '0')}`, 'kill task_id matches index');
  });
  const tm = f.timing_method as Row;
  const stamps = tm.timestamps_per_restart as Row[];
  assert(stamps.length >= 4, 'timing method needs the four timestamps');
  for (const s of stamps) { text(s.name, 'timestamp name'); text(s.source, 'timestamp source'); }
  assert((tm.metrics as Row[]).length >= 2, 'recovery_ms and classify_ms formulas required');
  text(tm.percentiles, 'percentile method');
  const barrier = text(tm.old_lease_barrier, 'old-lease barrier statement');
  assert(barrier.includes('30 s') || barrier.includes('30s'), 'the 30 s old-lease barrier must be explicitly included');
  assert(text(tm.unknown_is_blocking, 'unknown-is-blocking statement').includes('blocking'), 'unknown outcomes must be declared blocking');
}

// --- gen-recovery determinism ------------------------------------------------
function checkGenDeterminism(): void {
  const a = execFileSync(process.execPath, [here('repos/gen-recovery.ts'), '-'], { encoding: 'utf8', maxBuffer: 16 * 1024 * 1024 });
  const committed = readFileSync(here('recovery-100-v1.json'), 'utf8');
  assert.equal(a, committed, 'recovery-100-v1.json must be byte-identical to gen-recovery.ts output; regenerate it');
}

// --- make-repos executable ----------------------------------------------------
function checkMakeReposExecutable(): void {
  const p = here('repos/make-repos.sh');
  accessSync(p, constants.X_OK);
  const src = readFileSync(p, 'utf8');
  assert(src.includes('set -euo pipefail'), 'make-repos must set -euo pipefail');
  for (const pin of ['479eec44aa5cda0a5f91a48b2e5b6119c18570ba', 'cd739784278eb03050235d38d07c327c7811b36b', '82cabef608abfb716d1415eb14e9dcb2044f6712']) {
    assert(src.includes(pin), `make-repos must pin ${pin}`);
  }
  assert(src.includes('GIT_AUTHOR_DATE') && src.includes('GIT_COMMITTER_DATE'), 'deterministic dates required');
  assert(src.includes('fixture@example.invalid'), 'fixed fixture identity required');
}

const corpusBytes = readFileSync(here('corpus-v1.json'), 'utf8');
const faultBytes = readFileSync(here('fault-matrix-v1.json'), 'utf8');
const recoveryBytes = readFileSync(here('recovery-100-v1.json'), 'utf8');
for (const [name, bytes] of [['corpus-v1.json', corpusBytes], ['fault-matrix-v1.json', faultBytes], ['recovery-100-v1.json', recoveryBytes]] as const) {
  checkCanonical(name, bytes);
}
validateCorpus(corpusBytes);
validateFaultMatrix(faultBytes);
validateRecovery(recoveryBytes);
checkGenDeterminism();
checkMakeReposExecutable();
const digest = (b: Buffer | string): string => createHash('sha256').update(b).digest('hex').slice(0, 12);
console.log(
  `System fixture corpus verified: 20 unique S-12 tasks across greeting/calc/notes (3 opencode-capable), ` +
  `fault matrix ${9} modes, recovery 100 tasks / 10 SIGKILLs, canonical JSON, gen-recovery deterministic ` +
  `(sha256 ${digest(recoveryBytes)}), make-repos.sh executable with pinned SHAs. Fixture data only; no execution evidence.`,
);
