# Provisional durable stop control (#19)

This local library slice persists stop intent, revocation, late evidence and
separate termination observations. It grants **no execution, acceptance,
publication or merge authority**. No stop transport, HTTP/UI controls, delivery
runtime, automatic resume or supported execution profile is added. Runner `Launch`
still refuses. #9/#10 O1–O8 and #89 runtime qualification remain open.

## Durability and admission

`store.RequestStop` requires the authenticated owner credential fingerprint. It
commits the immutable request, affected lease revocations and existing v2 `cancel`
outbox messages together under SQLite WAL / synchronous=FULL. Only afterward does
it measure and persist acknowledgement readiness, then return. That timestamp is
the observed latch-commit boundary, not network delivery. Lost responses replay
the same recorded acknowledgement. Failure before commit rolls back everything;
failure after commit leaves request-only history, never a positive response.
Recovery in another daemon boot records an unknown (nil) request-to-ack duration.
`StopRequests` and `StopTargets` discover retained outbox history after restart;
reading them is not permission to deliver old-boot messages before reconciliation.

Pause-task, cancel-attempt and global-stop suppress matching admission and delivery
inside the reservation transaction. Cancel also holds replacement attempts for
its task. Per this worker packet, pause conservatively **revokes active leases and
requests termination**; this is stricter than spec §2.3's finish-existing pause.
Reconcile that distinction before product/API exposure. There is no clear-latch API.

`grants.go` invokes the same latch path transactionally on supersession, revocation
and discovered expiry, including startup backfill. Invalidated grants stay fenced;
an affected active task stays held even with a new grant. An invalidation with no
active attempt fences only that grant, preserving independently approved future
revisions. Existing `StopDispatch` also invokes the common path.

`RecordControlLease` is an owner-only **potential issuance journal**, not a daemon
lease service or permission to send a lease. It requires durable assignment accept,
current authority, exact v2 identity/boots and explicit expiry margin. Nonces and
message bodies are retained; replay never extends a deadline. A late renewal
latches expiry and is recorded as fenced. Stops revoke every recorded issuance.
Existing slices supply no real execution/delivery leases beyond this import seam.

## Status and evidence

`StopStatus` separates `stop_requested`, `stop_acknowledged`,
`termination_unconfirmed`, `termination_expired` and `termination_observed`.
Expiry requires elapsed time **strictly greater** than the maximum of every
potential issuance's full deadline plus its configured margin (0 < margin <=
60 seconds). A shorter renewal cannot shorten an earlier bound. Reopen uses a
fresh monotonic domain and waits a full 30-second maximum validity plus each
retained margin. These are diagnostic bounds, not qualified runtime measurements.
Unknown issuance history remains unconfirmed indefinitely.

Neither expiry nor reconnect confirms death, clears quarantine, releases capacity
or permits replacement. `ObserveTermination` imports owner-verified supervisor
evidence, checks v2 session, stop identity and exact boots, then commits before
exposing confirmation. The caller must verify the report digest, containment and
provenance; a worker's declaration is insufficient. Old-boot observations remain
history, not current confirmation. Remote-unknown keeps reservations held even
after observed local termination. Existing owner reconciliation is separate.

Late result/renewal messages are retained and refused. `CustodyResult` still saves
verified result bytes and recovery sources, but post-stop results are quarantined,
never current. Pre-stop custody receipts remain immutable history; they do not
restore authority. No artifact, attempt journal, reservation or latch is deleted.

## Local process experiment

`runner.TerminateProcessGroup` accepts an externally owned group for an already
journaled assignment. It syncs request and acknowledgement records, sends SIGTERM,
escalates to SIGKILL after the configured grace, and observes group disappearance
before syncing a separate `terminated` record. Request-to-ack and ack-to-observed
durations derive from Go's monotonic clock; all three wall timestamps are retained.
Missing identity, permission errors, timeout or persistence failure cannot produce
a positive termination result. Replay never signals an old/reused group ID.

The test launches a real parent shell and two children, checks their group IDs,
and asserts complete observed disappearance within five seconds, both with TERM
and forced KILL. This proves that bounded local experiment only. Process groups
cannot contain descendants that escape with setsid/setpgid, defend PID reuse,
bound filesystem-sync latency or qualify suspension/reboot behavior. A trusted
supervisor must supply those guarantees. `Tick` remains a metadata check, not an
autonomous process watchdog; no adapter or harness launch is implemented.

Tests in `internal/store/control_test.go` cover replay, restart, SQL failures/full
disk, SIGKILL before/after latch commit, partitions, maximum issuance margins,
concurrent dispatch/renewal, authority invalidation and preserved late custody.
`internal/runner/termination_unix_test.go` records real stop measurements and tests
session failures, unconfirmed status, journal failure and retained recovery work.
