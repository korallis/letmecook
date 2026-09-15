# Gaffer — Roadmap

**Status:** proposed baseline v0.4; foundation decision recorded, not locked · **Updated:** 15 September 2026

Companions: [PRD](PRD.md), [specification](spec.md), [evaluation](evaluation.md).

This is a sequence of evidence gates for a solo maintainer. 9Router is the selected
shared model-access layer from M0, including multiple subscriptions per provider.
The gates validate that integration rather than deciding whether to include it.
This plan replaces the original
rough 29-week plan, whose ordering delayed essential controls until after parallel
execution. No calendar commitment is credible while isolation, live and reuse gates remain
unmeasured. The original M0 recommendation was roughly five focused engineering
days, not an observed effort result. The
[foundation effort record](https://github.com/korallis/letmecook/blob/main/docs/decisions/0001-execution-foundation.md#effort-record-and-estimates)
owns observed M0 activity and conditional M1/M2 estimates. Those planning ranges
apply after entry gates and exclude unknown M0 remediation and operator waits;
they are not a release date.

## Release sequence

```text
M0  Evidence and feasibility        prove 9Router integration; decide Gaffer reuse
M1  Durable bounded execution       one task, one runner, recoverable artifact
M2  Intent and operator workflow    first useful private alpha
M3  Portable knowledge              explicit memory with measured retrieval
M4  Capacity and parallel delivery  second harness + eligible fleet; beta
M5  Recurring work                  schedules and GitHub polling
M6  External usability and v1       offline drafts, install, restore, honest limits

Later: automatic curation, learned idioms, auto-merge, desktop runners
```

Exit checkboxes are milestone acceptance gates, not an implementation-status
tracker. All three reversible slices (#93 protocol, #94 fixture daemon, #95 embedded
shell) have merged, as has #10's v2 contract. These merges do not pass a milestone:
[O1–O8](https://github.com/korallis/letmecook/blob/main/docs/contracts/execution.md#8-9-reconciliation-obligations)
remain open before contract acceptance. The recorded build choice leaves #9 open
pending required measurements and fresh Opus review. Failing a gate can narrow
scope or favour reuse; it need not produce another subsystem.

## M0 — Evidence and feasibility

**Question:** can the intended workflow run through a containable harness and the
shared 9Router instance, and is a new Gaffer foundation justified?

**Work**

- Record the operator's first three real use cases and a standalone-harness baseline using the same 9Router routes.
- Evaluate the closest reuse candidates using [the comparison rubric](evaluation.md#3-build-or-reuse-before-building), including licence, maintenance and integration cost.
- Connect a pinned 9Router build and named routes for the first worker and API planner. Configure two distinct subscriptions from the same provider in 9Router.
- Prototype one bounded task in an isolated disposable repository through harness→9Router; prove structured planning through the same router's API and bounded discovery.
- Validate tool calls, streaming, structured output, ambient config/hooks, private endpoint access and billing policy on the intended runtime.
- Implement a small authenticated route boundary and sanitized status adapter where the selected router lacks scoped keys or safe stable status APIs. Keep all provider/account selection and refresh in 9Router.
- Define the verification fixture and prepare captures/tasks with expected outcomes before tuning prompts.
- Specify task-aware route assessment, hard eligibility, operator preferences, bounded bootstrap and decision evidence; preserve 9Router's ownership of request fallback and accounts. Keep desired model labels separate from verified settings/capabilities.

**Exit evidence**

- [ ] A written build/adopt/extend decision compares at least two plausible foundations with the same required workflow; untested properties stay unknown.
- [ ] One real task through 9Router returns a complete reviewable artifact with recorded harness/router version, base SHA, route and usage observations.
- [ ] Two subscriptions from one provider serve the configured route. Disabling/exhausting one causes router-owned selection of the other before response output or on the next request, without changing agent configuration or redispatching the Gaffer task. Partial-stream failure follows the separate reconciliation test.
- [ ] Provider credentials remain inside 9Router. Worker inference access cannot reach its management APIs or select an unauthorised route. Status projection excludes nested credentials and raw API-key fields.
- [ ] A worker cannot write outside the execution boundary or modify runner policy; adversarial fixtures cover symlinks, hooks and child processes.
- [ ] Planner/discovery probes cannot execute repository code or gain delivery permissions.
- [ ] Cancellation terminates the test process tree; failure is a blocker for unattended work.
- [ ] Model discovery is distinguished from readiness by a real bounded probe; the route's paid-overflow policy and request/partial-stream fallback behaviour are established.
- [ ] Baseline task results, operator minutes and total overhead are recorded with a repeatable test method.
- [ ] The task-routing contract and development counterexamples are reviewed; alpha implementation and a separate static-versus-task-aware comparison are tracked without changing the identical-route whole-product baseline.

**Recorded choice:** build a narrow Go core, retaining SQLite, an embedded
React/TypeScript/StyleX UI and OpenCode 1.18.30 as the first adapter target. The
[comparison and reconciliation plan](https://github.com/korallis/letmecook/blob/main/docs/decisions/0001-execution-foundation.md)
is not locked and establishes **no supported execution runtime**. Fresh Opus review,
paired candidate execution/cost, Docker-free #89 confinement plus adapter/verifier
integration, live worker/planner/continuity and real baseline observations remain
open. The failed one-send live trial grants no retry. Original dependency edges
remain; only the approved provisional slices have an ordering exception.

Revisit adopt/extend if a maintained project proves the critical boundaries with
less owned maintenance than the narrow core. Do not count provisional sunk effort
in that comparison. Gaffer's account-routing layer
is already delegated to 9Router. Fix or document specific adapter/protocol gaps;
do not turn them into a competing native-account manager or silently bypass the
shared router. API-backed connections can also be configured in 9Router when chosen.

## M1 — Durable bounded execution

**Question:** can one approved task execute, fail, recover and produce one accepted
artifact without losing state or repeating external effects?

**Build**

- Daemon state, authority, task/attempt identities, transactional outbox and resource reservations.
- Deterministic validation and persistence of an approved route decision with the task's requirements and policy versions; M1 accepts operator-supplied assessments and does not depend on the M2 semantic planner.
- Runner enrollment disabled by default, local repository policy and one supported containment profile.
- Runner journal, renewable lease/watchdog, stream sequencing, artifact upload/acknowledgment and retry classification.
- Basic CLI for create/approve/inspect/pause/cancel/stop; a fake harness for deterministic fault tests plus the real M0 adapter.
- Scoped verification and a local review package. No automatic publication.
- Consistent backup/restore command and explicit storage limits.

**Exit evidence**

- [ ] Twenty representative sequential tasks produce inspectable artifacts and verification evidence; failures are retained and explained rather than excluded from the count.
- [ ] Fault injection covers assignment before/after commit, duplicate delivery, lost acknowledgment, daemon restart, runner restart and a live network partition.
- [ ] A partition or delayed fresh lease renewal cannot cause overlapping valid execution leases; stale results cannot become current or publish. A restore-generation change fences surviving old-generation messages before resume.
- [ ] Connected stop acknowledgment targets five seconds; lease-expiry termination of disconnected runs meets the documented bound.
- [ ] An unlisted repository, unavailable route or changed local policy produces a named refusal without widening placement.
- [ ] Retry/duration/concurrency limits work even with unknown provider quota; router/harness/task retries have a bounded combined deadline.
- [ ] Router downtime parks model calls while capture, stored review and stop remain usable; reconnect resumes under current task authority without direct-provider bypass.
- [ ] A crash during artifact promotion leaves either a recoverable orphan or a valid durable reference; never an acknowledged missing artifact.
- [ ] Backup restore begins paused, validates manifests and reconciles attempts; disk-full and corrupt-checkout fixtures preserve the only available work.
- [ ] Safe recovery p95 is under 60 seconds for the specified 100-task fixture, excluding provider wait; all failures and timings are reported.

**Gate:** no UI or extra harness can compensate for failure here. Do not begin
unattended dogfood until the authority, stop and reconciliation cases pass.

## M2 — Intent and operator workflow

**Question:** does the complete phone-to-reviewed-change flow improve real work?
This is the **private alpha**, limited to one repository and sequential execution.

**Build**

- Authenticated private HTTPS web application: capture, routing correction, brief/plan editing, combined approval, task status, evidence review and stop.
- Task-aware selection among approved 9Router profiles: bounded semantic assessment, capability filtering, explainable ranking, uncertainty and eligible overrides for worker/planner/reviewer steps. Fixed role defaults are the comparator, not the completed feature.
- Bounded discovery and structured intent/planning with criterion IDs and assumptions.
- Revocation on material intent changes; current instructions override stored preferences.
- Minimal Markdown context: charter, progress, verified commands and decisions, with human-approved writes.
- Single integration writer and explicit trusted publication executor using recorded authority and exact artifacts.
- In-app digest and operator-time/usage instrumentation.

**Exit evidence**

- [ ] Thirty held-out captures are assessed against operator-labelled intent; report outcome fidelity, material assumption errors, edits, latency and question burden.
- [ ] At least 24 of 30 feasible pilot task cases, including their bounded retries, satisfy their acceptance criteria without material scope breach; any unauthorised external effect blocks release regardless of aggregate accuracy.
- [ ] Impossible/unauthorised capture cases are scored separately for safe clarification/refusal. Ambiguous repository routing, changed intent and missing authority block affected work without stalling independent authorised work.
- [ ] Phone capture acknowledgment p95 is under two seconds on the declared network; separate discovery-start and approval-to-dispatch timing are reported.
- [ ] Refresh and disconnect restore status; stale approvals and duplicate submissions cannot act on a newer candidate.
- [ ] Review shows the exact candidate, checks and limitations. Accept, publish and merge are distinguishable; no default-branch push occurs.
- [ ] Lost push/PR response is reconciled by remote branch/head/PR identity before retry; moved remote refs block instead of overwriting.
- [ ] Ten paired tasks compare Gaffer with a standalone harness using the same 9Router routes, a good brief and progress notes; include all planning, review and recovery effort.
- [ ] A separately registered selector comparison checks suitability, uncertainty, overrides and zero hard-constraint violations against static role defaults, counting assessment and failed-run overhead. Here route choice varies while the available route set, fallback definitions, budgets and harness remain fixed; do not combine it with the identical-route product comparison.
- [ ] Seven consecutive days of bounded dogfood have no lost acknowledged result or unauthorised publication. Blocking is allowed and reported.

**Decision:** if intent clarification does not reduce correction effort, simplify
it to explicit brief editing before increasing model sophistication. If coordination
cost exceeds its benefit, retain a single worker with durable workflow state.
Self-dogfood is useful after this gate; always keep a tested standalone fallback.

## M3 — Portable knowledge

**Question:** does retained knowledge improve later tasks without amplifying error?

**Build**

- Repository facts and project-scoped knowledge with commit/path provenance.
- Single context writer, review proposals, external-edit detection and Git/index reconciliation.
- Explicit references and FTS/BM25 retrieval with budgets and evidence links.
- Stale-command signals and a context browser with history, correction and export.

**Exit evidence**

- [ ] Contradictory proposals cannot silently replace approved knowledge; dirty external edits are preserved.
- [ ] A planted obsolete command produces a review signal with evidence; a network failure is not automatically classified as a false instruction.
- [ ] Paired later tasks with/without memory report operator rediscovery/correction effort, useful citations and stale-context incidents.
- [ ] A process crash after a context commit but before indexing rebuilds the correct index without duplicate knowledge commits.
- [ ] Export/uninstall leaves readable context; full restore still requires state/artifacts as documented.

**Gate:** keep manual approved memory if automatic assistance adds correction burden.
Embeddings and automatic promotion remain experiments, not prerequisites for beta.

## M4 — Capacity and parallel delivery

**Question:** can two supported harnesses and several permitted machines improve
throughput without increasing rework, cost or data exposure? This produces **beta**.

**Build**

- Second harness sharing the same 9Router instance, adapter conformance suite and version support matrix.
- Multi-runner allowlists/reservations; selection over eligible runner/harness/route tuples.
- Router-sourced availability and usage observations, task admission/backoff and fairness; no duplicate provider-account quota ledger.
- Parallel independent tasks in isolated clones; ordered dependency composition and serial integration.
- Budget/queue/review-backlog visibility. Start with low configurable concurrency.

**Exit evidence**

- [ ] Both adapters pass configuration/isolation, approval-blocking, cancellation, structured-output, crash and router-failure tests on the supported matrix.
- [ ] Removing an eligible runner never redirects work to an unlisted machine; a locally reserved runner rejects a relabelled wrong repository.
- [ ] Both harnesses use the same centrally managed subscription connections. Separate subscriptions remain separate capacity; duplicate aliases do not create extra capacity; unsupported headroom/reset displays unknown.
- [ ] Router-managed provider/account fallback stays within the approved route. Gaffer retries only terminal task failures, after reconciling the prior attempt.
- [ ] Dependent tasks receive exact prerequisite artifacts; rejecting/changing a predecessor invalidates downstream candidates.
- [ ] Deliberately overlapping and semantically conflicting patches are caught during integration/verification; clean application alone never passes the gate.
- [ ] Twenty paired tasks compare whole-product value with the standalone-harness baseline; separately compare parallel Gaffer with sequential Gaffer on suitable task cases. Count capture, planning, approvals and failed-run effort. Target median operator effort is at least 20% lower than the standalone baseline; parallel mode must improve on sequential mode for its enabled task classes. Apply the predeclared PRD defect gate (zero critical defects and no more major defects than the comparator over seven days), and report spread and sample limitations.

**Decision:** parallelism stays optional if its measured benefit is limited to
specific task classes. Keep sequential mode as a first-class execution policy.
9Router and same-provider multi-subscription support are already alpha foundations.
Beta improves task admission using its signals; it does not promise exact quota
forecasting across every provider.

## M5 — Recurring work

**Question:** can standing authority safely turn selected events into useful work?

**Build**

- Timezone-aware schedules and outbound GitHub polling with durable cursors.
- Trigger templates bound to repository, branch/head, operation class, grant and budget.
- Deduplication, coalescing, bounded catch-up, loop prevention, dry-run and global pause.
- Scheduled in-app digest. External notification delivery is a separate opt-in integration.

**Exit evidence**

- [ ] Repeated delivery and five CI events for the same failing head produce one logical work item.
- [ ] Stale head, foreign branch and untrusted issue instructions cannot expand execution/publication scope.
- [ ] DST transition, missed schedule, daemon restart and polling pagination fixtures show defined, bounded behaviour.
- [ ] A fix that fails CI cannot form an unbounded self-trigger cycle.
- [ ] Dry-run shows the exact proposed authority/action/resource decision without executing it.
- [ ] Fourteen days across two dogfood projects report accepted unattended outcomes, interventions and failures; no minimum busy-hour target.

**Gate:** add public webhooks/chat only when polling does not meet an observed
need and their ingress/authentication design is complete.

## M6 — External usability and v1

**Question:** can another person install, understand, recover and maintain this?

**Build**

- Signed release artifacts for validated platforms; honest daemon versus runner support matrix.
- PWA shell and opt-in offline drafts, foreground sync, duplicate prevention and session revocation.
- Setup, update/rollback, recovery, backup, retention, adapter and security documentation.
- Licence/dependency review, contribution guide, examples and release compatibility matrix.
- Local-only product metrics and explicit third-party network/telemetry documentation.

**Exit evidence**

- [ ] Three independent users reach a verified artifact within 30 minutes after prerequisites; record full prerequisite/install time separately.
- [ ] At least one external user completes the documented restore drill without maintainer intervention.
- [ ] Offline capture survives refresh/reconnect and submits once; approvals/stop never report success while offline.
- [ ] Schema upgrade and restore/rollback fixtures preserve authority and artifacts without replaying old publication actions.
- [ ] No Gaffer telemetry is sent; runtime egress matches the documented provider, forge and operator-configured network calls. Third-party harness behaviour is identified separately.
- [ ] Docs are generated consistently and link correctly; every claimed supported feature has an evidence entry in the release checklist.
- [ ] No unresolved authority, containment or acknowledged-data-loss defect remains in the supported profile.

An external contribution is welcome but is not an exit requirement: its timing is
outside the maintainer's control. v1 is the reliable solo workflow, not completion
of every idea in the original PRD.

## Later experiments and entry conditions

| Capability | Evidence required before implementation |
| --- | --- |
| Automatic memory promotion | Held-out reduction in correction/rediscovery effort with no material false-knowledge increase |
| Learned idioms | Repeated scoped ambiguity and evaluation of wrong application, not just question-count reduction |
| Embeddings | FTS/reference retrieval fails known cases; chosen embedding provider satisfies data policy and cost constraints |
| Auto-merge | A requested narrow policy, reliable candidate/check identity and revocation semantics; GitHub rules still apply |
| In-app hunk editing | Demonstrated forge-review friction sufficient to justify editor and revalidation complexity |
| Browser/desktop runners | Dedicated permission/isolation profile and observed task need |
| Third harness/external adapter ABI | Contributor/operator demand and passing conformance suite |
| Team features/local-only inference | Separate persona evidence and a complete security/deployment design |

## Traceability from the original plan

| Original emphasis | Revised placement |
| --- | --- |
| M0 execution spike | M0 feasibility plus M1 durable executable core |
| M1 intent and four parallel workers | M2 intent flow; parallelism waits until M4 |
| M2 durability and stop | M1 prerequisites |
| M3 full curator, vectors and idioms | M2 minimal memory, M3 measured retrieval; advanced automation later |
| M4 gateway and quota economics | 9Router, named routes and same-provider multi-subscription proof in M0; caps/recovery in M1; richer task admission in M4 |
| M5 cron/webhook/chat | M5 schedules and outbound GitHub polling; ingress/chat later |
| M6 merge queue, budgets, trust | M1 budgets, M2 serial integration/publication, M4 parallel integration; auto-merge later |
| M7 public release | M6 external usability and a narrower v1 |

Story IDs US-A1 through US-E3 remain in the PRD with explicit release labels. Old
numeric story references have been removed. Decisions and metrics are now shared
across the documents rather than separately implied by each milestone.
