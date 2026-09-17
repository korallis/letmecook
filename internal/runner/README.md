# Durable runner supervision

`Create`, `Accept`, `RequestLease` and `ApplyLease` preserve the original
provisional admission/journal semantics. S2 adds an explicitly development-only
launcher, guardian and transport outbox; it does not qualify a supported runtime.

`Options.Policy` is an independent local eligibility loader. `Options.Admission`
is the supervisor's own development-profile flag, not a policy supplied by the
daemon. `Options.Boot` gives all journals of one supervisor the published process
boot. `Open` never resumes a lease, clears quarantine or adopts new generation
identity: it retains recovery evidence, writes a fresh restart boot and stops.

`PrepareLaunch(ctx, LaunchRequest)` refuses absent/currently expired lease,
unqualified profiles and mismatched checkout/profile. Runtime HOME, TMP and
configuration are siblings of the candidate. It prepares an inference boundary;
fake skips inference entirely and is the only harness allowed a nil boundary. Gateway tokens
and credentials are never put into the journal. The job receives only its scoped
token; receipts reserve durably before sending upstream.

`Launch(ctx, LaunchRequest) error` requires durable intent and acknowledged
starting. The guardian is outside the job's process group and sandbox, in its own
session. A supplied `Launcher.Wrap` transforms the adapter command into a guardian
command without changing the job's stdout/stderr. Adapters must use `exec.Command`
(not context-triggered guardian KILL), configure the command before `Wrap`, and
leave stdin/extra files/process attributes untouched afterwards. Descriptor 3 is
private guardian control. The job uses its own PGID and inherited rlimits.

Guardian cutoff is elapsed monotonic duration, capped by a conservative wall-clock
cutoff so wake after suspend cannot extend the lease. Renewal cannot undo an
already latched stop. Supervisor EOF or cutoff sends TERM, KILL at two seconds,
then observes until ESRCH or the five-second bound. EPERM stays unknown. A
process-table observation tracks descendants/escaped sessions; detected escapes
never produce positive whole-tree evidence. Sampling is **not** an adversarial
OS process boundary, and the development profile does not claim otherwise.

Existing `TerminateProcessGroup` remains the normal cancellation evidence path.
The guardian separately writes a file+directory-synced receipt; `WaitGuardian`
journals it and `Open` retains it after supervisor death. Re-signaling recovery
requires matching PID and OS start token; a missing leader is not proof of tree
termination. `RecoveredBoundaryState` counts durable request reservations and
terminal receipts; missing terminal receipts remain remote-work unknown.

New journal events: `launch_intent`, `starting_ack`, `launched`,
`guardian_observed`, `outbox`, `outbox_ack`, `fenced`. Immutable outbox keys retain
exact request bytes and acknowledgments. Existing hash chaining, private
ownership/inode checks, exclusive locks, 128 KiB payload / 8 MiB journal bounds,
and fsync-before-ack are unchanged. A persistence failure poisons the handle;
there is no automatic truncation or reset.

See [the executable's README](../../cmd/gaffer-runner/README.md) for the boot/facts
lifecycle, CLI flags, evidence sequence and limitations, and
[runnerjournal](../runnerjournal) for the storage primitive. Tests retain the
original admission/lease coverage and add real subprocess/TLS/sandbox failures
under `cmd/gaffer-runner`. A nil/unqualified `LaunchRequest` always refuses. `Close` revokes the scoped
boundary while receipt callbacks can still journal; closing the handle never
claims quiescence. A reopened/quarantined handle reports execution disabled.

`RecordHarnessWatermark` retains the complete, acknowledged harness sequence.
`ReleaseHarness` supports the adapter's optional `Release(context.Context,
harness.RunHandle, int64) error` method only after that watermark and an
acknowledged finalize/terminated message. It calls at most once. `Close` also
releases a remaining eligible handle on shutdown; incomplete output/custody is
retained rather than silently released. The fake adapter need not implement it.
