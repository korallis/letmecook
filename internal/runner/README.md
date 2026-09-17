# Provisional runner admission and lease journal (#16)

This reversible **local library** advances [#16](https://github.com/korallis/letmecook/issues/16).
It does not close that issue or qualify an execution runtime. No executable,
network connection, provider access, process launcher or sandbox integration is
added. `Launch` always returns `runner_execution_unqualified`; no hidden enable
flag exists. #9/#10 reconciliation, #89 measured confinement and #6 real adapter
acceptance remain outstanding. Existing live-trial state is untouched.

## Inputs and trust

`Create` takes an authenticated `Session`, explicit timing bounds, a trusted
monotonic clock, wall clock, and independent runner-local policy loader. Session
generation, peer fingerprint and daemon boot come from a future authenticated
control-plane exchange, never from an assignment. The fresh runner boot is exposed
through `Status` for local-policy publication before accepting work. These are
trusted in-process interfaces, **not authentication endpoints**.

`Accept` consumes existing `store.Dispatch` inputs after authenticated daemon
`Delivery`; reading historical `Assignment` records is not delivery permission.
The caller must authenticate/revalidate current daemon authority. The runner
independently compares the entire locally loaded eligibility tuple against the
supplied mirror, checks repository/root/runner/route and resource policy using
the existing scheduler checks, validates the input/decision/route digests, and
checks finite single-attempt allowances. No new account router or quota ledger.
Synthetic test facts describe no qualified real profile.

Acceptance persists complete immutable dispatch input and the stable v2 `accept`
message before returning it. Exact retransmission returns that acknowledgement;
new message IDs for the same assignment are retained as aliases. Changed message,
assignment or attempt identity refuses. This first slice admits at most **one
assignment for the lifetime of a journal**. Terminal release/new attempts await
supervisor evidence and reconciliation; there is no tombstone deletion or reset.

## Lease and stop behavior

`RequestLease` persists one nonce and its original send timestamp before returning
the request; retries retain both. `ApplyLease` uses the existing v2 `CheckLease`
with retained identity, boots, outstanding nonce and prior cutoff. Responses are
never anchored to receipt time. Duplicate/changed replies cannot extend a lease;
late replies, clock regression, expiry, policy drift and operator stop latch
quarantine and discard pending nonce/deadline authority. An attempt's finite time
allowance caps renewals; grant and local-evidence expiry are checked at admission
and subsequent operations. Failed persistence poisons the handle and signals stop.

`Tick` is an explicit metadata check, **not an autonomous watchdog**. Its returned
deadline is not installed in a production supervisor and cannot authorize launch.
The explicit `TerminateProcessGroup` library experiment can separately journal
observed disappearance of an externally owned process group; see
[`internal/control`](../control/README.md) for its trust and containment limits.
It does not install a watchdog. Local stop never implies remote quiescence.
Future integration must enforce measured suspend-inclusive timing and complete
process-tree termination outside untrusted job code. The clock/bounds callbacks
are testable seams, not measurement evidence or safe production defaults.

`Open` always allocates and persists a fresh supervisor boot, marks the journal
quarantined and clears old nonce/deadline authority, including after clean close.
It never silently adopts caller-supplied new session/generation authority. There
is no resume, generation update or quarantine-clearing API. Reconciliation must
eventually account for old process identities, unknown remote work, artifacts,
reservations and retired namespaces before admitting replacement work.

## Storage and verification

`internal/runnerjournal` owns an append-only, SHA-256 chained log in a new mode0700
owned directory, with a mode0600 single-link regular file and exclusive OS lock on
the directory inode. Creation refuses existing state; opening refuses missing,
torn, corrupt, oversized or unsafe state. Paths must be absolute, canonical and
free of symlink aliases. Filesystem support follows the provisional store's local
allowlist: Linux ext4/XFS/Btrfs, macOS APFS. Other mounts/platforms fail closed.
One record is bounded to 256 KiB, payload to 128 KiB and total history to 8 MiB.
Exhaustion requires intervention; there is no truncation or automatic compaction.

Append acknowledges only after file **and directory** sync; create also syncs the
parent directory. Unknown outcomes poison the handle. Before replaying positive
responses, retained file/directory ownership, inode, permissions and size are
rechecked. Directory/file replacement or ownership loss refuses further work.
Hash chaining detects corruption; it does not authenticate a hostile OS owner,
detect a valid-prefix backup rollback, or qualify hardware power-loss durability.
Only a future paused restore protocol can replace journal state safely.

Run:

```sh
go test -race -count=1 ./internal/runner ./internal/runnerjournal
CGO_ENABLED=0 go test -count=1 ./internal/runner ./internal/runnerjournal
go vet ./internal/runner ./internal/runnerjournal
```

Tests cover concurrent acceptance/lost acknowledgements, identity collisions,
independent local-policy refusals, send-time lease cutoffs, nonce/boot/version
errors, late/duplicate renewals, finite budgets, restart quarantine, real
subprocess SIGKILL/reopen, ownership/replacement and torn log refusal. File and
directory sync failures are injected independently; unacknowledged persisted
bytes remain recovery input rather than being silently discarded. The existing
Go foundation CI includes both packages on Linux and macOS.

Still missing: real transport/bootstrap integration, qualified watchdog and
contained process identity/launch/stop, no-launch evidence, real adapter smoke, suspension
and reboot measurements, result custody, full output spooling and reconciliation.
Passing these tests establishes library behavior only, not supported execution
or original M1 acceptance.
