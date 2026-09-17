# M1 development system proof

## 1. Scope and honesty

**Checkpoint evidence, not acceptance.** Issue #24's public-binary system proof
runs on `macos-sandbox-exec-dev`. This is a development profile, unqualified for
unattended execution; `supported` is false. No supported runtime, completed M1
acceptance gate, #9/#10 acceptance, or original downstream acceptance follows.
A failed prerequisite fails its scenario; subsequent effects are not inferred.
The immutable fixture corpus is input, never evidence of execution.

The machine-readable evidence is [m1-acceptance-run.json](m1-acceptance-run.json).
Its `head`, `environment`, `scenarios`, `recovery_samples`, and
`recovery_statistics` fields identify the exact run and observations. This is the completed initial checkpoint, not the pending fixed-integration
rerun. Later integration changes require rebuilt binaries and a fresh full run.

## 2. Environment

The run records actual `sw_vers`, `uname -a`, `go version`, `node --version`,
OpenCode binary SHA-256, checkout SHA, start/end times and the private run root.
Hostnames, non-run-root user paths and structured credentials are redacted before
the JSON is written. OpenCode's installed binary digest is an observation, not
proof of a live execution. Observed versions: macOS 26.6.2 (25G83), Darwin
25.6.0 arm64, Go 1.26.5, Node 26.8.2; OpenCode SHA-256
`16c960ba77421da11b53e785f359b73f328a86118b48feb4af143db5d9afb198`. The starting integration/corpus checkpoint is
`e470d6fc36fd9f6996177325409dfe7556540022` (integration ancestor `d085ec0`).

## 3. Procedure

Run `scripts/m1-proof.sh` on macOS. It builds `gafferd`, `gaffer`, and
`gaffer-runner` with `CGO_ENABLED=0` into `/private/tmp/gaffer-m1-bin`, then invokes
`go test -tags system -count=1 -timeout 90m -v ./tests/system/...`. Each scenario
has fresh state/artifacts, CLI-generated certificates, pinned owner mTLS and the
separate `execution-provisional-v2` ALPN listener. Fake cases run the actual
`gaffer-runner mock-gateway` and an authenticated `facts` probe. No internal
product implementation is imported; only public protocol schemas are decoded.
SQLite is read only after orderly shutdown/checkpoint, using section 7's allowlist.

Amendments to integration record §11's abbreviated transcript:

- `GAFFER_SYSTEM_ROOT` defaults to `$HOME/.gaffer-system/<run>` (0700), not
  `/private/tmp`. The development verifier intentionally refuses canary roots
  inside system-temp exceptions. S-01 positively asserts this refusal and checks
  its diagnostic. All verification uses the explicit development profile; there
  is no unqualified fallback.
- Enrollment initially disables the runner. The owner must explicitly enable it,
  provision independent `--policy` and `--repository-profile` inputs, and register
  and validate the pinned repository before admission.
- `facts` requires gateway configuration (and the mock CA) and increments an
  already valid local policy revision. The proof first imports an explicitly
  unauthenticated revision-1 policy, which cannot admit work, then measures and
  imports authenticated revision 2. This is not a claim that placeholder policy
  digests are measured facts. Every restarted runner is measured/imported again.
- `flow run` needs a stable `--flow-id` and explicit `--fake-spec` to edit a file;
  the abbreviated default is not the desired greeting task.
- The supported offline restore command is `gaffer restore --backup …
  --state-dir … --artifacts-dir …`; there is no `gafferd --restore` flag.
- S-08/S-09 use a test-owned TLS proxy. S-13 additionally holds one committed
  `running` response until the daemon is killed, synchronizing a real crash of
  the trivial in-flight task without altering its fixture or product code.
- Current public event reads have sequence/revision/message, but no timestamps.
  Recovery admission/classification observations are owner-read upper bounds,
  explicitly labelled as such, not fabricated daemon event timestamps.

## 4. Results by scenario

The initial S-01–S-04 smoke passed S-02 and S-03. S-01 refused the unsafe root
but failed its required diagnostic. S-04 never launched: the first lease returned
`409 delayed_reply`, and the attempt became `assigned → stopping → cancelled`.
The subsequent complete S-05–S-13 dry run failed all nine scenarios; attempts
were blocked at launch, not successfully fault-injected. The full checkpoint returned exit 1 in 137.812 seconds. Its actual table follows.

| Scenario | Result | Duration (ms) | JSON pointer |
| --- | --- | ---: | --- |
| S-01 | FAIL | 1487 | `/scenarios/0` |
| S-02 | PASS | 1014 | `/scenarios/1` |
| S-03 | PASS | 2212 | `/scenarios/2` |
| S-04 | FAIL | 2251 | `/scenarios/3` |
| S-05 | FAIL | 1878 | `/scenarios/4` |
| S-06 | FAIL | 1820 | `/scenarios/5` |
| S-07 | FAIL | 1710 | `/scenarios/6` |
| S-08 | FAIL | 1941 | `/scenarios/7` |
| S-09 | FAIL | 1994 | `/scenarios/8` |
| S-10 | FAIL | 31727 | `/scenarios/9` |
| S-11 | FAIL | 18126 | `/scenarios/10` |
| S-12 | FAIL | 12988 | `/scenarios/11` |
| S-13 | FAIL | 54409 | `/scenarios/12` |
| S-14 | SKIP | 0 | `/scenarios/13` |

The initial full run additionally used an invalid event-page limit of128 in a
proof-only revision assertion. It has been corrected to the public maximum50;
a separate S-04 reproduction confirmed revision checks pass while the same
lease blocker remains. That read-decoder failure is not a product finding. The
next full run must replace this checkpoint evidence rather than editing its
failed assertions after the fact.

Confirmed product findings at the initial checkpoint:

1. **First lease mixes clock domains.** `internal/store/execution.go` supplies
   daemon wall-clock milliseconds as `CheckLease.ReceivedMS` for a runner's
   boot-local `sent_ms`. An ordinary first dispatch is rejected `delayed_reply`.
   Reproduce with S-04. Proposed patch: validate lease binding/bounds at issuance
   without comparing clocks from different processes; keep the daemon's durable
   issuance deadline separate, and enforce `CheckLease`'s S/R cutoff on the
   runner's same-boot clock. No product patch is part of this lane.
2. **Unsafe-root refusal loses its diagnostic.** Starting the development verifier
   under system temp exits before readiness but prints only `gafferd startup or
   shutdown failed`. Reproduce with S-01. Proposed patch: safely expose the
   profile and rejected path in the startup error, never gateway configuration
   or credentials. The refusal itself is a correct security control.

Test-only defects found during construction were corrected: required empty fake
`edits` arrays, manifest category decoding, structured verification status,
JSON-in-string redaction, and complete stream windows. These are not product
findings.

## 5. Recovery statistics

The initial dry S-13 ran the immutable 100-task schedule twice with `--auto-retry`.
All 20 scheduled kills were blocked because their attempts never reached running;
**zero real restart samples** were manufactured. Successful samples: 0; blocking
scheduled cases: 20; p50/p95/max: **null, not zero**. Nearest-rank statistics apply
only to successful measured samples; blocking cases fail the gate. No old-lease
barrier is subtracted. The actual restart barrier is 30 seconds maximum validity
plus the retained 7-second margin, not the fixture's abbreviated 30 seconds.

## 6. Fault matrix and immutable fixture discrepancies

Every row retains expected and observed identities, states, revisions, stream
watermarks, custody, reservation and remote-work evidence. A pre-launch refusal
is not proof that the fault mode ran. Unknown process/remote-work remains blocking.
The corpus files are untouched. Known discrepancies to resolve, not silently relax:

- Corpus `edit/create/delete` labels are not the closed authority vocabulary
  `read/verify/write`; the test records that explicit translation.
- Several failed-execution rows demand no custody receipt/no manifest, whereas
  the execution implementation packs and commits failed manifests before release.
  The original fixture expectations remain assertions, not rewritten passes.
- The `hang` fault row describes an inference request hang, but fake bypasses
  inference entirely. The public mock has no `--hang` flag. A fake process hang
  cannot establish an outstanding upstream reservation or its drain behavior.
- Reservations are immutable `dispatches` plus `dispatch_releases`, not a
  `dispatch_reservations` table.
- Recovery's quoted barrier and event timestamp availability differ as described
  in sections 3 and 5. There is no clock manipulation or SQL timestamp injection.

## 7. Read-only invariant queries

The test refuses a nonempty WAL before opening
`file:<state.db>?mode=ro&immutable=1`; a live immutable connection could miss WAL
state and is never used. The first six queries must return zero rows. The final
four retain inventories, without changing any row. This is the entire SQL allowlist.

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

**Not run: zero live requests and zero live receipts.** S-14 is gated behind all
S-01–S-13 passing and explicit `GAFFER_LIVE_GATEWAY=1`. The external config/key is
not copied into the checkout, fixtures, workspace or evidence. An invocation
without that environment may skip only S-14; it cannot turn synthetic failures
into acceptance. Any eventual live result must show the pinned model, nonzero
receipts with `gateway_usage`/`gateway_usage_unknown`, actual job-side credential
read denial, exact greeting bytes, verification and local acceptance. Gateway and
credential appear only as `<gateway>` and `<redacted>`.

## 9. Limitations and open questions

O1–O8 remain open. This checkpoint proves selected development bootstrap and
facts behavior plus fail-closed failures, not an accepted execution/recovery
runtime. Code added for unreachable stages is not execution evidence. No exact
recovery event timestamps are exposed at the public read boundary. Broader #24
requirements (route outage, corrupt checkout, disk-full, artifact promotion
failure, supported Docker-free runtime comparison) are not established merely
by this scenario scaffold. Publication, merge, deployments and package releases
remain separate and were not performed.

## 10. Reproduction and verification

```sh
scripts/m1-proof.sh
go vet -tags system ./tests/system/...
gofmt -l tests scripts
node tests/system/fixtures/check.ts
```

`GAFFER_SCENARIOS=S-04 GAFFER_PROOF_JSON=<outside-repository-debug.json>` narrows a
reproduction only; a filtered run is not the full gate. Retain fresh scenario
roots for diagnosis. Run the coordinator's standard matrix on the final head;
quote its actual exit and tail, never an expected result. `docs/build-reader.ts`
loads only `docs/{README,evaluation,PRD,spec,roadmap}.md`, none changed here;
the standard matrix nevertheless runs docs build/check. Do not edit generated
`docs/index.html` directly.
