# Immutable execution grants (#12, provisional)

Related work: [#12](https://github.com/korallis/letmecook/issues/12).
Operator-authorized reversible application work on merged #11/#10. Closure remains
**deferred pending accepted/locked #9/#10**. No foundation lock, supported runtime,
execution readiness, live inference or original downstream acceptance is claimed.

## Entry points and trust

`internal/authority/grant.go` owns typed envelope validation and deterministic
checks. `internal/store/grants.go` owns immutable records, head CAS and invalidation
transactions in the existing SQLite store. No parallel workflow/store tree, HTTP
mutation API, credentials, repository access, selector or process launcher exists.

**`Store.ApproveExecution` is a trusted operator/standing-policy entry point, not
an authentication mechanism.** Its actor string is only an audit reference. Only
an authenticated owner may eventually call it after approving that exact envelope;
never pass worker/model recommendations to it as approvals. Tests invoke it directly
with disposable synthetic data. Existing HTTP remains read-only and unauthenticated.

- `ApproveExecution(expectedID, grant)` creates revision 1 or a CAS replacement.
  Retained IDs cannot change content; grant revisions increase by exactly one.
  Brief/plan/decision revisions cannot roll back or change content at the same
  revision. New or changed route/profile candidates need a new route-decision
  revision/digest. Existing route/profile identities cannot change at the same
  route revision; limits-profile changes also need a new policy revision/digest.
- `RestrictExecution` can only narrow existing unrevoked authority. Exact unchanged
  envelopes return the existing grant, with no extra approval, row, invalidation or
  budget reset. An inside-envelope route choice is just a request, not a revision.
- `InvalidateExecution` persists revocation or supersession without replacement
  (for a material edit awaiting approval). Replacement approval atomically writes
  the new immutable grant/head and invalidates the old one. Old authority stays
  refused even on replay; explicit fresh approval cannot undo old invalidations.
- `CheckExecution` returns nil or `*authority.Refusal{Code, Field}`. Examples:
  `stale_revision`, `revision_conflict`, `widened_scope`, `expired`, `revoked`,
  `superseded`, `incompatible_route_policy`, `distinct_authority_required`.
  It checks stored head/invalidation before approving the request envelope.
- `ExecutionGrant` reads immutable history, **not** current authority.
  `ExecutionInvalidations(after, limit)` returns ordered durable signals (max 128).
  Consumers retain their own cursor and replay safely; signals have no deletion TTL.
  Startup, checks and signal reads latch elapsed expiries. No background stop loop
  exists. Expiry uses the trusted daemon's wall clock, never worker timestamps;
  once persisted it cannot be undone by clock rollback.

**A successful check is not an admission token.** #15 must reuse the private
`checkExecution` inside the same transaction as reservation/attempt/outbox creation,
not perform a check-then-dispatch sequence. Current full-graph capability, repository
policy, runner policy and isolation eligibility also remain #14/#15 obligations.
#19 consumes invalidation signals to fence leases and reconcile; a signal never
proves local termination, remote quiescence, custody, or safe replacement.

## Envelope and deliberate limits

Grant binds task/actor, repository reference and exact Git base (40/64 lowercase
hex), SHA-256 plus revision for brief/plan/route decision, criterion IDs, task kinds,
exact paths, operations, systems, runners, validity and finite resource ceilings.
Base advancement always needs explicit approval; no implicit rebase policy.

Collections are non-null, sorted, unique and bounded to 128 entries; grant/envelope
serialization caps at 64 KiB. IDs use existing canonical UUIDv4. Paths are exact,
relative portable ASCII file paths up to 512 bytes, excluding traversal, globs,
backslashes and `.git`. No directory-prefix or symlink authority is inferred.
Actual filesystem resolution belongs to the qualified runner. Operations are
`read`, `verify`, `write`; no generic shell, publication or merge operation exists.
Execution action accepts only `execute`; acceptance, publication and merge are
separately refused rather than inferred from plan approval or model output.

Each allowed route/profile includes route/policy/evidence revisions, exact router
build, full graph/settings digests, protocol/harness/isolation, explicit limits
profile/authority and complete provider/model/billing target set. Requests must
match an entire candidate exactly; omitting an inconvenient fallback is not a
narrowing. A pinned grant has one candidate; standing `within-envelope` choice may
select one approved candidate without another approval. Classifier recommendations
and overrides go through the same checks. No account identities, balance ledger,
provider credentials or Gaffer fallback loop exists; 9Router retains ownership.
Digests bind reviewed records; this slice does not claim to resolve/verify live
capabilities or prove graph completeness from a model-supplied list.

Budgets cover logical requests, physical subattempts, execution attempts, retries,
concurrency, request/response bytes, total/attempt/first-output/idle time, provider
output tokens and optional monetary cap. They are ceilings, **not** fresh balances
per revision/check. #15 must retain task-wide charges and unresolved reservations
across revisions, failed/discarded work and restarts. Strict profile requires a
positive provider output cap. Native profile requires explicit authority, exclusively
subscription targets, zero provider output cap and nil monetary cap (unavailable,
not unlimited). A monetary pointer to zero means a real zero-spend requirement.
No strict-to-native downgrade or unsupported cap stripping is accepted.

## Durability and evidence

Persistent schema 3 adds immutable grants, one head per task and append-only
invalidations in the existing migration transaction. Historical fixture schema 1
has no grants. Persistent schema 1/2 migrates without relabelling fixture data;
read schemas accept store-only 2/3 while the current daemon reports 3. Grant task
IDs can precede attempts; they do not create runnable tasks in the old snapshot.

Run:

```sh
go test -race -count=1 ./internal/store ./internal/authority
```

`internal/store/grants_test.go` exercises real public grant methods on disposable
SQLite state: exact bindings, replay/no-op, concurrent revision conflict, narrower
versus material changes, strict/native and fallback mismatch, invalid model-derived
permissions, hostile values, revocation/expiry and cursor replay after reopen,
schema 2 migration, SQL immutability/FK constraints, transaction cancellation,
real `SQLITE_FULL`, failed revocation, and owned-subprocess SIGKILL before/after
commit. These prove metadata behavior, not hardware power loss or active-process
termination. No live model, provider endpoint or real repository is contacted.
