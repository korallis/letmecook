# M1 development system proof

## 1. Scope and honesty

**Completed checkpoint, failed acceptance gate.** Issue #24's proof drives public
binaries on `macos-sandbox-exec-dev`: qualification is development, `supported`
is false, and unattended execution is not qualified. No supported runtime,
completed M1 gate, #9/#10 acceptance or original downstream acceptance follows.
A failed prerequisite fails its scenario; later effects are not inferred.
Immutable fixtures are inputs, never evidence of execution.

[m1-acceptance-run.json](m1-acceptance-run.json) is the completed, unfiltered run
at `ee810913c11acc781d5ef423028cc4a3b50566e5`. It replaces the initial
clock-domain-blocked checkpoint. The integration fixes now permit real launch,
custody, verification and local acceptance in S-04. Current failures below are
not the already-fixed first-lease clock comparison or opaque startup diagnostic.
The evidence-only commit containing this document does not change tested code.

## 2. Environment

Actual observations in `/environment`, `/head`, `/started` and `/finished`:

- macOS 26.6.2, build 25G83; Darwin 25.6.0, arm64.
- Go 1.26.5; Node v26.8.2.
- Installed OpenCode SHA-256:
  `16c960ba77421da11b53e785f359b73f328a86118b48feb4af143db5d9afb198`.
  This digest and the synthetic config-isolation probe are not live inference evidence.
- Returned integration/fixture head: `1e83fbe`; proof corrections: `ee81091`.
- Start: `2026-09-17T19:27:42.096116Z`; finish:
  `2026-09-17T19:35:47.718467Z`.
- Fresh private root:
  `/Users/Shared/gaffer-system-501/20260917T192741Z-89896`.
- Full proof and standard matrix ran concurrently. No successful recovery
  percentile is claimed from this run.

The JSON redacts credentials, hosts and user paths outside the run root, including
JSON/NDJSON inside strings and UTF-8 native/prefix bytes stored as base64. The
completed-file audit inspected 2,165 encoded fields and 999 credential fields:
zero detected credential-field, host or non-run-root user-path violations. It did
not read the live gateway config or key. Digests and byte assertions are computed
before redaction; redacted native payloads are not byte-identical replay inputs.

## 3. Procedure

`scripts/m1-proof.sh` builds `gafferd`, `gaffer` and `gaffer-runner` with
`CGO_ENABLED=0` into `/private/tmp/gaffer-m1-bin`, then runs
`go test -tags system -count=1 -timeout 90m -v ./tests/system/...`. Fresh
installations use CLI-generated certificates, pinned owner mTLS/TLS 1.3 and the
separate `execution-provisional-v2` ALPN listener. Synthetic cases run the actual
`gaffer-runner mock-gateway` and authenticated `facts`. No internal implementation
is imported. SQL is limited to section 7, after daemon shutdown/checkpoint.

Explicit amendments to the abbreviated integration transcript:

- The development verifier correctly refuses `/private/tmp` canary roots because
  of system-temp sandbox exceptions. S-01 asserts that refusal and its specific
  diagnostic. The script's default `$HOME/.gaffer-system/<run>` is also unsuitable
  on this host: `/Users/leebarry/.git` makes it part of an operator worktree, which
  repository preparation correctly refuses. This run explicitly selected the
  private 0700 Shared root above, outside both boundaries. Neither refusal is a bug.
- Enrollment starts disabled. The owner enables the runner, independently
  provisions its local policy/profile, and registers/validates the pinned repository.
  Imported revision 1 is explicitly unauthenticated and cannot admit work;
  measured authenticated revision 2 permits admission. Every new runner boot is
  remeasured/imported before new dispatch.
- `flow run` receives a stable `--flow-id` and explicit `--fake-spec`. Acceptance
  is `review.accepted` with decision action `accept`, not task phase `accepted`.
  Trusted check IDs are digests; command names and approval provenance are checked.
- Approvals explicitly allow three attempts/two retries and bounded aggregate
  requests/subattempts. This does not work around the retry-allocation defect.
- Offline restore uses documented `gaffer restore --backup ... --state-dir ...
  --artifacts-dir ...`. There is no `gafferd --restore` flag.
- S-08/S-09 inject transport failures through a pinned TLS proxy. S-13 holds one
  committed `running` reply, then truly SIGKILLs the daemon. The durable attempt
  is in flight; a trivial child may already have exited, so this is not proof of
  a live OS child at every scheduled kill.
- The public events API exposes sequence/revision/message, not timestamps.
  Recovery timing uses explicitly labelled harness observations and event sequences.
  No database timestamp or clock is manipulated.
- S-11's inference hang uses real OpenCode with the public mock's `--hang-after 0`,
  not a fake process hang. The adapter refuses before a request, so the case fails.
- Large stream pages are requested at the legal limit 128. A 503 remains a failed
  assertion; subsequent eight-record pages collect diagnostics without hiding it.

## 4. Results by scenario

The full script exited **1**; Go reported **486.622 seconds**. No scenario filter
was set. The observed table is:

| Scenario | Result | Duration (ms) | JSON pointer |
| --- | --- | ---: | --- |
| S-01 | PASS | 1514 | `/scenarios/0` |
| S-02 | PASS | 995 | `/scenarios/1` |
| S-03 | PASS | 2155 | `/scenarios/2` |
| S-04 | PASS | 4651 | `/scenarios/3` |
| S-05 | FAIL | 2595 | `/scenarios/4` |
| S-06 | FAIL | 70666 | `/scenarios/5` |
| S-07 | FAIL | 68215 | `/scenarios/6` |
| S-08 | PASS | 2810 | `/scenarios/7` |
| S-09 | PASS | 15184 | `/scenarios/8` |
| S-10 | FAIL | 30810 | `/scenarios/9` |
| S-11 | FAIL | 72096 | `/scenarios/10` |
| S-12 | FAIL | 46407 | `/scenarios/11` |
| S-13 | FAIL | 164615 | `/scenarios/12` |
| S-14 | SKIP | 0 | `/scenarios/13` |

S-04 proves exact greeting bytes, durable streams/custody/head, trusted verification,
local acceptance and a second task. S-05's actual runner cancellation acknowledgement
was 92 ms on co-located wall clocks, with process termination/quiescence and release;
retry then failed. S-08 proves byte-identical lost-commit replay and one custody row.
S-09 proves partition cutoff/guardian termination and released expiry. These are
selected development results, not a supported runtime qualification.

### Product findings and minimal proposed changes

All reproductions are scenario selections from section 10. Product files are
read-only in this lane; no proposed change below was applied.

1. **Aggregate allowance leaves no retry capacity (S-05, S-12, S-13).**
   `/scenarios/4/assertions/53`: retry returns HTTP 422 `budget_exhausted: task`
   after confirmed stop/release/resume. `BuildDispatch` copies all 12 aggregate
   requests/subattempts into the first attempt; store accounting conservatively
   charges those allowances and `PlanRetry` reuses them. Raising the aggregate
   ceiling alone cannot help. Minimal change: allocate bounded per-attempt
   request/subattempt allowances with capacity reserved for approved retries, or
   expose an explicit bounded allowance selection; preserve conservative accounting.
2. **Daemon restart leaves admissible termination stranded (S-06).**
   `/scenarios/5/steps/48` remains `stopping`, revision 5, unreleased after the
   bounded wait; `/scenarios/5/steps/41` classifies `lease_lapsed_unconfirmed`.
   The runner journal records `session_stale`, guardian termination and a
   terminated/quiescent outbox. Source inspection shows replay skips refused
   outbox entries and recovery returns early merely because a terminated entry
   exists. Minimal change: distinguish queued/refused from delivered termination
   and replay validated old-boot evidence through a fresh session. Do not release
   solely because time elapsed. The replay mechanism is a source-supported
   diagnosis, not a completed patch validation.
3. **Recovered runner-local stop is not latched (S-07).**
   `/scenarios/6/steps/37/data/body/entries/0` reports `terminated_old_boot` but
   `store refused: reconciliation_required: stop_id`. Guardian EOF/empty pgid and
   fresh facts are present, yet the attempt stays `running`, revision 3, unreleased.
   Minimal change: a validated recovered local-stop latch/handshake that retains
   identity, quiescence and replacement-barrier checks.
4. **Restored quarantine acknowledgement aborts the runner (S-10).**
   `/scenarios/9/assertions/50`: the restarted old journal exits with
   `custody acknowledgement mismatch`. `resumeCustody` treats a quarantined
   receipt as a fatal mismatch. Minimal change: retain quarantine/fencing evidence,
   never promote/finalize an old-generation result, and continue recovery-only
   operation instead of aborting the supervisor. New-generation dispatch was not reached.
5. **Trusted Go verification has no private writable runtime environment (S-12).**
   Tasks 05–07 execute but `go test ./...` fails with
   `go: creating work dir: mkdir /tmp/go-build...: operation not permitted`.
   `/scenarios/11/assertions/198` records only `PATH` in `env_keys`.
   Minimal change: supply explicit private TMPDIR/HOME/cache paths in
   `internal/verification/profile_dev.go`; never broaden sandbox filesystem access.
6. **Public OpenCode settings and adapter disagree (S-11 hang; blocks S-14).**
   Documented `{"model":"gpt-6-astra"}` is accepted at task creation but produces
   `opencode_run_refused` in `/scenarios/10/steps/507`; the attempt never reaches
   running and has zero model reservations/receipts. The adapter only allows empty
   settings, while public workflow normalization rejects `{}`. Minimal change:
   reconcile the approved model/variant schema at both boundaries and reject
   mismatches/unknowns without creating new routing authority.
7. **Legal large stream pages overflow the owner response cap (S-11/S-12).**
   `/scenarios/10/assertions/265`: `stream?after=0&limit=128` returns HTTP 503
   `store_unavailable`. Smaller pages recover all 198 records/1,608,636 native
   bytes and the terminal `spool_full` record. Minimal change: byte-bound each
   contiguous response page within the 1 MiB owner limit, preserving watermarks;
   do not make the response unbounded or mistake this for proven durable byte loss.

The standard matrix additionally exposes an **integration-test contract mismatch**:
`cmd/gaffer/review_round2_test.go:176` expects an exactly 65,536-byte owner task
request to return 201; current task normalization reserves the larger real runner
input envelope and returns 422 `oversized`. Both race and CGO-free tests fail
identically. Minimal change in that test: distinguish the HTTP-body cap from the
smaller deliverable task-envelope boundary and retain overflow rejection. This
lane neither changes that test nor weakens the new wire bound.

### S-12 twenty-task corpus

All twenty immutable inputs were attempted sequentially. The following pointers
identify each input and its following command/assertion evidence; PASS/FAIL is the
actual Go subtest result, not merely the terminal execution state.

| Task | Fixture expectation | Result | Observed gate | JSON input pointer |
| --- | --- | --- | --- | --- |
| task-01 | succeeded | PASS | Exact edits, trusted checks and acceptance passed | `/scenarios/11/steps/57` |
| task-02 | succeeded | PASS | Exact edits, trusted checks and acceptance passed | `/scenarios/11/steps/84` |
| task-03 | succeeded | PASS | Exact edits, trusted checks and acceptance passed | `/scenarios/11/steps/112` |
| task-04 | failed_verification | PASS | Expected failed verification and explicit rejection | `/scenarios/11/steps/139` |
| task-05 | succeeded | FAIL | Go verifier cannot create /tmp/go-build | `/scenarios/11/steps/166` |
| task-06 | succeeded | FAIL | Go verifier cannot create /tmp/go-build | `/scenarios/11/steps/191` |
| task-07 | succeeded | FAIL | Go verifier cannot create /tmp/go-build | `/scenarios/11/steps/217` |
| task-08 | retry_then_succeeded | FAIL | Retry refused: budget_exhausted | `/scenarios/11/steps/243` |
| task-09 | stopped | PASS | Cancelled and released | `/scenarios/11/steps/262` |
| task-10 | rejected | FAIL | Large stream page 503; no head to review-reject | `/scenarios/11/steps/283` |
| task-11 | approval_blocked | PASS | Approval blocked, not accepted | `/scenarios/11/steps/327` |
| task-12 | succeeded | PASS | Exact edits, trusted checks and acceptance passed | `/scenarios/11/steps/345` |
| task-13 | succeeded | PASS | Exact edits, trusted checks and acceptance passed | `/scenarios/11/steps/372` |
| task-14 | succeeded | FAIL | README content absent: delete mode deleted it | `/scenarios/11/steps/400` |
| task-15 | succeeded | PASS | Exact edits, trusted checks and acceptance passed | `/scenarios/11/steps/428` |
| task-16 | succeeded | PASS | Exact edits, trusted checks and acceptance passed | `/scenarios/11/steps/455` |
| task-17 | succeeded | PASS | Exact edits, trusted checks and acceptance passed | `/scenarios/11/steps/483` |
| task-18 | succeeded | PASS | Exact edits, trusted checks and acceptance passed | `/scenarios/11/steps/510` |
| task-19 | succeeded | PASS | Exact edits, trusted checks and acceptance passed | `/scenarios/11/steps/538` |
| task-20 | succeeded | PASS | Exact edits, trusted checks and acceptance passed | `/scenarios/11/steps/565` |

`/scenarios/11/steps/593` records 20 wall-time samples: p50 1,672 ms, p95 2,515 ms,
max 7,537 ms. These include failures and are **not** an accepted-work throughput
claim. Only 16 compact `corpus-row` summaries exist: tasks 05–08 fail before that
end-of-case step. Their inputs, commands, assertions and cleanup observations
remain present. Compact row observations precede review; final review is in its
separate command/read/assertion evidence.

## 5. Recovery statistics

S-13 attempted both immutable 100-task cycles with `--auto-retry` and all twenty
scheduled kill positions retained in `/recovery_samples`. There were **two real
SIGKILL/restarts**, one at task 008 of each cycle, **zero successful recoveries**,
and **twenty blocking scheduled cases**. A held unresolved reservation prevents
184 later task dispatches with `concurrency_ceiling`; eighteen scheduled kills
never become admissible and are explicitly `kill_performed: false`. They are not
fabricated restarts.

`/recovery_statistics` records successful classify/recovery p50, p95 and max as
**null, not zero**. Nearest-rank statistics would apply only to successful
samples; blocked cases remain visible and fail the gate. The old-lease barrier
is 30 seconds maximum validity plus 7 seconds retained issuance margin (37 s),
never subtracted. Twenty actual successful samples and recovery p95 <60 s were
**not achieved**. Public event sequence and harness-observation clocks are kept
separate. The current recovery helper requires a second attempt; original-result
finalization is another contract-permitted recovery path not generalized by that
helper and not successfully observed here.

## 6. Fault matrix and immutable fixture discrepancies

All nine rows retain identities, revisions, full stream watermarks, custody IDs,
reservation state and available stop/remote-work evidence. `held` is blocking,
never recovered. The row PASS means its intentional fault expectation matched,
not that unknown work became safe.

| Fault | Row result | Attempt / revision | Reservation | Custody | Stream through / bytes | JSON pointer |
| --- | --- | --- | --- | --- | --- | --- |
| crash | PASS | failed / 5 | released | retained | 2 / 58 | `/scenarios/10/steps/38` |
| approval | PASS | failed / 5 | released | retained | 2 / 97 | `/scenarios/10/steps/93` |
| ignore_term | PASS | cancelled / 5 | released | none | 1 / 41 | `/scenarios/10/steps/151` |
| huge_output | FAIL | failed / 5 | released | retained | 198 / 1608636 | `/scenarios/10/steps/230` |
| create_empty | PASS | succeeded / 5 | released | retained | 2 / 86 | `/scenarios/10/steps/285` |
| delete | PASS | succeeded / 5 | released | retained | 2 / 86 | `/scenarios/10/steps/340` |
| exit_nonzero | PASS | failed / 5 | released | retained | 3 / 98 | `/scenarios/10/steps/394` |
| detached_child | PASS | stopping / 4 | held | none | 2 / 72 | `/scenarios/10/steps/451` |
| hang | FAIL | stopping / 3 | held | none | 0 / 0 | `/scenarios/10/steps/504` |

`ignore_term` records terminated/quiescent stop evidence. `detached_child`
retains unknown stop evidence and blocks release/retry. Rows without an owner
stop view do not invent a remote-work observation. The inference-hang row is
blocked before inference; its zero reservations are not evidence of a drained
upstream request. The public mock now has `--hang-after`; the initial missing-flag
finding is obsolete. Source inspection suggests its simple nonstreaming request
schema may also need alignment with OpenCode's streaming/tool payloads once the
settings gate is fixed; that is an **unexecuted compatibility concern**, not an
observed second failure.

The corpus directory was not changed by this lane. Coordinator commit `1e83fbe`
already corrected crash/approval/nonzero-exit failed-custody expectations and the
37-second/harness-clock recovery method. Remaining exact discrepancies:

- `fault-matrix-v1.json` huge_output still expects "failed manifest (no custody
  receipt)"; this run retains a failed receipt without promoting a head. The
  no-receipt assertion remains failed, not silently rewritten.
- `corpus-v1.json` task-10 expects explicit rejection after huge_output, but failed
  execution has no successful result head and `review reject` is refused.
- Task-14 uses mode `delete` for both deleting the placeholder and writing README
  content. The fake implementation deletes both edit targets, so README content
  is missing. Choose a fixture-compatible per-edit mode/meaning in the corpus or
  clarify the fake contract; this lane changes neither.
- Fixture edit/create/delete labels are explicitly mapped to authority
  read/verify/write and recorded. Fixture README wording that no system tests
  exist is stale; untouched. Reservations are immutable dispatch rows plus
  dispatch-release rows, not a separate `dispatch_reservations` table.

## 7. Read-only invariant queries

There were **220 successful query observations across 22 installations**, including
132 zero-row safety observations. All first-six-query assertions passed. This
proves those stored invariants, not forward progress of held reservations.

The test refuses a nonempty WAL before opening
`file:<state.db>?mode=ro&immutable=1`. It never uses a live immutable connection
that could miss WAL state. First six queries must return zero rows; the final
four are inventories. This is the entire SQL allowlist.

```sql
SELECT task_id,count(*) AS n FROM attempts WHERE state NOT IN ('succeeded','failed','cancelled','expired') GROUP BY task_id HAVING count(*)>1;
SELECT generation,task_id,attempt_id,epoch,count(*) AS n FROM artifact_results GROUP BY generation,task_id,attempt_id,epoch HAVING count(*)>1;
SELECT a.id,a.state FROM attempts a JOIN dispatches d ON d.attempt_id=a.id LEFT JOIN dispatch_releases r ON r.dispatch_id=d.id WHERE a.state IN ('succeeded','failed','cancelled','expired') AND r.dispatch_id IS NULL;
SELECT h.task_id FROM artifact_result_heads h LEFT JOIN artifact_results r ON r.receipt_id=h.receipt_id WHERE r.receipt_id IS NULL OR r.quarantined<>0 OR r.attempt_id<>h.attempt_id OR r.generation<>h.generation;
SELECT a.id,a.revision,max(e.revision) AS event_revision FROM attempts a LEFT JOIN events e ON e.attempt_id=a.id GROUP BY a.id HAVING a.revision<>max(e.revision);
PRAGMA foreign_key_check;
SELECT generation,task_id,attempt_id,epoch,manifest_id,receipt_id,quarantined FROM artifact_results ORDER BY created_ms;
SELECT d.id,d.attempt_id,d.runner_id,d.grant_id,a.state,a.revision,r.body AS release FROM dispatches d JOIN attempts a ON a.id=d.attempt_id LEFT JOIN dispatch_releases r ON r.dispatch_id=d.id ORDER BY d.created_ms;
SELECT id,runner_id,runner_boot,daemon_boot,generation,mode FROM runner_sessions ORDER BY created_ms;
SELECT attempt_id,kind,runner_boot,daemon_boot,body FROM runtime_observations WHERE kind IN ('launched','exit','terminated','completion','stop') ORDER BY recorded_ms;
```

## 8. Redacted live run

**Not run: zero live model requests and zero live receipts.** S-14 was skipped
without `GAFFER_LIVE_GATEWAY=1`; synthetic failures independently prohibit using
the externally provisioned live gateway configuration. That file/key was not
copied into the checkout, fixtures, logs, workspaces or this report. Synthetic
OpenCode probing is not live gateway inference.

An eventual live result must use the pinned model, nonzero
`gateway_usage`/`gateway_usage_unknown` receipts, actual job-side credential read
denial, exact greeting bytes, verification and local acceptance. A prompt/model
marker echo is insufficient. The current completed-shell-output helper has only
unit coverage, not a live denial proof; it also needs exact probe-command binding
before treating a marker printed by an arbitrary shell command as denial evidence.
Receipt count measures model-boundary requests, not every authenticated discovery
HTTP request. The only report spellings for the live host and key are `<gateway>`
and `<redacted>`.

## 9. Limitations and O1–O8 status

O1–O8 remain open. S-01 observes unqualified facts refusal/no launch, not an
attempted unqualified task launch. S-02 establishes enrollment/repository setup;
S-04 supplies the execution proof. Missing successful recovery/live samples,
pre-review compact rows, unavailable daemon event timestamps and the S-13
original-result-finalization measurement gap are explicit above. Code for an
unreached stage is not evidence that it works.

Broader #24 route-outage, corrupt-checkout, disk-full, artifact-promotion-failure
and supported Docker-free runtime-comparison requirements are not established
by this development checkpoint. No product or fixture patch, rebase, push, PR,
merge, deployment or release was performed by this lane. Execution, local
acceptance and external publication remain distinct authorities.

## 10. Reproduction and verification

Use a private 0700 root outside system-temp exceptions and operator worktrees:

```sh
cd /private/tmp/gaffer-work/24-acceptance-gate
umask 077
GAFFER_SYSTEM_ROOT=/Users/Shared/gaffer-system-501/$(date -u +%Y%m%dT%H%M%SZ)-$$ \
  scripts/m1-proof.sh
go vet -tags system ./tests/system/...
gofmt -l tests scripts
node tests/system/fixtures/check.ts
```

For diagnosis only, add `GAFFER_SCENARIOS=S-05` (or another named scenario) and
`GAFFER_PROOF_JSON=/private/tmp/gaffer-diagnostic.json`; a filtered run is not the
full gate. Preserve fresh roots for diagnosis. No failed JSON assertion was
edited after the run. The final full script log is
`/private/tmp/gaffer-s7-final-proof.log`; its actual tail is:

```text
SCENARIO STATUS DURATION_MS EVIDENCE
S-01 PASS 1514 /scenarios/0
S-02 PASS 995 /scenarios/1
S-03 PASS 2155 /scenarios/2
S-04 PASS 4651 /scenarios/3
S-05 FAIL 2595 /scenarios/4
S-06 FAIL 70666 /scenarios/5
S-07 FAIL 68215 /scenarios/6
S-08 PASS 2810 /scenarios/7
S-09 PASS 15184 /scenarios/8
S-10 FAIL 30810 /scenarios/9
S-11 FAIL 72096 /scenarios/10
S-12 FAIL 46407 /scenarios/11
S-13 FAIL 164615 /scenarios/12
S-14 SKIP 0 /scenarios/13
run JSON: /private/tmp/gaffer-work/24-acceptance-gate/docs/evidence/m1-acceptance-run.json
FAIL	github.com/korallis/letmecook/tests/system	486.622s
FAIL
```

`go vet -tags system ./tests/system/...` and `gofmt -l tests scripts` both
returned exit 0 with no output. Their captured logs contain only `EXIT=0`.
The fixture check returned exit 0, with this exact tail:

```text
System fixture corpus verified: 20 unique S-12 tasks across greeting/calc/notes (3 opencode-capable), fault matrix 9 modes, recovery 100 tasks / 10 SIGKILLs, canonical JSON, gen-recovery deterministic (sha256 e535c1cc633c), make-repos.sh executable with pinned SHAs. Fixture data only; no execution evidence.
EXIT=0
```

Helper checks for nested/encoded redaction and credential-marker decoding passed:
`go test -tags system -run '^Test(EvidenceRedaction|EncodedEvidenceRedaction|CredentialProbeEvidence)$' ./tests/system/...`
reported `ok github.com/korallis/letmecook/tests/system 0.400s`; the corresponding
`-race` check reported `ok github.com/korallis/letmecook/tests/system 1.568s`.
These do not imply that the unrun live scenario passed.

The standard matrix was run on `ee81091`:

```sh
/Users/leebarry/.claude/jobs/13633b12/tmp/matrix.sh \
  /private/tmp/gaffer-work/24-acceptance-gate \
  /Users/leebarry/.claude/jobs/13633b12/tmp/s7-matrix.log
```

It returned **exit 1**, not a green suite. Both Go test modes failed at:

```text
review_round2_test.go:176: POST /api/v1/tasks got 422 want 201: {"version":"workflow-provisional-v1","error":"oversized","detail":"task"}
```

The complete bounded matrix summary is retained here so its final passing docs
lines cannot conceal the earlier failures:

```text
=== head ===
ee81091 test(system): reconcile public proof schemas and preserve crash evidence
=== gofmt ===
gofmt-clean
=== vet ===
vet-ok
=== race ===
--- FAIL: TestHTTPSTaskEnvelopeBoundaryAndBareIdentityConflict (1.31s)
FAIL
FAIL	github.com/korallis/letmecook/cmd/gaffer	38.768s
ok  	github.com/korallis/letmecook/cmd/gaffer-runner	87.048s
ok  	github.com/korallis/letmecook/internal/backup	58.871s
ok  	github.com/korallis/letmecook/internal/httpapi	42.407s
ok  	github.com/korallis/letmecook/internal/inference	5.455s
ok  	github.com/korallis/letmecook/internal/inference/protocoljson	4.713s
ok  	github.com/korallis/letmecook/internal/reconcile	63.072s
ok  	github.com/korallis/letmecook/internal/runner	8.383s
ok  	github.com/korallis/letmecook/internal/runnerjournal	5.018s
ok  	github.com/korallis/letmecook/internal/store	214.386s
FAIL
race-FAIL rc=1
=== cgo-free ===
--- FAIL: TestHTTPSTaskEnvelopeBoundaryAndBareIdentityConflict (1.43s)
FAIL
FAIL	github.com/korallis/letmecook/cmd/gaffer	39.529s
FAIL
cgo-free-FAIL rc=1
=== protocol ===
239 shared cases: Go/TypeScript expected outputs agree; typed boundaries reject invalid input
=== readapi ===
read API: real CGO-free daemon, store-backed Go/TypeScript schema, bounded filters and adverse wire cases passed
read API: real persistent daemon, empty install and strict mode/schema decoding passed
=== web ===

> typecheck
> tsc --noEmit

=== docs ===

> build
> node build-reader.ts

Reader generated from 5 Markdown documents.

> check
> node build-reader.ts --check

Reader verified: 5 documents, 127 unique anchors, all internal links valid.
=== diff-check ===
diff-check-ok
=== tree ===
 M docs/evidence/m1-acceptance-run.json
EXIT=1
```

`docs/build-reader.ts` reads only `docs/{README,evaluation,PRD,spec,roadmap}.md`,
none changed here. The matrix nevertheless built and checked the reader; generated
`docs/index.html` was not edited. A final evidence consistency/redaction audit,
immutable-fixture diff check and `git diff --check` passed before this evidence
commit. The only post-test changes are this document and the emitted run JSON.
