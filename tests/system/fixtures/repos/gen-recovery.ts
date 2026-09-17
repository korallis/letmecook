// Generates recovery-100-v1.json, the S-13 recovery fixture: 100 trivial fake
// tasks across the three synthetic repositories plus the daemon-restart
// schedule and timing method. Node 24+, no dependencies.
//
// Usage: node tests/system/fixtures/repos/gen-recovery.ts [output-path]
//   (default output: recovery-100-v1.json next to this file; "-" writes stdout)
//
// Deterministic: same script version -> byte-identical JSON (canonical
// 2-space indent, sorted-by-construction arrays, trailing newline).
// check.ts re-runs this generator and requires byte equality.
import { writeFileSync } from 'node:fs';

const REPOS: Record<string, { base_commit: string; profile: string; commands: number }> = {
  greeting: { base_commit: '479eec44aa5cda0a5f91a48b2e5b6119c18570ba', profile: 'greeting-echo-v1', commands: 2 },
  calc: { base_commit: 'cd739784278eb03050235d38d07c327c7811b36b', profile: 'calc-go-v1', commands: 2 },
  notes: { base_commit: '82cabef608abfb716d1415eb14e9dcb2044f6712', profile: 'notes-links-v1', commands: 1 },
};
const REPO_ORDER = ['greeting', 'calc', 'notes'] as const;
const TOTAL = 100;
// Every 10th task (1-based) crashes first: --auto-retry (S-13 only) must
// release, re-dispatch and complete it. Exactly 10 such tasks.
const isCrashTask = (index1: number): boolean => index1 % 10 === 0;
// gafferd is SIGKILLed while these 1-based task indexes are in flight.
const KILL_AT = [8, 18, 28, 38, 48, 58, 68, 78, 88, 98];

const pad = (n: number): string => String(n).padStart(3, '0');
const allPass = (repo: string): { index: number; expect: string }[] => {
  const count = REPOS[repo]!.commands;
  return Array.from({ length: count }, (_, index) => ({ index, expect: 'pass' }));
};

function editFor(repo: string, index1: number): { path: string; content: string } {
  switch (repo) {
    case 'greeting':
      return { path: 'greeting.txt', content: 'hello, gaffer\n' };
    case 'calc':
      // A non-Go file keeps `go test ./...` and `gofmt -l .` green.
      return { path: `run-${pad(index1)}.md`, content: `# Run ${index1}\n\nRecovery run marker for task ${index1}.\n` };
    default:
      // Plain page with no markdown links: the trusted link check stays green.
      return { path: `docs/recovery-${pad(index1)}.md`, content: `# Recovery ${index1}\n\nSynthetic recovery marker page for task ${index1}.\n` };
  }
}

function task(index1: number): Record<string, unknown> {
  const repo = REPO_ORDER[(index1 - 1) % REPO_ORDER.length]!;
  const edit = editFor(repo, index1);
  const good = { mode: 'edit', edits: [{ path: edit.path, content: edit.content }] };
  const attempts = isCrashTask(index1) ? [{ mode: 'crash', edits: [{ path: edit.path, content: edit.content }] }, good] : [good];
  return {
    id: `recovery-task-${pad(index1)}`,
    repo,
    brief: isCrashTask(index1)
      ? `Apply the ${edit.path} change for recovery task ${index1}; the first attempt crashes and --auto-retry must complete it.`
      : `Apply the ${edit.path} change for recovery task ${index1}.`,
    criteria: [{ id: `r${pad(index1)}-edit-applied`, text: `${edit.path} exists with exactly the pinned deterministic content` }],
    paths: [edit.path],
    operations: ['create', 'edit'],
    harness: ['fake'],
    fake_spec: { attempts },
    expected_outcome: isCrashTask(index1) ? 'retry_then_succeeded' : 'succeeded',
    verification: { profile: REPOS[repo]!.profile, commands: allPass(repo) },
    notes: isCrashTask(index1)
      ? 'Crash-then-succeed pair: exercises auto-retry (S-13 only) across the restart barrier.'
      : 'Trivial deterministic edit; every trusted check must stay green.',
  };
}

const fixture = {
  version: 1,
  fixture: 'recovery-100-v1',
  scenario: 'S-13',
  purpose: '100-task recovery load: SIGKILL gafferd 10 times with --auto-retry enabled and measure restart-to-admission and restart-to-full-classification.',
  repos: Object.fromEntries(
    Object.entries(REPOS).map(([name, r]) => [name, { profile: r.profile, base_commit: r.base_commit }]),
  ),
  base_policy: 'Same pinned initial commits as corpus-v1.json; every task bases on its repository initial commit (no chaining, no merge route).',
  sequencing: {
    mode: 'sequential',
    detail: 'One active attempt at a time in id order, as in S-12; recovery pressure comes from daemon kills, not concurrency.',
  },
  restart_schedule: {
    target: 'gafferd (the daemon), SIGKILL',
    kills: KILL_AT.map((taskIndex, i) => ({
      seq: i + 1,
      signal: 'SIGKILL',
      during_task_index: taskIndex,
      task_id: `recovery-task-${pad(taskIndex)}`,
      detail: 'Kill the daemon while this task is in flight; the runner, guardian and job keep running across the restart.',
    })),
    kill_count: KILL_AT.length,
    auto_retry: '--auto-retry is enabled for this scenario only (harness_crash, lease_expired, runner_restarted, daemon_restart classes).',
    after_each_kill: [
      'Start a fresh gafferd against the same state/artifacts dirs (new daemon_boot, same generation).',
      'Run reconcile startup; wait for every non-terminal attempt to be classified.',
      'Re-import runner facts if the runner restarted; otherwise reuse the live session.',
      'Resume dispatching from the first non-terminal task.',
    ],
    repeat_for_samples:
      'S-13 asserts p95 over at least 20 restarts; run this 100-task cycle twice (20 kills total) when 10 samples are insufficient.',
  },
  timing_method: {
    timestamps_per_restart: [
      { name: 'kill_unix_ns', source: 'test harness clock at SIGKILL' },
      { name: 'restart_ready_unix_ns', source: 'first successful authenticated owner API response from the new daemon' },
      { name: 'first_admitted_dispatch_unix_ns', source: 'first dispatch returning 201 after restart (daemon event log)' },
      { name: 'all_classified_unix_ns', source: 'first moment no attempt is non-terminal and no reconcile classification is pending (daemon event log)' },
    ],
    metrics: [
      { name: 'recovery_ms', formula: 'first_admitted_dispatch_unix_ns - restart_ready_unix_ns' },
      { name: 'classify_ms', formula: 'all_classified_unix_ns - restart_ready_unix_ns' },
    ],
    percentiles: 'Nearest-rank over the sorted samples: p50 = ceil(0.5*n)-th, p95 = ceil(0.95*n)-th, max = n-th. With n=10, p95 is the 10th (max) sample; reach n>=20 by repeating the cycle.',
    old_lease_barrier:
      'The measured recovery window deliberately includes the 30 s old-lease barrier (lease validity 20 s plus drift/termination margin) whenever a pre-restart lease must lapse before its dispatch is releasable; no timing sample may subtract it.',
    unknown_is_blocking:
      'Attempts classified unknown (remote work or process unconfirmed) count as blocking: all_classified is not reached while any attempt is unknown, and such tasks are not counted as recovered.',
  },
  tasks: Array.from({ length: TOTAL }, (_, i) => task(i + 1)),
  evidence: 'None. Generated plan data for the S-13 system test; nothing here is evidence of execution.',
};

const json = `${JSON.stringify(fixture, null, 2)}\n`;
const out = process.argv[2] ?? new URL('../recovery-100-v1.json', import.meta.url).pathname;
if (out === '-') process.stdout.write(json);
else writeFileSync(out, json);
