# Provisional exact-artifact local review (#21)

`Decision` binds accept/reject intent to the selected candidate, manifest digest,
retained verification/evidence IDs, execution-grant ID/revision, criterion coverage
and disclosed limitations. `store.RecordLocalDecision` checks those references in
one transaction. Missing/failed required checks refuse acceptance. Decisions are
append-only; `CurrentLocalReview` derives current intent, rechecks custody bytes and
grant currency, and invalidates on selection replacement or newer verification.
Retries return retained receipts without reactivating stale intent. History survives
reopen and is ordered by commit sequence, not caller timestamps.

These are trusted in-process operator/verifier APIs, not authentication or an
acceptance permission system. A referenced execution grant is provenance only.
Synthetic accepted decisions exercise metadata semantics; the product verifier
always refuses under unresolved #89. This slice grants no execution, acceptance,
publication or merge authority and adds no HTTP/CLI mutation surface.

`FreshInvocation` produces a typed fresh-context packet with exact candidate/base,
grant-bound criterion IDs, actual retained verification, status/limitations and a separately permitted
named reviewer route plus operator-selection provenance. The store records the
packet and refuses reused context IDs, replaced candidates or edited evidence.
It is never sent: no inference, worker-session continuation, account selection or
9Router changes occur. A model/route suffix is not independence or permission, and
review cannot grant authority. #34 owns task-aware selection and future dispatch
must revalidate real route eligibility and freshness.

Tests: `go test -race ./internal/review ./internal/store`. Store tests include
replacement/late replay, failed-check refusal, corruption, real SQLITE_FULL, WAL
reopen and observed owned-subprocess SIGKILL before/after evidence commit. These
are local metadata observations, not hardware power-loss or runtime qualification.
