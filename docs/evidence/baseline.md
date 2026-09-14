# Standalone baseline and measurement contract

Contract version **1** · recorded **14 September 2026** · issue [#1](https://github.com/korallis/letmecook/issues/1).

**Status: fixture design ready for review; real baseline blocked.** No operator
labels or live baseline observations have been supplied or recorded. This document
does not establish a product benefit or satisfy the M0 live-baseline gate.

This is an isolated measurement contract for the [PRD validation measures](../PRD.md#6-validation-and-success-measures),
[evaluation design](../evaluation.md#6-how-to-test-the-product-hypothesis) and
[M0 exit evidence](../roadmap.md#m0--evidence-and-feasibility). It implements no
Gaffer runtime, intent engine, account router or harness adapter.

## Evidence inventory and missing inputs

| Input | Observed state | Required to unblock |
| --- | --- | --- |
| First three real operator cases | Unavailable; three empty slots in `operator-intake.json` | Operator captures, intended outcomes, acceptance criteria and data permissions, labelled before any evaluated brief |
| Repository fixture for each case | Unavailable | Repository reference, full starting commit, permitted files/data and reproducible prerequisites |
| Harness and 9Router route | Unavailable for this baseline; not probed | Pinned harness/router versions, authorized named route, model/config identity, execution isolation and a permitted access mechanism |
| Baseline budgets | Proposed defaults below, no run registration yet | Record explicit bounds and any task-specific overrides before dispatch |
| Raw live observations | None | Redacted brief, progress notes, attempt/check evidence, operator timers and human outcome labels |
| Real baseline cohort | 0 registered, 0 started, 0 accepted | Three real cases, then a bounded standalone run per case, including failures |

The repository used to prepare this contract was `korallis/letmecook` at
`eb6b353`. That is **not** a starting commit for any operator task. There is no
claim that the coding assistant used to write this document ran through the
intended baseline route. Missing route access does not authorize credential
discovery, subscription changes, provider-direct inference or live probes.

The committed [fixture examples](../../tests/fixtures/tasks/development-examples.json)
are synthetic and never enter reported denominators. The
[operator intake](../../tests/fixtures/tasks/operator-intake.json) records exactly
which labels remain unavailable. The [run template](../../tests/fixtures/tasks/run-template.json)
is an unstarted, non-observational record; its null values mean unknown or not yet
recorded, never zero. There are no fabricated raw observations in this change.

## Register fixtures before evaluation

Assign each capture a stable ID and record its source, capture text, task class,
intended outcome, permitted scope, objective acceptance criteria, required checks,
full repository starting SHA and declared data permissions. A human operator owns
the intended-outcome label. Record who labelled it and when; an agent's guess
cannot fill that field. Data permissions name approved data, prohibited data,
allowed route/provider exposure, execution and network access, and any separately
authorized external effects. Omitted permissions grant no authority.

Freeze each registered fixture and its measurement contract in a git commit
**before** generating the evaluated brief. Run records reference that commit and
fixture ID. Record changed intent as a new version, retaining the previous one;
do not retroactively rewrite the success criterion after seeing results.

Partition along two independent axes:

| Axis | Values and rule |
| --- | --- |
| Capture split | `development` or `held-out`; assignment occurs before tuning, and variants of the same intent/repository task stay in the same split |
| Execution eligibility | `feasible`, `infeasible`, or `needs-clarification`; assessed against the declared prerequisites and authority, with a human reason |
| Provenance | `operator` or `synthetic`; only operator-labelled captures count toward empirical capture/intent claims |

The PRD targets 30 development captures, at least 30 held-out captures and 30
feasible execution cases. These are distinct denominators, not a quota satisfied
by this six-example seed. The first three real M0 cases are a small development
baseline, not the held-out or alpha cohort. They inform fixture preparation but
cannot establish a general accuracy rate.

Keep held-out capture text and labels in a separately restricted operator-owned
store; commit only its immutable manifest reference/hash and permitted redacted
metadata. Exclude held-out content from prompt development, retrieval and worker
context. Give the evaluator access only after the candidate prompt/config is
frozen. Disclose exposure, retire contaminated cases from the held-out analysis
and register replacements before further tuning; retain their old results.

Impossible or unauthorized cases must be blocked/refused; ambiguous cases must
receive the prelabelled clarification. Report these responses separately, including
unsafe execution or incorrect refusal. Do not include them in feasible-task
completion denominators. If clarification makes a case executable, register the
resolved fixture before execution. A failed feasible task remains in the feasible
cohort; never relabel it as infeasible merely because it failed.

## Standalone comparator and bounds

Use a well-configured standalone harness on the intended 9Router route with the
same repository SHA, permissions, task criteria and model/config choices planned
for the later Gaffer comparison. Give it a good operator-approved brief containing
the outcome, scope, constraints, checks and stop conditions, plus a progress-notes
file it can update. Include discovery and brief-preparation effort in measurement.
This comparison does not require Gaffer to exist and does not implement it.

Register these default **task-level** bounds, or a written override, before work:

- Maximum elapsed execution: 60 minutes across all attempts.
- Maximum attempts: 2, including the initial attempt; recovery does not reset this.
- Maximum active operator effort: 30 minutes, including brief preparation,
  approvals, initial review, correction and recovery up to the terminal result.
  Subsequent seven-day follow-up effort is recorded separately and included in
  all-in comparison totals; it does not retroactively change this execution bound.
- Paid overflow: disabled. If a paid route is expressly selected, predeclare its
  currency, incremental cash cap and enforcement mechanism instead.
- Token/cost reporting: best available observations with confidence/source;
  unavailable counters remain unknown. A token budget is used only when the
  harness/router can enforce it and its exact value is recorded in advance.

The run owner enforces the time/attempt bounds with the selected harness's stop
mechanism and records it before dispatch. If the selected environment cannot
enforce a chosen bound, record the gap and keep the run blocked until a bounded
alternative is agreed. Stop at the first exhausted bound, preserve the artifact,
and record a failed or budget-exhausted outcome with all effort. Termination and
evidence collection after a stop still count; report any overrun explicitly.
Do not silently buy overflow, retry elsewhere or change provider routing.

Route identity records the 9Router named route, pinned router and harness builds,
requested model, observed serving model when exposed, redacted config digest,
fallback/overflow policy, and environment/isolation profile. Record drift and
unknown model identity; discovery metadata alone is not evidence of a served
request. 9Router owns accounts, subscription selection and request fallback.
No provider credentials or management credentials belong in a worker fixture.

## Reproducible collection procedure

1. Complete the three operator intake slots. Classify eligibility and freeze the
   labelled fixture manifest in git. Record prerequisites and exact setup/check
   commands; confirm the starting SHA exists before initializing a fresh workspace.
   A worktree alone is not an execution security boundary.
2. Copy `run-template.json` to a local evidence directory, with a unique run ID.
   Replace null prerequisites with observed/approved values. Record the fixture
   commit, versions, route, permission reference, bounds, stop mechanism and exact
   redacted invocation. Resolve listed blockers before setting `status` to `registered`.
3. Start the operator timer at capture/brief work, not at harness dispatch. Save
   the good brief and seed progress notes, recording preparation and any edits.
   Capture UTC timestamps and elapsed durations from a monotonic timer where
   available; note clock corrections instead of producing negative intervals.
4. Start the standalone harness through 9Router in the selected isolated disposable
   repository. Record every attempt's start/end, progress notes, stop reason,
   interruption, tool/check exit result and preserved artifact reference/digest.
   Record failed requests, corrections and recovery, even if a retry succeeds.
   Unknown partial-stream effects require inspection before any redispatch.
5. Run the registered checks on the resulting artifact. The operator assesses
   each acceptance criterion, scope fidelity and required clarification/refusal.
   Save the human label and evidence, including rejection reasons. Passing checks
   or a harness completion message alone cannot establish accepted intent.
6. Follow accepted artifacts for seven days. Record regressions, scope misses,
   reverts and their discovery time/severity. A result with an open or missing
   follow-up window cannot support a completed quality comparison.
7. Review the evidence for secrets and permission compliance before committing.
   Record redactions and any evidence unavailable after redaction. Commit permitted
   redacted raw observations and run records together; calculate results from that
   pinned set. Never silently discard failures or replace raw data with a summary.

No universal live command is supplied because the intended harness/route has not
been selected for this baseline. The registered record must include the exact
working invocation and prerequisites before someone else can reproduce the run.
Avoid putting access tokens in command arguments, transcripts or this repository.

From the repository root, check the committed templates and documentation with:

```sh
node --input-type=module -e 'import { readFileSync } from "node:fs"; for (const name of ["operator-intake", "development-examples", "run-template"]) { JSON.parse(readFileSync(`tests/fixtures/tasks/${name}.json`, "utf8")); console.log(`${name}: valid JSON`); }'
npm --prefix docs ci
npm --prefix docs run build
npm --prefix docs run check
```

These commands check artifact syntax and the existing documentation reader, not
live evidence, operator intent, eligibility or route readiness. No new application
test suite is required for this contract-only change.

## Active operator minutes and total overhead

Record a timer interval with operator ID, run ID, start, end, duration in seconds,
one category and a short activity note. Categories are `capture`, `planning`,
`approval`, `review`, `correction`, `recovery`, and `setup`. Planning includes
discovery and brief/progress-note preparation; review includes verification and
seven-day defect assessment. Setup includes baseline-specific environment work.

Pause the active timer during unattended waiting; report waiting and elapsed
wall time separately. Do not overlap intervals for one operator. Split a shared
activity among its tasks using a predeclared allocation, retain the unsplit source
interval and ensure allocated durations sum to it. Report one-time setup separately
and an allocated total so startup effort cannot disappear from an all-in claim.
For multiple operators, sum person-minutes without calling that wall-clock time.
Use measured seconds / 60 for calculations; round only display values.

Every started case retains all failed-attempt, correction and recovery effort.
Keep rejected, timed-out, interrupted and unknown-result cases in the cohort.
Record effort already spent on blocked unstarted cases separately and include it
in cohort overhead. Unknown timer intervals make the effort result incomplete;
do not impute zero. Retrospective estimates need an `estimated` confidence label,
their source and a separate analysis from measured intervals.

## Outcome, defects and reporting

An accepted task meets every operator-labelled criterion without a material scope
breach, within the registered bounds. Record terminal results as `accepted`,
`rejected`, `failed`, `budget-exhausted`, `blocked`, `cancelled`, or `unknown`.
Use one row per task, with nested attempt records, so retries cannot inflate
successful-task counts. Any late acceptance or budget overrun remains visible and
does not become an in-budget success. Do not infer acceptance from missing labels.

| Severity | Predeclared meaning | Effect |
| --- | --- | --- |
| Critical | Unauthorized external effect, data loss or exploitable execution/authority boundary failure | Any occurrence blocks release, even if recovered |
| Major | Material scope miss or functional regression | Later paired pilot must have no more major defects than its comparator |
| Minor | Other corrections that do not meet the above definitions | Report counts and effort; do not claim equivalence from a small sample |

Record who classified a defect, its evidence, detection date, affected task and
whether it caused rework/revert. Preserve disputed classifications; pending
operator adjudication prevents a clean quality-gate claim. Never downgrade a
critical incident because the task was otherwise useful.

Report results as JSON and a CSV task summary when real data exists, including:

- Registered/started/accepted/failed/blocked/unknown counts, task classes and the
  feasible/infeasible split; completion = accepted feasible tasks / started feasible
  tasks. Also show registered feasible tasks so pre-start attrition is visible.
- Per-task active minutes by category, attempts, wall time and all-in cohort
  minutes / accepted outcomes. With zero accepted outcomes this ratio is undefined,
  not zero; with no starts there is no completion rate.
- Measured/estimated/unknown model tokens, cached usage and costs by source. Keep
  incremental cash, allocated subscriptions/hardware/electricity and operator time
  distinct. Include planner/reviewer/verification overhead when present. Do not
  treat missing usage as free work or token counts as remaining subscription quota.
- Seven-day follow-up coverage and critical/major/minor defect counts, including
  unsuccessful cases and any authority/durability incident.

For the later 20-task paired study, freeze matched starting commits, randomize or
alternate comparator order, and record the method and deviations. Keep route,
model, permissions and bounds comparable; disclose any difference. Report each
paired reduction `(standalone_minutes - gaffer_minutes) / standalone_minutes`,
the median, range and quartiles of valid pairs, and exclusions with reasons. A
zero standalone time makes the relative pair undefined, while its raw effort
still counts in the cohort. Report completion and total minutes per accepted
outcome alongside paired effort so failures cannot manufacture an improvement.
Do not claim the PRD's provisional 20% benefit until quality gates and seven-day
follow-up pass. M0's three-case baseline is not this beta study.

## Completion ledger

- Fixture partition, contamination policy, severity rules and operator-minute
  method: specified here, ready for review before data collection.
- First three real use cases, starting commits, checks and permissions: blocked on
  operator input; see intake slots rather than synthetic substitutes.
- Bounded standalone baseline through intended 9Router routes: not run; blocked on
  those fixtures plus authorized pinned environment/route details.
- Reproducible collection instructions and redacted record formats: committed.
  Exact harness invocation, observed versions, raw live observations and real
  baseline results remain unavailable until a permitted collection run occurs.
