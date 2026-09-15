# Provisional execution protocol

Issue [#93](https://github.com/korallis/letmecook/issues/93), draft contract.
**Not locked: fresh independent contract review remains required.** This slice
neither accepts nor unblocks #9, #89, #10, original #11, or their dependents.
The ordering exception permits reversible types and synthetic consistency checks,
not a selected foundation, supported runtime or execution authority.

The [M1-01 execution contract](../../docs/contracts/execution.md) develops #10's
state/evidence, acknowledgement, restart/restore and safe-retry rules directly from
these artifacts. Its v2 control-message additions preserve historical v1 fixtures.
Named obligations O1–O8 remain open before #9/#10 acceptance; no runtime is added.

## Scope and interfaces

`protocol.go` and `protocol.ts` expose matching versioned message types and pure
contract checks. `Decode` / `decode` validate bounded untrusted JSON before it
becomes a message. Other exported checks revalidate their typed arguments: static
types are not trust boundaries. A successful check means **contract-consistent**,
never authenticated, authorized, durable, safe to execute or safe to retry.
Go returns named refusals/errors; TypeScript throws named errors for invalid input
and returns named refusals for valid-but-inconsistent comparisons. Fixture drivers
normalize these representations before comparing exact expected outcomes.
There is no runner, network lease implementation, store, inference client, grant,
publication/merge handler, feature toggle or artifact custody implementation.

The fixture driver under `tests/fixtures/protocol/` is a synthetic trace model.
It executes both implementations and compares their outputs with explicit expected
outputs. It is not a process simulator or proof of distributed safety.

## Wire identity and validation

- `Decode` / `decode` accept historical `execution-provisional-v1` and
  `execution-provisional-v2`. Unknown versions return `unknown_version`.
  `CheckSession` / `checkSession` require exact negotiated v2 and a matching
  message version; no implicit downgrade or relabelling of v1 journal records.
  Lease and result-ack pairs also reject mixed versions. The M1-01 contract
  specifies singleton TLS ALPN negotiation; no TLS stack is implemented here.
- Every message has `message_id`, `kind` and `identity` containing `generation`,
  `task_id`, `attempt_id`, `epoch`. IDs/nonces/boot identities are canonical lowercase
  UUIDv4 values; epochs are integers in `1..9007199254740991`.
- Generation is a fresh globally unique restore/execution namespace. A normal
  daemon restart retains generation and allocates a new daemon boot ID; restore
  allocates a new generation. Neither uses wall time as identity. Epoch increases
  atomically for each new attempt of a task, never for message retransmission.
  The future store enforces uniqueness/CAS; these types cannot allocate or prove it.
- Input is at most 8192 UTF-8 bytes, JSON depth at most 32. Duplicate decoded keys,
  trailing input, invalid UTF-8, unknown/missing fields, nulls, invalid scalar forms
  and excessive values reject. Scalar IDs/references use restricted ASCII, hashes
  are lowercase SHA-256 hex, numeric values are finite safe integers. Wire numbers
  use unsigned decimal integer tokens only (no sign, fraction or exponent), checked
  before floating-point conversion so rounding cannot hide out-of-range fractions.
- Message deduplication binds message ID to full validated semantic content.
  Assignment deduplication additionally binds assignment ID to identity, immutable
  input digest and immutable route record, ignoring only message ID. Exact replay
  returns `duplicate`; conflicting reuse returns `identity_conflict`. Deduplication
  history must survive crashes in a future journal; this slice stores none.
- Check current generation, task, attempt and epoch **before** accepting replays or
  acknowledgements. Old generations return `stale_generation`; other non-current
  identities return `stale_attempt`. A future consumer preserves stale evidence
  separately and never promotes it into current acceptance.

Messages: `assign` (assignment ID, input digest, route), `lease_request` (nonce,
runner/daemon boot IDs, request-send monotonic milliseconds), `lease_reply` (same
nonce/boots, validity), `transition` (expected revision, from/to attempt state),
`result` (manifest identity), `result_ack` (same manifest plus durable receipt ID).
V2 additionally defines `accept` (assignment ID and boots), `refuse` (request
message ID and named reason), `cancel` (stop ID and boots) and `terminated`
(stop ID, boots, local/remote observations and evidence digest). V1 rejects these
kinds. All fields not belonging to that kind reject. See the M1-01 wire table for
direction, correlation and owner-evidence preconditions; decoding is not evidence
of durable assignment acceptance or verified stop.

Route records contain only `route_ref`, `decision_digest`, `policy_digest` and
`limits_profile`. References match `[a-z][a-z0-9_-]{0,63}`: no endpoint, URL, account,
credential or free-text payload. An opaque reference is not evidence of eligibility.
The [task-routing contract](../../docs/contracts/task-routing.md) still requires
immutable eligible named-route selection over the complete graph; 9Router alone
owns account choice, credentials and fallback. The
[native-limits contract](../../docs/contracts/native-subscription-limits.md) still
requires explicit native authorization and unavailable provider-bound wording.
These protocol records do not implement grants or either limits profile.

## State and refusal contract

Attempt state changes require matching current identity and `expected_revision`;
revision mismatch or exhaustion of the safe-integer range returns
`revision_conflict`. The future state owner must increment revision on accepted
changes, not replays; the pure check mutates no state. Allowed edges (all other
edges return `invalid_transition`):

| From | To |
| --- | --- |
| assigned | starting, stopping, unknown |
| starting | running, stopping, unknown |
| running | result_pending, stopping, unknown |
| result_pending | succeeded, failed, stopping, unknown |
| stopping | cancelled, expired, unknown |
| unknown | stopping, cancelled, expired, result_pending |
| succeeded, failed, cancelled, expired | none |

Task state is a separate read model: `draft`, `ready`, `active`, `verifying`,
`awaiting_review`, `accepted`, `reconciling`, `blocked`, `failed`, `cancelled`.
No task acceptance or delivery operation exists here. #94 must not equate an
attempt's `succeeded` with task acceptance, publication or merge.

`desired` (`run`/`stop`) is separate from `confirmed_process`
(`not_started`/`running`/`terminated`/`unknown`) and `remote_work`
(`quiescent`/`unknown`). A requested stop never confirms termination. Missing
observations never mean `terminated` or `quiescent`. State edges alone cannot
establish their evidence preconditions: terminal/recovery transitions require the
future owner to verify process and remote evidence, artifacts and authority.
The trace model therefore keeps uncertainty/quarantine independent of phase.
`Observe` / `observe` never clears quarantine, even after termination and remote
quiescence claims; verified reconciliation by a future owner is still required.

## Lease timing assumptions (unmeasured)

Initial lease and renewal use the same runner-initiated request/reply pair.
`sent_ms` and receive observation are in one runner boot's monotonic clock domain;
never compare clocks across machines or boots. Values are nonnegative safe integer
milliseconds. Validity is `1..30000`; trusted local drift and termination margins
must both be positive, and their sum must be strictly less than validity.

`stop_by_ms = sent_ms + validity_ms - drift_ms - termination_ms`.
The watchdog must begin stopping no later than this conservative cutoff; all
replies received at or after it reject as `delayed_reply`. Adding overflow rejects.
A reply matches only the outstanding fresh nonce, identity and both boot IDs
against the trusted current runner/daemon boot context (not merely each other);
mismatch returns `nonce_mismatch` or `boot_mismatch`. Consumed/cancelled nonces
cannot be revived. A renewal sent/received after the existing cutoff cannot revive
an expired lease. The future owner must cancel pending requests upon expiry or
partition and supply the current outstanding-nonce status and prior cutoff to the
check. The trace supplies these inputs explicitly; no pending-request tracker is
implemented here. Network partition requires stop, invalidates outstanding nonces
and marks process and remote state unknown; no timeout proves termination.

The future daemon must durably record each issuance **before sending**, retain the
latest potentially valid lease even after lost replies, and fence new admission on
restart. Its replacement barrier must use its own conservative latest issuance
bound plus drift and termination margin; runner timestamps are not daemon expiry
proof. Boot change cancels pending requests and old local deadlines. Restore also
changes generation. Unknown clock/scheduling/isolation/process or remote-work
assumptions quarantine instead of permitting replacement. This slice supplies no
measured bounds, timer, watchdog, persistence or retry permission.

## Result identity and acknowledgement

Manifest identity is `{manifest_id, sha256, bytes}`; SHA-256 covers exact manifest
bytes, with `bytes` in `1..1048576`. This is an opaque manifest reference, not an
archive implementation or a claim that referenced blobs exist. Manifest content,
path handling, chunking and custody format remain open under M1-01 obligation O6.

A durable result receipt must bind the current full identity and exact manifest.
The pure acknowledgement check requires matching `result` and `result_ack` plus
matching receipt identity/manifest/receipt ID, `artifacts = verified_durable` and
`metadata = manifest_and_result_committed`. Unknown artifacts, event-only metadata
or a well-formed but mismatched receipt return `ack_not_durable`; conflicting
result/ack manifests return `identity_conflict`. Missing or malformed receipt
fields return `malformed`. A receipt is a **trusted-owner evidence claim**, not a worker
assertion or proof supplied by JSON validation. The future custody owner must verify
all digests, atomically promote and sync every referenced blob and manifest, then
commit the manifest/reference and result event before issuing that receipt.

Lost acknowledgement resends the same manifest and returns the same durable receipt;
new message IDs do not create new results. Mere result/event persistence never
creates a receipt. Local recovery retention remains required until durable ack
**and** separate retention policy permit deletion. Result ack is neither successful
verification nor local acceptance nor publication authority. #94 has no custody
owner and therefore cannot issue real result acknowledgements.

## Reconciliation checklist — required before #9/#10 acceptance

The [M1-01 named obligations](../../docs/contracts/execution.md#8-9-reconciliation-obligations)
map these requirements to O1–O8, including every v2 addition. All remain open.

- [ ] #9 independently compares measured foundations; retain, port or discard these
  types without treating sunk effort as selection evidence. Confirm module paths.
- [ ] Reconcile every message, refusal, state edge, fixture and size/version limit
  with selected foundation; define migration/compatibility or change version.
- [ ] Prove durable generation/epoch/revision allocation, assignment/message
  deduplication, transaction/outbox ordering, restore and one-daemon ownership.
- [ ] Measure Docker-free isolation and complete process-tree stop on exact selected
  runtime; retain #89 and all original live/installation gates independently.
- [ ] Establish clock drift, scheduling, watchdog, renewal cancellation, daemon
  issuance/replacement bounds and restart recovery using real adverse processes.
- [ ] Bind route decisions to actual authority and current full-graph evidence;
  preserve native/strict limits and unknown remote-work reservations.
- [ ] Select manifest/custody format; prove sync/crash/disk-full/lost-ack behaviour
  against real blobs and store. Never substitute event durability for custody.
- [ ] Define evidence-authorized recovery and task acceptance transitions separately
  from desired state, result ack, execution, publication and merge.
- [ ] Satisfy the contract-review requirement in #93 before lock and obtain
  independent exact-final-head code review plus actual applicable CI. Head changes
  require renewed review; a different validation reviewer does not satisfy the
  issue's required contract review.

## Checks

See the [fixture guide](../../tests/fixtures/protocol/README.md) for prerequisites,
commands and evidence limits. TypeScript reuses
`experiments/inference-boundary/json.ts` for strict duplicate-key parsing; that
module has no imports, network or execution side effects. CI includes its changes
and reuses the existing pinned TypeScript toolchain without new dependencies.
