# Reconcile before bounded retry (#20, provisional)

Related work: [#20](https://github.com/korallis/letmecook/issues/20) on the
[execution channel](../contracts/execution.md), the
[durable stop control](../../internal/control/README.md) and the
[M1 integration record §8](../decisions/0002-m1-end-to-end-integration.md).
Operator-authorized development path only. No supported execution runtime,
#9/#10 acceptance or downstream readiness follows from this slice; `unknown`
outcomes count as blocking, never as recovered.

## What reconcile does

`internal/reconcile` classifies every non-terminal attempt from retained
evidence and acts only through store writers that re-verify that evidence inside
their own transaction. It runs three ways:

- `reconcile.Startup` after `store.Open` and before either listener opens. A
  failure keeps the daemon closed.
- `reconcile.OnHello` inside `POST /x/v1/session`, after the runner's session row
  is durable. The runner's `journals[]` mark corrupt attempts and confirm
  results the supervisor preserved.
- `reconcile.Sweep` every 5 s (`reconcile.RunSweeps(ctx, deps, 5*time.Second)`
  in `cmd/gafferd`). A sweep retains a report only when the outcome changed.

Each run reads one snapshot per attempt (`store.ReconciliationInputs`: the
dispatch and its acknowledgement, attempt state, leases and whether the
replacement barrier has elapsed, stop targets and the requests behind them,
termination observations, runtime observations, the custody receipt, the result
head, the runner's last session, task latching, grant liveness and the paused
flag) and retains its classifications as an immutable `reconcile_reports` row.
`gaffer reconcile status` (`GET /api/v1/reconcile`, the S4 owner API) reads the
latest report through `reconcile.Reader`.

## Classifications

| Classification | Evidence | Action | Blocking |
| --- | --- | --- | --- |
| `refused_before_accept` | `runtime_observations` kind `refused`, no acknowledgement | release `cancelled`, `not_started`, actor runner principal | no |
| `assigned_undelivered` | no acknowledgement (or accepted with no lease ever issued) | live outbox: none; stop latched, grant dead, daemon restart or runner restart: release `not_started` (`expired` for grant expiry), actor the stop requester when it is a principal | no |
| `terminated_confirmed` | current-boot `control_observations` row: process `terminated`/`not_started`, remote work quiescent, settled boundary | fence to `stopping` if needed, release `cancelled` or `expired` by stop cause | no |
| `terminated_old_boot` | the same evidence from another boot (retained by `ReportTermination` or observed before a restart) **and** the replacement barrier elapsed | release as above | no |
| `lease_lapsed_unconfirmed` | every lease past the barrier, no termination evidence | `FenceAttempt` to `stopping` under the lease-clock stop `execwire.ExpiryStopID(nonce)`; reservation held | yes |
| `remote_work_unknown` | termination reported without quiescent remote work or with an unsettled boundary | none; reservation held | yes |
| `custody_committed_pending_finalization` | non-quarantined current-generation receipt, no finalize | `unknown` or lapsed `result_pending` **with a durable quiescence proof**: `CompleteFinalization` from stored evidence (exit observation at exactly the sink watermark and digest, no boundary receipt in flight, no latch, live grant); without proof the reservation stays held | yes once the barrier elapses without proof |
| `result_pending_remote` | the daemon's exit observation (or, as a hint only, the runner's journal says `result_pending`), no receipt | `unknown` without a stop **and with the exit observed**: `RecoverResultPending` so the runner's upload is admitted; a journal alone changes nothing (the runner re-proposes with its exit evidence) | no |
| `journal_corrupt` | the runner's latest durable hello journal (`runner_sessions.hello`) or the hello being processed reports corruption, or the attempt has no dispatch | none; every store mutation for the attempt refuses `journal_corrupt` until a later hello reports the journal intact | yes |
| `stale_generation` | the attempt belongs to another generation (restored store) | none: neither launched nor released | yes |
| `awaiting_evidence` | nothing provable yet (live lease, barrier pending, accepted without a lease request) | none | no |
| `latch_cleared` | a `cancel_attempt` stop whose attempt is terminal and released | clearing record `latch-cleared:<stop_id>` in `reconcile_reports` | no |
| `retry_planned` / `retry_dispatched` / `retry_refused` | see below | | |

`awaiting_evidence` and `stale_generation` are labels this slice adds to the
record's set so every non-terminal attempt appears in the report. Nothing
releases on a timer, a bare success flag or an absent PID. A release's
`evidence_digest` is the supervisor's report digest or the SHA-256 of the
retained daemon-ledger snapshot (`runtime_observations` kind `reconcile`).

### Quiescence proof for finalization from evidence

Received usage receipts are never proof that the inference boundary drained: a
reservation whose receipt was never posted is invisible in them, so remote work
could still be running. `CompleteFinalization` releases only with one positive,
durable proof, in this order:

1. the runner's retained finalize attestation for the receipt (a `completion`
   or `attestation` record whose settled boundary covers every retained usage
   row and whose stream and exit equal the daemon's evidence);
2. a `launch_intent` with `boundary_port: 0`: the fake harness runs without an
   inference boundary, so no reservation was ever possible (and no usage row may
   exist);
3. a runner-journal attestation (`store.BoundaryAttestation`) bound to the
   dispatch's runner boot and the receipt. `execwire.Journal` carries no
   boundary yet, so reconcile cannot derive this proof on the current wire.

A usage reservation without a terminal receipt refuses regardless. Without
proof the classification stays `custody_committed_pending_finalization`, becomes
`action_required` once the replacement barrier elapses and never releases; the
runner's own finalize (or an owner release with verified provenance) resolves it.

`CompleteFinalization` retains the completion it finalized from (kind
`completion`, with the proof source) carrying the proven boundary. The runner's
later `POST /x/v1/attempts/{id}/finalize` for the same receipt is answered with
the same outcome only when it attests that stream watermark, exit and boundary;
a divergent attestation is `identity_conflict`.
A `--fixture` daemon has no reservations, grants or leases; reconcile reports
nothing for it and retains nothing.

### The replacement barrier

`ReconciliationInputs.LeaseBarrierPassed` is `controlExpiry`: elapsed time on the
daemon's own control clock strictly greater than the maximum over every retained
lease of its full deadline plus its margin. After a reopen the old monotonic
domain is unusable, so each lease counts as 30 s maximum validity plus its
retained 7 s margin from the reopen. An attempt with no lease ever issued has
nothing to wait for. `store.SetControlClock` exists for tests and diagnostics;
production waits.

Termination evidence is *current* only when it was validated on arrival (a
`control_observations` row), under this daemon boot, and the runner has never
run under another boot since the dispatch was admitted. Restart history is
monotonic (`rcRunnerRestarted`: any `runner_sessions` row for the runner with a
different boot and `created_ms` at or after the dispatch); a later hello that
reasserts the original boot does not restore currency, because the session rows
are immutable. A retained report, a report from another daemon boot, or any
report once the runner has restarted is non-current: `ReleaseAttempt` re-checks
all three inside its transaction and requires the barrier, whatever the
classifier decided. The runner's journal entries are read with one rule
(`store.SelectJournal`): within a hello a corrupt entry for a dispatch wins over
an intact one in either order; across hellos the latest hello mentioning the
dispatch wins (`rcLatestJournal`); a hello from another runner never applies.
A retained finalize attestation (proof 1 above) must carry the dispatch's runner
boot and the attempt's receipt, else `boot_mismatch` / `identity_conflict`.

### Reports and retry admission

Startup always retains its report. Every hello and sweep classifies every
non-terminal attempt (a hello's journals apply only to the calling runner's
dispatches), so their outcomes share one key; a report is retained only when
its entries differ from the last retained report of this boot, whatever trigger
produced that one, so identical hello replays and idle hello/sweep pairs do not
accrete rows. Retry admission is a compare-and-dispatch: `PlanRetry` narrows the
request envelope's `attempts`/`retries` ceilings to the predecessor's epoch, so
`store.Dispatch` admits the request inside its own transaction only while that
attempt is still the task's last one and refuses `attempt_ceiling` otherwise;
automatic retries additionally use one intent key per released attempt
(`retryIntent`), re-derive the predecessor's durable cause immediately before
admission and refuse when it is not the allowlisted cause they observed. A
runner journal that claims `result_pending` without the daemon's exit
observation changes nothing: lease-lapse fencing applies exactly as if the
runner had said nothing.

### Stop latches

A `cancel_attempt` latch suppresses admission for its task until the attempt is
terminal and its reservation released. Reconcile then appends a clearing record
(`reconcile_reports.id = "latch-cleared:" + stop_id`, body `LatchClearance`);
the stop, its targets and observations are never deleted. `pause_task` (`gaffer
task stop`, `StopDispatch`), `global_stop` and `authority_supersession` are never
cleared by reconcile: the owner's `gaffer task resume` and `gaffer daemon resume`
append clearance records for task pauses and global stops respectively, not
authority supersession. One predicate decides whether any latch is still in
force, whatever its kind (`rcActiveStopFilter` in `internal/store/reconcile.go`,
over `control_stops s`): `store.TaskLatched`, `ClearLatches`, `RetryInputs.Latched`,
`PlanRetry` and admission/runtime fencing all use it. The sticky `dispatch_stops`
flag follows its own `pause_task` latch (`dispatchID(task, "legacy-stop")`).
Terminal release alone does not clear a cancellation latch: reconciliation must
record its clearance before another dispatch can be admitted.

## Retry

`reconcile.PlanRetry(ctx, deps, taskID)` clears eligible latches for the task,
then requires: every attempt terminal, the last reservation released, the daemon
unpaused, no active latch, a live head grant and headroom under the task's
`attempts`/`retries`/`total_ms` ceilings. It returns a `store.DispatchRequest`
with a fresh intent key that rebinds the last request to the head grant, retains a
`retry_planned` entry naming the prior attempt and its cause, and admits
nothing: `store.Dispatch` re-checks everything and creates the next epoch. The
owner may retry any terminal cause (`failed` included).

`gafferd --auto-retry` (default off) enables automatic dispatch in Startup and
Sweep. The channel's OnHello currently uses the default retry-off dependencies;
it still reconciles evidence. Automatic dispatch runs only for causes `harness_crash`, `lease_expired`,
`runner_restarted` and `daemon_restart`, only for a release this run performed or
a latch it cleared, and never for `failed`, `refused`, operator stops,
`remote_work_unknown` or authority refusals. The store's `one_current_attempt`
index and sequential reservation refuse a second active attempt whatever the
sweep concurrency. `store.ReconcileDispatch` (`gaffer reconcile release`) stays
owner-only for the blocking outcomes.

## Operator flow

1. `gaffer reconcile status` lists every non-terminal attempt with its
   classification, cause and `action_required`.
2. Blocking outcomes need evidence, not time: a supervisor `terminated` report
   with quiescent remote work releases `lease_lapsed_unconfirmed`;
   `remote_work_unknown` needs the boundary to drain or an owner release with
   verified provenance; `journal_corrupt` and `stale_generation` need the
   restore/repair procedure of the backup slice.
3. `gaffer task retry` after a release; `gaffer task stop` pauses a task until
   the owner resumes it.

## Verification on this slice

`internal/reconcile` tests seed real SQLite stores through the exported store
API only: every classification, the barrier via the injected control clock,
old-boot release, `remote_work_unknown` never releasing, custody completion and
the runner's identical finalize replay, result-pending recovery, latch clearing
against a task pause, ceilings and grant revocation, four concurrent auto-retry
sweeps producing one attempt, a SIGKILLed child between the committed
observation and the release repaired at startup, a simulated restored store
(new generation, paused) and 100 retained attempts classified at startup in
well under 5 s. `internal/store/reconcile_test.go` covers each release basis
refusing without its evidence, fence semantics, finalize-from-evidence refusals
(missing receipt, records past the exit watermark, in-flight boundary, stop
latch) and crash hooks before and after every commit.
