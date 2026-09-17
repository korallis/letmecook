# Fenced attempt execution and recovery

M1-01 specification for [#10](https://github.com/korallis/letmecook/issues/10),
15 September 2026. **Provisional, not accepted or locked.** Work may precede #9;
acceptance still requires the selected foundation and the named reconciliation
obligations below. This document grants no execution authority and unblocks no
original downstream acceptance. It does not select a foundation or supported runtime.

The merged [#93 types](../../schemas/execution/README.md) and
[shared Go/TypeScript fixtures](../../tests/fixtures/protocol/README.md) are the
starting contract, not a competing protocol. `execution-provisional-v1` remains
readable in daemon fixture mode. `execution-provisional-v2` adds explicit
assignment/stop/refusal messages using the same identity, validation and checks.
The six existing message payloads retain their meanings. No runner, transport,
store, artifact custody, UI or publication implementation is supplied here.

[Spec §3](../spec.md#3-durable-execution-model) supplies the safety requirements;
[§9.2](../spec.md#92-protocols) supplies the surrounding transport design.
This contract owns execution message semantics: `assign` carries no embedded lease,
and immutable input/authority resolve through `input_digest`.
Streams, upload and bootstrap remain separate bounded interfaces under O2/O6,
not unknown fields that an execution decoder may ignore.

## 1. Identities and durable ownership

| Identity | Meaning and allocation rule |
| --- | --- |
| `task_id` | Stable desired objective and criterion mapping; not one process or one retry. Changing the objective needs a new authorized revision, not recovery that reduces scope. |
| `attempt_id` | Fresh UUIDv4 for one execution of immutable input, route and policy. Reconnect/retransmit retains it; retry allocates another. Never restart an uncertain process under the same attempt. |
| `generation` | Fresh random UUIDv4 for a new execution namespace, including every restore. Equality only: UUIDs have no ordering. Persist the accepted generation and retired generations; only the authenticated, reconciled daemon may introduce a new one. |
| `epoch` | Per-task positive safe integer, increased atomically for each new attempt, never reused within a generation. Restore changes generation even if a backed-up epoch is reused. Exhaustion blocks admission; never wrap. |
| `assignment_id` | Stable outbox/journal key for the full identity, immutable `input_digest` and route record. A second assignment ID for the same attempt must not create a second launch. |
| `message_id` | Stable identity of one semantic message; retries may reuse it. Persist its semantic content and response. Reuse with different content is `identity_conflict`. |
| `runner_boot`, `daemon_boot` | Fresh UUIDv4 per supervisor/daemon incarnation, including process restart. A boot identifies one local monotonic clock domain, not just an OS boot. Never infer current boots from two mutually matching messages. |
| row revision | Positive safe integer for daemon-owned attempt state, initially 1. Accepted transition increments once by CAS; duplicates do not. |

Exactly one daemon owns the live state directory under an OS lock. In one durable
transaction it checks current authority, reserves task capacity/budget, increments
epoch, creates the immutable attempt and assignment, and inserts the outbox record.
Commit before delivery. The runner serializes admission for that full identity and
journals the assignment before `accept`. A crash between launch intent and process
observation is `unknown`, never evidence that launch did not happen. Recovery must
reconcile the recorded supervisor/process-tree identity instead of launching again.

Check authenticated peer/role, exact version, current generation, task, attempt and
epoch before deduplication, transitions, lease issuance or current result selection.
An unknown/future identity is not permission to advance local state. `CheckReplay`
is only a pairwise comparison: the owner must also enforce unique attempt and
assignment keys across its whole retained journal. Keep tombstones until the entire
namespace is retired and fenced; do not use a short replay TTL to forget launches.

The route record is exactly #93's safe reference/digest/limits-profile record.
Resolve `input_digest` to verified immutable input and authority records, including
repository mapping, brief/plan revisions, base/context, grant, isolation and full
route decision. An unresolved digest or route reference blocks admission. The
[task-routing](task-routing.md) and [limits-profile](native-subscription-limits.md)
contracts still apply: all inference goes through 9Router; account selection,
credentials and fallback remain its responsibility. Execution, local acceptance,
publication and merge require distinct authority.

## 2. Version and wire contract

Use the design's runner-initiated TLS connection with authenticated, revocable
runner identity. For this specification the execution channel requires the exact
TLS ALPN selection `execution-provisional-v2`. Offer/select only that execution
version; missing selection, no common version or any other selected value closes
the execution channel **before assignment or lease exchange**. No automatic fallback
to v1, prefix/major-version matching, or retry with stripped fields. Transport
binding and peer provisioning remain O2; this is a singleton negotiation contract,
not an implemented TLS stack. Every message must also carry that selected version.
`Decode` alone accepts historical v1 fixtures and therefore is not a session check.
`CheckSession` / `checkSession` enforce current identity and the exact selected v2
string; their caller must obtain that selection from TLS, never a worker field.

A reconnect negotiates again, establishes trusted current boots and generation,
and reconciles journal state before admission. A generation update is not learned
from an ordinary `assign`, lease or result. Bootstrap/reconciliation exchanges
are an authenticated control-plane obligation (O2), not fabricated execution
messages containing a dummy attempt. A changed generation first stops/fences old
work; acknowledging a generation change alone cannot prove that work stopped.

All execution messages use `{version, message_id, identity, kind, ...payload}`.
The closed union and exact limits are defined by `protocol.go` / `protocol.ts`:
8192 UTF-8 bytes, depth 32, canonical lowercase UUIDv4, lowercase SHA-256 hex,
unsigned decimal safe integers, no duplicate decoded keys, unknown fields, nulls,
trailing values, invalid UTF-8, fractions or exponents. Every actual boundary
revalidates input; static Go/TypeScript types convey no trust.

| Kind | Direction | Payload beyond the common envelope |
| --- | --- | --- |
| `assign` | daemon to runner | Existing `assignment_id`, `input_digest`, `route`. Assignment is not a lease or launch permission. |
| `accept` | runner to daemon | `assignment_id`, `runner_boot`, `daemon_boot`; exact durable assignment acknowledgement. |
| `refuse` | either peer | `in_reply_to` message ID, `reason` from the refusal enum (excluding `ok`/`duplicate`). No reflected payload or free-text error. |
| `lease_request` | runner to daemon | Existing fresh `nonce`, both boots, runner-local `sent_ms`. |
| `lease_reply` | daemon to runner | Existing matching `nonce`, both boots, `validity_ms` in 1..30000. |
| `transition` | runner proposal to daemon | Existing `expected_revision`, `from`, `to`. Daemon validates evidence and durably applies CAS; this message alone is not an acknowledgement. |
| `cancel` | daemon to runner | `stop_id`, both boots. Revocation/stop is sticky for this attempt, regardless of phase revision. |
| `terminated` | runner supervisor to daemon | `stop_id`, both boots, `confirmed_process` (`not_started`, `terminated` or `unknown`; unknown never releases), `remote_work` (`quiescent` or `unknown`), `evidence_digest`. |
| `result` | runner to daemon | Existing `manifest = {manifest_id, sha256, bytes}`. |
| `result_ack` | daemon to runner | Existing same manifest plus stable `receipt_id`. Custody acknowledgement only. |

`accept`, `refuse`, `cancel` and `terminated` require v2; v1 rejects them.
New fields/kinds must not be silently introduced into a deployed closed union:
change the negotiated version and reconcile journals before use. Never relabel an
old journal message as v2 in place. A v1-only peer remains fixture/read-only until
explicit migration; this spec supplies no executable upgrade or live consumer.

`refuse` correlates to a retained request and the same full identity. It never
changes execution state or proves cancellation. Malformed/unauthenticated input
without trustworthy correlation is discarded/connection-closed, not reflected
into a fabricated identity. `reconciliation_required` means valid shape but missing
required owner evidence; no admission. Other named refusals retain #93 meanings.
A receiver must not answer a refusal with another refusal.

### Acknowledgement points

| Event | Durable point and replay semantics |
| --- | --- |
| Assignment accepted | Runner syncs immutable assignment and acceptance record before sending `accept`. Daemon matches assignment and trusted boots, commits outbox acknowledgement before considering delivery complete. Lost ack resends the same assignment and retained accept; never launches a second process. Accept says journaled, not running. |
| Lease issued | Daemon commits issuance before sending reply; runner receives no authority from assignment or transport success. See section 4. There is no lease-ack round required for the daemon to reserve its full issuance bound. |
| State proposal | Daemon commits CAS and audit event. A lost response requires a current state/revision read or retained message outcome, not blind reapplication of the edge. State sync endpoint/encoding is O2; no new wire acknowledgement is implied. |
| Stop requested | Daemon commits stop, disables new leases/admissions and persists cancel in outbox before sending. Runner syncs sticky stop/cancelled nonces before reporting it. Delivery/receipt of cancel is never stop confirmation. |
| Stop confirmed | Authenticated supervisor's `terminated` resolves to verified process-tree evidence for exact attempt/boots and stop ID; daemon commits that evidence and observation before exposing confirmation. Ack loss replays the same evidence; absence means unknown. |
| Result acknowledged | All manifest/blob custody and metadata committed before `result_ack`, per section 5. A stream event or completed upload request is insufficient. |

Replay recognition never extends a lease, advances a revision twice, selects a new
result or clears stop/quarantine. Durable write failure sends no positive ack and
stops new work. Unknown write outcome is reconciled by identity, not assumed absent.

## 3. State and evidence rules

Task phases remain #93's `TaskState`; they describe objective-level progress, not
process liveness. Attempt `succeeded` can move a task toward `verifying`, never
automatically to `accepted`. Unknown execution requires task `reconciling`; missing
prerequisites require `blocked`. Verification/review/acceptance ownership remains O7.

These are the existing allowed attempt edges; every other edge refuses. Each edge
also needs current identity, authority and expected revision. Structural
`CheckTransition = ok` does **not** check these evidence preconditions.

| From | Allowed to | Additional evidence before applying |
| --- | --- | --- |
| `assigned` | `starting`, `stopping`, `unknown` | Starting needs durable accept, current initial lease, supported isolation and one recorded launch intent. Uncertain launch records go unknown. |
| `starting` | `running`, `stopping`, `unknown` | Running needs observed exact supervised process tree and still-valid lease. |
| `running` | `result_pending`, `stopping`, `unknown` | Result pending needs completed local execution and preserved immutable result; not necessarily daemon custody yet. |
| `result_pending` | `succeeded`, `failed`, `stopping`, `unknown` | Success/failure needs verified outcome, complete result custody, stopped local writers and reconciled remote work. Failure is not a bypass of result preservation. |
| `stopping` | `cancelled`, `expired`, `unknown` | Cancelled/expired needs confirmed local stop (or qualified fencing bound), preserved recovery evidence and remote reconciliation. Expiry is a cause, not proof of death. |
| `unknown` | `stopping`, `cancelled`, `expired`, `result_pending` | Verified reconciliation only; never restart the same attempt. Recovered result pending requires intact validated artifacts. |
| `succeeded`, `failed`, `cancelled`, `expired` | none | Terminal attempt history is immutable. A retry creates a new attempt/epoch. |

Preserve independent `desired`, `confirmed_process`, `remote_work` and
`quarantined` from #93. `stop_requested` sets desired stop without inventing process
exit. Partition, lease expiry and either restart mark process/remote work unknown,
set desired stop and latch quarantine. Local process termination does not prove
remote inference stopped. `Observe` deliberately never clears quarantine, even
after both positive observations; a separately authorized reconciler must verify
provenance, retained artifacts, all reservations and fencing before clearing it.

A `terminated` report references the retained `cancel.stop_id`, or a fresh
supervisor-created stop ID for autonomous watchdog expiry. In the latter case the
supervisor's journal must bind the stop cause and cancelled nonce set to the attempt.
`not_started` requires durable no-launch evidence and disabled future launch;
absence of a PID is insufficient. `terminated` requires the entire contained tree
and descendants stopped, with no surviving writer capability. `evidence_digest`
references a verified supervisor-owned journal/report, not worker stdout. Its
serialized evidence format and measurement remain O4. `remote_work = unknown`
keeps reservations/replacement blocked even when local stop confirmation is valid.
An old-boot report may be retained as recovery evidence but not consumed as current
confirmation without separate reconciliation. A worker cannot self-clear quarantine.

Stop wins over result selection in the daemon transaction order. After committed
stop/revocation, result bytes may be retained as recovery evidence, but never
promoted to successful task acceptance or publication. A previously committed
custody receipt remains custody evidence, not restored execution authority.

## 4. Initial lease, renewal and replacement barrier

Timing inputs are trusted supervisor measurements, not values chosen by a worker.
`S = sent_ms`, `R = received_ms` share one runner boot's monotonic clock.
`V = validity_ms`, `D = drift_ms`, `T = termination_ms` are positive integer
milliseconds, `D + T < V`. D bounds relative clock-rate error, timer quantization
and clock/suspend effects over the lease; T bounds watchdog scheduling and complete
process-tree stop latency. Unknown bounds fail closed. Wall clocks and timestamps
from other boots are not deadlines. Suspend must either be included in measured
elapsed time/stop guarantees or invalidate the runtime profile.

1. The accepted runner records a fresh outstanding nonce and S immediately before
   request send. One outstanding request per attempt; retransmission retains nonce
   and S. Initial launch is disabled while waiting.
2. The daemon rechecks generation/epoch, current boots, durable assignment,
   authority/desired state, reservations and runtime bounds. It serializes issuance
   against stop/replacement, records identity, nonce, V and its own monotonic issue
   time I durably **before** reply send. A duplicate nonce returns the original
   reply/issuance, not another extension. Changed content is identity conflict.
3. The runner checks the reply against its retained request and independently
   trusted current boots/identity. The cutoff is
   `stop_by_ms = S + V - D - T`; reject overflow, `R < S`, or `R >= stop_by_ms`.
   On success consume the nonce, install the cutoff in the external watchdog and
   only then permit launch/continued work. Never anchor validity to R.
4. Renewal uses the same handshake with a new nonce/S. Both S and R must precede
   the existing cutoff as well as the new cutoff. If rejected, do not extend the
   installed lease. At/before cutoff begin stop; by `S + V - D` complete tree
   termination under the measured T bound. Stop/expiry/partition/restart cancels
   all outstanding nonces. A late successful reply cannot resurrect stopped work.

The initial design's 5-second renewals, V=30000 and T=5000 are test inputs, not
measured defaults. The shared example uses S=1000, R=1200, D=100, T=5000, V=30000:
cutoff 25900 regardless of delivery latency; receipt at 25899 passes, at 25900
refuses. A renewal with S=6000, R=6200 and prior cutoff 25900 installs 30900.
After old cutoff, it cannot be treated as a new initial lease by passing null.
The caller owns pending-request/lease state; the pure check cannot detect that lie.

For replacement, the daemon uses **its own** clock and retains the latest bound
for every potentially delivered issuance, including lost replies. Let Dd bound
the conversion of the runner's full V to daemon elapsed time and Td conservatively
cover termination/scheduling. A same-boot conservative barrier is
`B = max(I + V + Dd + Td)` over those issuances. New local writers require daemon
`now > B` or verified complete termination with all old launch/lease capabilities
revoked. I follows the request send causally; reply delay cannot extend the runner
cutoff because S, not receive time, anchors it. Dd/Td must be measured in the
selected profile; they cannot be inferred by comparing S with I. A barrier alone
never proves remote quiescence or makes an uncontained runner safe.

On daemon restart, retain generation, allocate/persist a fresh boot, close admission
and mark active attempts unknown. Old boot replies reject once the runner learns
the boot change; disconnected runners still obey their already installed cutoff.
Do not subtract an old monotonic timestamp from the new boot clock. After acquiring
exclusive ownership and disabling old issuance, wait a **full** maximum validity
(30000) plus measured Dd/Td on the new monotonic clock, or obtain verified stop for
every affected attempt. Include every possibly issued lease from the durable
journal. Unprovable history, ownership, suspend or timer assumptions quarantine
instead of using this bound. Reconnect does not resume an expired process.

Restore starts paused with a fresh generation. Retire the restored outbox, never
blindly replay it. The backup may omit later issuances: fence the original daemon
and every known possibly active runner; if the population or exclusive ownership
cannot be established, stay quarantined indefinitely. Only then can a full new-boot
maximum-lease barrier or verified stop be used. A new UUID does not physically stop
the old daemon or authorize two writers. Multi-daemon consensus is out of scope.

## 5. Result identity and custody

Retain #93's `Manifest`: `manifest_id` is a stable UUIDv4, `sha256` covers the exact
immutable manifest bytes, `bytes` is their exact length in 1..1048576. These are
not Git commit IDs and not a digest of parsed/reformatted JSON. A retry sends the
same tuple; changed manifest ID, hash or size conflicts with the selected result
for that attempt. Bind one final result identity to full generation/task/attempt/
epoch. New message IDs cannot create another final result. `CheckReplay` does not
deduplicate different result message IDs; the custody owner's unique result key and
`CheckAck` supply that separate invariant.

The manifest must bind full attempt identity, immutable input/base identity, exit
outcome, summary/verification references, and the complete candidate artifact set.
Each referenced blob needs byte length, digest and role; paths must be validated
against traversal, absolute paths, duplicate aliases, symlinks and unsupported
filesystem shapes before materialization. Include tracked diff, explicitly included
untracked/binary changes and recovery evidence; detect/block unsupported submodule
or LFS content, never silently omit it. No credentials or transient build output.
The exact archive/manifest serialization, path normalization, total blob/upload
limits and resumable transfer format are O6; until selected and validated, these
opaque references cannot be acknowledged as real custody.

The custody owner validates all bounds/hashes, syncs temporary complete blobs,
atomically promotes them on the same filesystem, syncs containing directories,
and does the same for the manifest. It then commits manifest/blob references,
result event and the stable receipt ID in one durable metadata transaction.
**Only after both custody and metadata durability may it send `result_ack`.**
A crash before the commit can leave collectable orphan blobs, not an ack. A crash
after commit/before ack is resolved by replaying the same receipt. Disk-full, sync,
rename or commit failure withholds ack; unknown commit outcome requires a receipt
lookup. Never mark committed references durable because an event row exists.

`CheckAck` requires the exact result/ack/receipt identity and manifest,
`artifacts = verified_durable`, `metadata = manifest_and_result_committed` and
matching receipt ID. Those flags are trusted custody-owner claims, not proof from
untrusted JSON or worker declarations. See the
[provisional daemon's capability limits](../../cmd/gafferd/README.md#store-correctness-and-limits).
Keep runner recovery state until durable ack **and** separate retention policy
allow deletion. Unacknowledged local data can be lost on disk destruction; report
that loss. Native session resume requires intact compatible session/environment,
not just a transcript. A custody ack is not verification, local acceptance or
publication/merge permission.

## 6. Safe retry versus quarantine

“Safe retry” means eligible to propose a **new** attempt under renewed admission
checks, not exactly-once execution or automatic permission from a timeout.
All rows also require preserved/verified recoverable input, remaining bounded
budget/capacity, unchanged authorized objective and current eligible route/profile.

| Prior situation | Required decision |
| --- | --- |
| Lost assignment ack, same immutable assignment | Retransmit, do not retry execution. Reconcile launch journal if uncertain. |
| Lost result ack | Resend same manifest; return retained durable receipt, no rerun. |
| Proven never started, launch disabled | Reconcile/retire old attempt, then new epoch may be admitted. |
| Complete local stop or qualified lease barrier; remote work quiescent; artifacts intact; quarantine explicitly reconciled | New isolated attempt may be admitted. Lease barrier needs proven containment/watchdog, not merely elapsed wall time. |
| Partition, unknown process tree, lost/corrupt journal, unsupported isolation, unknown timing/suspend bound | Stop new placement, preserve evidence, quarantine; no concurrent retry. |
| Local process dead but remote inference/action unknown | Keep reservation and reconciling state; no replacement, refund or completed policy drain. |
| Stop/revocation | No retry under revoked authority. Renewed authority still requires reconciliation. |
| Unknown publication/push/PR outcome | Reconcile by approved external action/branch/head identity in the separate delivery subsystem before any reissue. Never replay shell effects here. |
| Disk full or corrupt checkout | Block new work; preserve the only copy. No hot retry loop or automatic discard. |

At-least-once delivery is expected. Arbitrary shell side effects are **not
exactly-once**. A worktree is not a security boundary. Retriable local work needs
the selected OS isolation profile; external effects need separately authorized,
idempotent/reconciled mediation, with no provider/publication credentials in workers.

## 7. Executable expected traces

Run `node tests/fixtures/protocol/check.ts` and `go test ./tests/fixtures/protocol`.
The existing corpus and added M1-01 cases execute the same Go/TS public interfaces
with exact expected outputs; they do not grep source. `trace` orders supplied
checks, while `observe` folds observation state. Neither is a network/process/store
simulator: durable journal/receipt inputs are explicit synthetic claims.

| Trace/case | Required observable result |
| --- | --- |
| `trace_lost_assignment_ack` | Current assignment `ok`; same assignment/new message `duplicate`; changed immutable input `identity_conflict`. |
| `trace_lost_result_ack` | Event-only and missing-artifact claims `ack_not_durable`; matched custody `ok`; resend `ok` with same receipt; changed hash `identity_conflict`. |
| `trace_partition_delayed_renewal` | Cutoff 25900; partition latches quarantine; consumed nonce and expired prior lease cannot renew. |
| `trace_daemon_restart_rejects_pending` | Process/remote unknown on restart; old boot reply `boot_mismatch`; old generation `stale_generation`. |
| `trace_m1_initial_renewal_expiry` | Initial cutoff 25900; fresh renewal cutoff 30900; reply at previous cutoff rejected; cancelled nonce rejected. |
| `trace_m1_process_uncertainty` | Stop request does not confirm; expiry and later positive observations never auto-clear quarantine. |
| `trace_m1_old_generation_messages`, `m1_old_generation_custody_ack` | Every execution message kind from a prior generation rejected, including exact replay and durable-looking ack. |
| `trace_m1_version_refusal`, `m1_v1_control_refused`, `m1_accept_extra_field` | v2 control shapes decode; absent/other selected version and mixed message version refuse; v1 control kinds and extra fields refuse; original v1 fixtures remain valid. |

Complete state-edge matrix, malformed input, byte/numeric limits, stale epochs,
nonce and receipt counterexamples remain from #93. TLS negotiation, physical stop,
durable writes and safe replacement require the downstream evidence below; passing
synthetic traces cannot prove them.

## 8. #9 reconciliation obligations

All obligations are **OPEN**. Acceptance of #10 requires accepted #9 and a recorded
retain/port/discard decision for **every** message, refusal, state edge, fixture,
size/version rule and example here. Cite exact evidence and resulting contract
revision when closing each obligation; this provisional work is not selection
or runtime qualification evidence.

| ID | Obligation before acceptance/use |
| --- | --- |
| O1 — foundation and paths | Reconcile with measured build/adopt/extend comparison, licence/integration cost and actual package boundaries. Preserve Go/TypeScript direction unless the accepted decision changes it; retain/port/discard provisional #93/#94 without sunk-cost bias. |
| O2 — transport and compatibility | Bind singleton ALPN, peer revocation, trusted generation/boot bootstrap and journal/state sync to the selected transport; specify their exact control-plane encodings/bounds. Validate v1 journal migration, v2 session matching, refusal correlation and incompatible/mixed-peer tests. No transport exists here. |
| O3 — durable identity and fencing | Prove unique epochs/assignments/current writer, replay tombstones, revisions, transaction/outbox/ack ordering and exclusive daemon ownership across process crash, disk loss and stale backup restore. |
| O4 — isolation and stop evidence | Measure Docker-free confinement and complete tree termination for exact runtime/adapter; define verified supervisor evidence format, no-launch proof, boot binding and stop/quarantine reconciliation. Historical Docker tests are not runtime qualification. |
| O5 — time and replacement | Measure runner/daemon drift, suspend, watchdog scheduling/termination bounds, nonce cancellation and full restart/restore barrier under delayed messages, lost replies and uncontrolled old processes. Unknown assumptions remain blocked. |
| O6 — manifest and custody | Select exact manifest/archive/path/chunk/size contract and durable filesystem/store operations; prove hash verification, crash/sync/rename/disk-full/lost-ack behavior against real bytes, including unsupported artifact shapes and retention. |
| O7 — authority and recovery | Bind input/route digests to real grants/full-graph evidence; preserve strict/native limits and remote-unknown reservations. Specify evidence-authorized quarantine clearing, task verification/acceptance and separate delivery reconciliation. |
| O8 — review and acceptance | Obtain required independent contract review before lock, exact-final-head code review and actual CI. Record reviewer identity honestly: pipeline GPT-5.6 Sol-review is not Opus and does not satisfy a separately required Opus review. Head changes need renewed review. |

Related work: [#10](https://github.com/korallis/letmecook/issues/10),
[#9](https://github.com/korallis/letmecook/issues/9),
[#93](https://github.com/korallis/letmecook/issues/93).
No closing reference is warranted while these obligations remain open.
