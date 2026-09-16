# Atomic admission (#15, provisional)

Related work: [#15](https://github.com/korallis/letmecook/issues/15),
[#71](../../docs/contracts/task-routing.md). **Closure deferred pending
accepted/locked #9/#10.** Reversible metadata implementation, not foundation lock,
Docker-free runtime qualification, lease issuance or execution readiness.
No inference, provider client, account selection, semantic planner or HTTP mutation
endpoint is added. Halted `initial_20260915` remains untouched: spent 1, one-use,
unknown original; no retry or new live scope.

## Trust and eligibility

`internal/scheduler/eligibility.go` owns deterministic checks.
`internal/store/dispatch.go` uses the existing SQLite transaction and identity,
grant and repository owners; no check-then-dispatch token or in-memory queue.

- `PublishEligibility(actor, expectedRevision, facts)` imports one exact
  repository/remote/base/root/runner tuple; alias IDs cannot bypass its current
  revision. `actor` must be an authenticated owner
  credential fingerprint obtained after proof of possession, never a request field.
  Owner must independently verify the runner-local mirror and reviewed full-graph
  evidence. Positive booleans are trusted evidence assertions, not probes or worker
  self-attestation. No production profile or live capability evidence ships here.
- Facts include local policy revision/scope, runner boot, exact route/config/build/
  graph/settings/evidence identity, every reachable provider/model/billing target,
  protocol/harness, mandatory capabilities (tools/modalities/verification/OS may be
  named capabilities), context bounds, local/provider bound support, capacity,
  authenticated router access and expiring availability evidence.
- Each full graph target must match the approved grant and have compatible reviewed
  data/billing/capability evidence. Missing fallback support excludes the whole
  route. Graph completeness is an owner-reviewed claim bound to its digest, not
  inferred from a worker list or discovered account. Refresh/revoke facts when the
  router configuration, runtime, runner boot or local policy changes.
- Docker-free isolation must be explicitly supported `native` or `dedicated-vm`,
  match its approved/observed runtime digest, require no Docker and include all
  `RequiredIsolationControls()`. Host shell and worktree-only profiles refuse.
  A real runner must independently enforce current local policy and confinement
  before journaling acceptance; importing metadata cannot replace that boundary.
- `Decision` carries operator-supplied assessment requirements, brief/plan/context,
  classification/evidence/unknowns, version/provenance, authorization reference,
  resources, selected route and rationale-only ranked alternatives. Grant's
  `RouteDecision` revision/SHA-256 must equal canonical decision bytes. Decision
  binds the exact immutable eligibility revision/digest. No M2 planner/UI is needed.
  Optional unknown subscription headroom is recorded, not guessed. Mandatory
  unknowns, unsupported bounds and stale revisions park with named refusals.

## Transaction and replay

`Dispatch(DispatchRequest)` receives one approved choice, not an instruction to try
ranked routes. Request carries a caller-retained canonical UUIDv4 intent key,
current grant request/task ceilings, approved decision and a single-attempt
`Allowance`. In one immediate transaction it checks stop/current grant,
eligibility head, repository selection and enabled unrevoked runner; checks current
attempt and budgets; increments retained task epoch; inserts attempt and protocol
event; then inserts **one immutable dispatch row containing the reservation,
canonical input/decision/facts and outbox assignment**. Commit precedes return.
Partial-current unique index prevents two current assignments per task.

- Same intent/content returns the same assignment, including after commit response
  loss. Attempt, assignment and message UUIDs derive deterministically from intent
  key, so rollback/crash before commit retains the same identities on resubmission.
  Reusing an intent with different content is `identity_conflict`.
- Different intent against unresolved task returns `current_assignment`. M1 is
  deliberately sequential globally: any other unresolved attempt returns
  `concurrency_ceiling`. No parallel worker scheduling or automatic placement.
- `PendingAssignments(after, limit)` provides bounded durable outbox discovery;
  `Assignment(id)` resolves retained immutable input and outbox for reconciliation;
  it is historical data, not delivery authority. `Dispatch` replay has the same
  historical meaning after stop, drift or terminal release.
- `Delivery(runnerFingerprint, id)` rechecks authenticated runner, generation,
  assigned phase, stop/grant, current facts and repository policy. Repeated delivery
  returns exactly the same v2 `assign`, including after lost acknowledgement.
  `AcknowledgeAssignment` validates full identity/assignment/current boots and
  stores the exact immutable `accept` before success. Caller must negotiate v2.
- Restart preserves assignment and charges but existing #11 recovery marks attempts
  unknown/reconciling. Outbox remains readable; new delivery/launch stays refused
  pending reconciliation. No lease or resume API exists. Restore remains #23;
  copying a database is not restore authorization.

Named parked/refusal codes include `no_eligible_tuple`, `stale_decision`,
`stale_evidence`, `invalid_assessment`, `mandatory_unknown`,
`local_policy_denied`, `repository_policy_drift`, `runner_disabled`,
`fallback_evidence_missing`, `fallback_ineligible`, `fallback_capability_missing`,
`hard_bound_unavailable`, `isolation_missing`, `isolation_unsupported`,
`isolation_drift`, `resource_ceiling`, `duration_ceiling`, `attempt_ceiling`,
`budget_exhausted`, `concurrency_ceiling`, `router_unauthenticated`,
`route_unavailable`, `stopped`, `revoked` and `reconciliation_required`.
Refusals expose fixed field names, no reflected prompts or credentials. Storage
errors are errors, never successful parked/admitted records. No parked task write
or scheduler tick loop is needed; caller owns presentation/backoff.

## Charges, stop and release

Task request ceilings must fit both grant and local policy. Separate finite
single-attempt allowance reserves requests, subattempts, duration, output tokens
and cost plus decision's CPU/memory/disk/process resources. Allowance has one
attempt/concurrent process and no execution retry of its own. Local bytes and
first-output/idle deadlines remain in immutable allowance for future enforcement.
Worst-case allowance is charged at commit, including failures/discarded work;
**no refunds**. Counts/retries and sum of allowance charges survive grant revision,
restart and terminal release. Elapsed task time and proposed duration must still
fit total time and grant/local/evidence validity. Unknown prior monetary allowance or native output consumption
cannot become a known balance under a later hard-cost/output grant. Native subscription
profile still rejects hard monetary/output requirements; no cap stripping.

`StopDispatch(owner, taskID)` is sticky and serializes with dispatch/delivery and
reconciliation. For a current dispatched `assigned` attempt, the same durable
transaction inserts the stop latch, applies `assigned` to `stopping`, updates the
task to `reconciling` and appends the transition event. Duplicate stop does not
advance revision or append another event. Missing, unknown, already stopping and
terminal attempts retain their state; unknown attempts still require reconciliation.
Grant invalidation and runner disable/revoke also suppress new admission/delivery.
None confirms termination or releases resources. This bounded admission-store
transition lets owner terminal proof reconcile without a daemon restart. #19 still
owns runtime cancellation/termination orchestration, lease fencing and real stop
evidence. Local-policy tightening changes facts so stale admission and delivery
refuse; it cannot physically stop an already running process here.

`ReconcileDispatch(owner, proof)` is a trusted owner-reviewed reconciliation entry
point, not a worker report. Only protocol edges `unknown|stopping` to
`cancelled|expired` release capacity. It requires exact identity/revision,
not-started/terminated evidence, remote quiescence, revoked launch capability,
preserved artifacts and verified evidence digest. Terminal CAS, event, release
receipt and owner audit identity commit together. Duplicate exact proof is
idempotent; unknown remote work, local timeout, stop receipt or bare process exit
cannot release. Budget charges remain consumed. Success/failure release awaits
real result/custody owners; no premature success/acceptance API is supplied.
A new intent/epoch may retry only after release and full current checks. Task stop
never auto-clears; no timeout or new grant silently resurrects it.

## Evidence

```sh
go test -race -count=1 ./internal/store ./internal/scheduler ./internal/authority
```

`internal/store/dispatch*_test.go` executes public methods on disposable SQLite and
local synthetic Git repositories: concurrent duplicate/competing dispatch, named
refusals, stale graph/billing/isolation/policy, lost ack, immutable input resolution,
stop/revocation races, duplicate stop and concurrent stop/reconciliation, atomic
stop rollback, owner proof validation, terminal reconciliation, retry/budget/resource
ceilings, unknown-headroom strict/native cases, migration, cancellation, real `SQLITE_FULL`
and owned-subprocess SIGKILL before/after commit. Existing read fixtures cover
schema 6 and prior schema reads. No live inference, Docker, private repository,
physical power-loss, runtime isolation, remote stop or artifact custody is proven.
Independent pipeline review is GPT-5.6 Sol-review, not Opus; actual pipeline/CI
results belong to delivery record, not this list of runnable checks.
