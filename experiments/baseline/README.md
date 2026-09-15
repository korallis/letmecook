# Baseline registration, collection and synthetic execution

Issue [#1](https://github.com/korallis/letmecook/issues/1) preparation. This isolated
TypeScript experiment validates frozen declarations and supplied actual-run
observations, retains the input records privately, and exports restrictive JSON
and CSV summaries. The separate `run-case.ts` command executes one public synthetic
case with the pinned native worker, shared 9Router authority, isolated checks and
complete retained candidate bytes. Development and tests use public synthetic fixtures.

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
The record demo executes no declared check commands. Its public real cohort is
empty; synthetic records never enter real completion or effort denominators.

## Execute the public synthetic case

The operator selects an installed Docker context and supplies absolute paths to
the public pinned router checkout, OpenCode source and Linux ARM64 binary. The
already-loaded image and native protocol are the measured ARM64 profile in
`../harness/pins.json` and `../router-authority-extension/runtime-identity.json`.
The launcher refuses incompatible identities and never pulls an image. Matilda
is optional infrastructure; this command has no machine name or credential path.

```sh
export GAFFER_DOCKER_CONTEXT=your-selected-context
export GAFFER_ROUTER_SOURCE=/path/to/pinned-9router
export GAFFER_OPENCODE_SOURCE=/path/to/pinned-opencode-source
export GAFFER_OPENCODE_BINARY=/path/to/pinned-opencode-linux-arm64
npm --prefix experiments/baseline run test:checks
npm --prefix experiments/baseline run case01 -- /tmp/new-case01-run two-requests
# A separate deterministic fixture uses two tool rounds and a final response:
npm --prefix experiments/baseline run case01 -- /tmp/new-case01-three three-requests
```

The shared router runs its actual subscription selection, request validation,
physical debits and durable receipts against a network-isolated fake original HTTP
server. This command accepts only its two named synthetic scenarios. It preserves
the 32-attempt/900-second baseline envelope and freezes the complete declaration
in an isolated local Git repository before starting the scope. An execution
projection breaks the registration/packet digest cycle; the start binds both final
digests and rejects changed declarations, tool paths, settings or phase limits.

OpenCode receives the frozen context and can apply patches only to the two declared
workflow files. The worker uses a 768-MiB container and an owned repository volume.
After native execution and transport end, the supervisor pauses it and obtains a
bounded Docker archive. Full inventory, modes and bytes are verified against the
base; undeclared changes, links and special files refuse retention. Isolated
128-MiB check containers use exact registered commands and immutable inputs.
The expected result is a failing base pin check, passing candidate pin check,
and the same inherited synthetic audit failure at both revisions.

The supervisor joins exact wire prompts, ordered encrypted continuations, every
tool result, native events, physical sends, durable receipt debit fields and scope
spending. Tool execution timestamps bind continuation order; CLI event emission
may arrive after the next request begins. A local acknowledgement binds the frozen
registration, packet, candidate, transcript and checks after retained-byte readback.
`validateRetainedRun` resolves the complete required record graph and verifies its
registration, invocation, receipt, snapshot, check and dataset joins. Offline replay
uses the same read-only validator; it never repairs missing evidence. Only the three
declared synthetic approval/eligibility/effort placeholders remain unresolved.

SIGINT/SIGTERM fence scope admission, worker dispatch and success acknowledgement,
including after awaited control, persistence and check operations. Bounded owned
container cleanup still runs. Before acknowledgement, the evidence directory and
every ancestor through the filesystem root are synced. A failed ancestry sync
retains the source volume and evidence; these tests establish ordering and error
handling, not power-loss survival. Source removal follows verified worker/gateway
termination, durable export and another complete record readback. Offline replay
reconstructs both retained historical candidates after their source removal.

The output directory contains content-addressed evidence, a frozen registration
commit, immutable run journals and the collector's private/public JSON and CSV.
The measurement remains `failed` because the inherited audit fails and no operator
label exists, even when the mechanics proof reports `passed`. Human effort, usage,
cost and acceptance remain unknown; the real cohort stays empty. Failed runs and
state volumes are retained. An existing output directory refuses another start.
This synthetic command does not authorize a live replacement or a private case.

See [artifact limits](artifacts/README.md) and [isolated check evidence](checks/README.md).

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
  and raw observation references. Public JSON/CSV report known `wallElapsedMs` and
  wall overrun separately from nullable, independently supplied `elapsedMs`; a short
  or missing elapsed observation cannot hide the known wall-clock duration.
- Declared base/candidate check outcomes. Failed gates do not disappear when an
  operator accepts useful failure-attribution work; acceptance does not mean merge
  readiness. Required candidate checks must bind the final accepted attempt and
  its accepted artifact through `candidateArtifact`; checks of an earlier candidate
  cannot qualify the final one. Stale observations remain in the history. Failed,
  not-run and unknown checks retain a source explaining the limitation and can
  still support the operator's useful failure-attribution label.
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

Public `knownLowerBounds` and `provenOverruns` preserve already-established budget
breaches when the complete total is unknown. The physical lower bound is the larger
of the distinct supplied operation count and any observed scope count. The effort
lower bound contains measured trial seconds only; estimates remain separate.
Thus 33 supplied operations or 1801 measured seconds retain their positive overrun
against 32/1800 even with missing receipts or recovery intervals. The full total
and its full overrun remain null when unavailable. A zero proven overrun means no
positive breach has been established from that lower bound, not that the complete
total is zero or within budget. A record-only effort treatment has no cap or
proven effort-overrun value.

Effort sums and cap comparisons use exact decimal arithmetic over each parsed JSON
number's canonical decimal spelling, including exponent notation. The input number
domain and fractional precision are unchanged; no epsilon or rounding to a fixed
number of decimal places is applied. For example, one measured second plus ten
179.9-second estimates is exactly 1800 seconds. A positive decimal excess still
fails the cap even when its rounded numeric total would equal the cap.

Numeric summary fields remain convenient projections. Their generated decimal
companions preserve exact arithmetic when JSON/JavaScript numbers cannot represent
the result: effort `exactSeconds`, `overruns.operatorSecondsExact`,
`knownSecondsByCategoryExact`, and cohort `exactSeconds`. CSV includes exact
estimated/trial seconds and the numeric/exact operator overrun. Unknown complete
totals remain null/empty in both representations. Category and cohort sums use the
original interval values, including across phases, rather than rounded subtotals.
Decimal text is generated from validated numbers; it contains no caller free text.

Place actual input in an operator-owned directory with mode 0700 and a file with
mode 0600. The output parent must also be private and already exist. Then:

```sh
node experiments/baseline/collect.ts /private/records/input.json /private/records/export
```

Input is bounded to 16 MiB; nonblocking open and a regular-file check refuse FIFOs
without waiting for a writer. FD reads consume at most the limit plus one byte,
including when a file grows after stat. Symlink files, exposed modes, ambiguous
JSON keys and invalid UTF8 are refused. Output uses an exclusive directory and
files, captures opened no-follow directory identities and rechecks their inode,
device, mode and type around writes and before acknowledgement. Directory
substitution refuses the export and leaves the original partial files intact.
Created file descriptors remain open through final verification. Before completion
and acknowledgement, every retained file must still match its pathname/inode,
private regular-file properties, size and expected bytes, including the completion
manifest itself. Missing, substituted or altered files refuse acknowledgement;
partial output remains for inspection. This is persistence verification at those
observation points, not a security boundary against the file owner.
The exporter fsyncs the records, then writes `complete.json` with exact file-byte SHA-256 digests.
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
validated enums, numeric facts and generated exact decimal text. Unknown CSV cells are empty, not zero. Known
measured seconds, estimated seconds and incomplete all-in effort are separate.
Sequence incidents such as a physical send after an unknown original (including
fallback within the same harness attempt), a later harness attempt, or concurrent
cases remain visible and disqualify accepted counts. A terminal unknown without a
later operation remains unknown without inventing a replacement. Every overlapping
case in the same provenance cohort is flagged, independently of input order;
adjacent half-open run intervals do not overlap. This reports supplied data;
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
