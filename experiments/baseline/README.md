# Offline baseline registration and collection

Issue [#1](https://github.com/korallis/letmecook/issues/1) preparation. This isolated
TypeScript experiment validates frozen declarations and supplied actual-run
observations, retains the input records privately, and exports restrictive JSON
and CSV summaries. It has no inference, grant, scope-start, repository staging,
check execution or live coordinator implementation. Its development and tests use
public synthetic fixtures only.

The original approved intake, historical proposal, development examples and
unstarted template in `tests/fixtures/tasks/` remain unchanged. A frozen declaration
is a new artifact. It is **not** a runtime permit or proof that a referenced
preflight, authority, Git commit, receipt, timer or human label is authentic.
`executionAuthorized` and `evidenceAuthenticated` are always false in this slice.
The calling evaluator must verify those sources through the authorised workflow.

All live replacement remains blocked following the initial trial's reported
unknown original. The initial trial is separate from the **zero registered or
started real baseline cases**. See [PR 85](https://github.com/korallis/letmecook/pull/85)
for current native-suite evidence; this record tooling does not resolve that stop.

## Run the synthetic example

Requires Node 24 or later; runtime code has no added dependency. TypeScript and
Node typings are pinned development dependencies.

```sh
npm --prefix experiments/baseline ci --ignore-scripts
npm --prefix experiments/baseline run check
npm --prefix experiments/baseline test
node tests/fixtures/tasks/check.ts
mkdir -m 700 /tmp/gaffer-baseline-record-demo
npm --prefix experiments/baseline run demo -- /tmp/gaffer-baseline-record-demo/export
```

Use a new private parent and output path on repeated runs. `fixture.ts` constructs
only invented synthetic records: a failed original, a charged router fallback,
failed audits, measured planning, estimated correction and unknown recovery.
No commands declared in a fixture are executed. The demo's public real cohort is
empty; synthetic records never enter real completion or effort denominators.

## Frozen declaration

`validateRegistration` accepts the closed schema in `registration.ts`. The complete
example in `fixture.ts` is the schema reference alongside the validator. It binds:

- Stable case/version and freeze time, predecessor digest for later versions,
  authority reference/digest and the complete approved public case digest.
- Eligibility and its sourced reason; immutable base SHA, tree digest, private
  fixture manifest and context/brief/progress-seed references with digests.
- Explicit read/write paths, criterion text digests and check-to-criterion mapping;
  exact check argv/cwd, toolchain version/binary/environment and check-input digests.
- Harness/router identity, named route, native profile, packet, isolation,
  preflight and stop-plan references. These are declarations, not verified controls.
- Exactly 32 physical attempts and 900000 ms per case, including retries, one case
  at a time, zero refresh, Astra/xhigh, existing Codex subscriptions through 9Router,
  no paid fallback, retained strict default and unavailable provider caps.
- A sourced operator-effort treatment: a positive cap of at most the original
  1800-second proposal, including preparation, or explicit `record-only` with a
  null cap. Selecting a treatment must reflect actual operator authority; this
  validator does not supply it. Estimates and missing intervals stay separate.

For operator declarations the validator binds `operator-01` through `operator-03`
to the unchanged intake authority, private-manifest reference/digest, approved-case
digest and ordered criterion text digests. It cannot verify private paths/checks
against private source. The caller must resolve that mapping before execution.
Synthetic declarations use a separate identity prefix and are always excluded.

Freeze the registration in Git before dispatch and reference that full commit in
the run record. The run also carries the canonical registration digest; changing
the declaration breaks that link. The collector does not call Git or resolve the
commit. Preserve earlier registration versions externally, rather than rewriting
one after seeing a result.

## Actual-run observations and private export

The input file is one closed object: `{schema: 1, registrations: [...], runs: [...]}`.
Each registration has exactly one case record, including blocked unstarted cases;
all harness attempts/retries belong inside it. The terminal observation schema is
in `observations.ts`, with examples in `fixture.ts`. It retains:

- Scope evidence and observed physical count/completeness/quiescence; ordered
  attempts, every supplied physical original/fallback, failures, progress, artifacts
  and raw observation references.
- Declared base/candidate check outcomes. Failed gates do not disappear when an
  operator accepts useful failure-attribution work; acceptance does not mean merge
  readiness. Not-run/unknown checks retain a source explaining the limitation.
- Sourced measured/estimated/unknown intervals and coverage by trial/follow-up.
  Empty intervals and incomplete coverage never become a complete zero-effort
  result. Measured intervals must match their UTC duration; overlapping intervals
  for an operator are rejected across the complete supplied set. Split shared work
  into non-overlapping sourced allocations before ingestion; an allocation engine
  is not implemented here.
- Observed/estimated/unknown usage and separately allocated ownership-cost inputs.
  Empty/incomplete usage is unknown; currencies are not added together; absence of
  paid fallback is not a monetary observation.
- Explicit human output labels, criterion evidence and seven-day follow-up. Missing
  labels remain null. A late accepted output can remain `budget-exhausted` and is
  excluded from in-budget successes. Follow-up cannot finish before seven days.

Place actual input in an operator-owned directory with mode 0700 and a file with
mode 0600. The output parent must also be private and already exist. Then:

```sh
node experiments/baseline/collect.ts /private/records/input.json /private/records/export
```

Input is bounded to 16 MiB; symlink files, exposed modes, ambiguous JSON keys and
invalid UTF8 are refused. Output uses an exclusive directory and files, fsyncs the
records, then writes `complete.json` with exact file-byte SHA-256 digests.
Success is acknowledged only after all directory syncs complete. Existing output
is never overwritten. On storage failure, retain the partial directory; it is not
a successful export. Raw sidecar evidence referred to by records remains in the
authorised private evidence store and must be separately retained and checked.

The export contains `private-records.json`, `public-summary.json`,
`public-summary.csv`, and `complete.json`. **Only the two public-summary files are
publication candidates**, still subject to the existing redaction review. Private
records include commands, paths, case/base mappings and evidence references. The
complete manifest hashes private records and remains private too.

Public summaries copy no caller IDs, arbitrary text, commands, paths, references,
private SHAs or digests. They use fixed record aliases, the three public case IDs,
validated enums and numeric facts. Unknown CSV cells are empty, not zero. Known
measured seconds, estimated seconds and incomplete all-in effort are separate.
Sequence incidents such as a physical send after an unknown original (including
fallback within the same harness attempt), a later harness attempt, or concurrent
cases remain visible and disqualify accepted counts. A terminal unknown without a
later operation remains unknown without inventing a replacement. This reports supplied data;
it neither controls future execution nor discovers omitted runs. Input-set
completeness remains explicitly caller-declared.

## Remaining issue-1 work

No real case has been registered or observed by this slice. The native suite still
uses fixed greeting/planner fixtures. Real repository admission, native tools or
declared-check runner, baseline session timings, multi-file/report artifact
qualification, durable candidate bytes and sequential case orchestration remain
separate additive experiment work. Real preflight, resolved private mapping,
actual scope/receipt/check observations, operator timers and final human labels
are also missing. Unknown original work blocks replacement regardless of whether
a declaration validates. No M0 completion, quality or productivity claim follows
from a synthetic export.
