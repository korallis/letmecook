# 0001 — Build a narrow execution core; retain 9Router

**Recorded:** 15 September 2026 · **Issue:** [#9](https://github.com/korallis/letmecook/issues/9)

**Decision: build** the small Go workflow/runner core, using SQLite and an embedded
React/TypeScript/StyleX UI; retain 9Router for all model access. Do not adopt or fork
intentic or Claudexor as the application foundation now. This is a bounded design
choice from the evidence available, **not a locked decision, completed M0 gate,
permission to start original downstream issues, or supported execution claim**.
Fresh Opus review and the missing measurements below still block lock/acceptance.

The 15 September instruction to produce the decision now permits an honest record
of unknowns; it does not turn failed or absent observations into passes. No candidate
was installed or executed for this comparison. No product inference, credential
access, live-trial retry, deployment or source import was performed. Use a related-work reference,
not `Closes #9`, until its mandatory comparison/runtime/review gates are satisfied.

## Evidence cutoff and authority

Repository base: `27aa5b367e4ae6a017931e6882fd58598a9efab6` (current `main` at
inspection). The complete issue body and all four comments were refreshed. The
[approval comment](https://github.com/korallis/letmecook/issues/9#issuecomment-5669265822)
resolves the optional native limits profile and three intended baseline outcomes;
the [latest live checkpoint](https://github.com/korallis/letmecook/issues/9#issuecomment-5675624345)
records the failed first trial. Later body amendments require Docker-free execution
and allow only reversible #93–#95 work ahead of the original dependencies. None
waives #71/#1/#8, measured confinement, or fresh Opus review.

Forge observations override stale historical prose and the assignment's assumption
that all three provisional slices had merged:

| Work | Observed delivery at cutoff | What that establishes |
| --- | --- | --- |
| Worker #6 | [Merged worker change](https://github.com/korallis/letmecook/pull/75), head `c4e2f1d580f3a48cdf8ea0676943cffd90fd7579` | Synthetic adapter evidence, not live task acceptance |
| Planner #7 | [Merged planner change](https://github.com/korallis/letmecook/pull/73), head `1a27346fc744b473dea866adb18ea405915f0581` | Restricted synthetic planning and durable proposal evidence, not live acceptance |
| Continuity #8 | [Merged suite/evidence change](https://github.com/korallis/letmecook/pull/85), head `a58b1a52a4d07c538aabf8cd01dea2b246acb88f` | Synthetic continuity plus retained failed live attempt, not two proved subscriptions |
| Protocol #93 | [Merged protocol change](https://github.com/korallis/letmecook/pull/96), head `c698c1c65337bac8c4e46cbe1a06c2029ba8b2db` | Matching provisional Go/TypeScript types and synthetic traces |
| Store #94 | [Merged fixture daemon](https://github.com/korallis/letmecook/pull/97), head `01603f9ab646688384157ed14b99e062b88cb6e6` | Real fixture-only SQLite/read API; no execution authority |
| Shell #95 | [In-flight, pending merge](https://github.com/korallis/letmecook/issues/95); no `web/` or merged shell on this base | Approved scope only; no UI implementation or rendered evidence available to retain |

Earlier evidence documents saying these changes are drafts retain their observation
time and scope. Their merge does not retroactively pass their live criteria.

## Same required workflow

Apply this single public fixture and rubric to both candidates and the proposed
build. It is a comparison specification, **not a record that either candidate ran**.

1. Capture “change `greeting.txt` from `hello\n` to `hello from harness\n`” from an
   online phone/browser. Correct the brief, retaining both revisions. Use the worker
   fixture base `e0c05a1793f5d3719e608bef690e28531a38c33c` and identical public bytes.
2. Record explicit scope, criterion, exact base, finite limits and allowed named
   worker/planner/reviewer routes. Apply [#71](../contracts/task-routing.md): task
   requirements, deterministic full-graph eligibility, eligible operator override,
   immutable decision and a fresh reviewer invocation. No account-level selection.
3. Execute one worker and its verifier in the same newly qualified Docker-free
   profile. Route every model request through the same scoped boundary and pinned
   9Router. Give neither worker nor verifier provider/publication credentials.
4. Disconnect the UI, request stop, interrupt the supervisor, delay a lease reply,
   lose a result acknowledgement and restart from retained state. Record local
   termination separately from unknown remote work; no overlapping valid attempts,
   implicit replay, renewed budget or lost acknowledged artifact.
5. Restore a consistent backup paused, fence old-generation traffic, inspect exact
   candidate/check bytes, and locally accept that identity. Publication and merge
   remain separate authorities. Test publication reconciliation only against a
   synthetic forge: lost response, moved remote head, no overwrite or blind retry.

Use identical route graph, budgets, fixtures and checks; count capture/correction,
setup, review, failures and recovery. A scripted model response tests mechanics,
not intent quality. The preserved Docker runs are useful failure-case inputs but
cannot be relabelled as this Docker-free comparison. Existing native fixtures use
a different recorded base; their timings are not paired candidate results.

**Result today:** zero paired capture-to-review candidate runs, zero real baseline
cases, no measured candidate operator minutes. Untested properties below are
unknown. Missing runtime proof prevents claiming a complete supported path, even
when a daemon installs without Docker.

## Pinned alternatives and source method

Two close candidates from the [M0 shortlist](../evaluation.md#3-build-or-reuse-before-building)
were freshly inspected as public, detached source data on 15 September. Both were
non-archived with GitHub pushes that day; this establishes recent activity, not a
support SLA, release quality or future maintenance commitment. Pins are immutable
commits, not floating `main` or similarly named releases. No install hooks, upstream
tests, candidate daemons, account probes or model endpoints were executed.

| Candidate | Exact pin and declared version | Licence/dependency fit and reuse value |
| --- | --- | --- |
| intentic | [`31faac45ff8d88ead311b7b65b87a966ea367441`](https://github.com/intentic/intentic/tree/31faac45ff8d88ead311b7b65b87a966ea367441); root and sandbox package `0.0.0`, not a release identity | [MIT, Artur Kurowski](https://github.com/intentic/intentic/blob/31faac45ff8d88ead311b7b65b87a966ea367441/LICENSE). [Node 24.21.0 / pnpm 12.4.1](https://github.com/intentic/intentic/blob/31faac45ff8d88ead311b7b65b87a966ea367441/package.json); TypeScript/Node daemon, Vue browser, Rust host CLI, tmux, optional hosted platform/Postgres. Broad browser workflow already exists. |
| Claudexor | [`84f354943c283cf3858558252f4292ecf6fdc4c8`](https://github.com/razzant/claudexor/tree/84f354943c283cf3858558252f4292ecf6fdc4c8); root/package `3.12.0`, not an installed artifact attestation | [MIT, Anton Razzhigaev](https://github.com/razzant/claudexor/blob/84f354943c283cf3858558252f4292ecf6fdc4c8/LICENSE). [Node >=20.19.0 / pnpm 10.34.5](https://github.com/razzant/claudexor/blob/84f354943c283cf3858558252f4292ecf6fdc4c8/package.json); TypeScript packages, file journal, CLI/HTTP/SSE and native macOS UI. Stronger fit for extracting a small durable execution surface. |

Both root MIT grants permit modification/distribution with their notices; neither
is rejected for its root licence. Full transitive/runtime/SDK/native-binary licence
and redistribution compatibility is **unknown**, not inherited from the root MIT
label. intentic's [sandbox manifest](https://github.com/intentic/intentic/blob/31faac45ff8d88ead311b7b65b87a966ea367441/_sandbox/sandbox/package.json)
includes vendor SDKs, native PTY/watcher components and many workspace packages.
Claudexor's [daemon](https://github.com/razzant/claudexor/blob/84f354943c283cf3858558252f4292ecf6fdc4c8/packages/daemon/package.json)
and [control API](https://github.com/razzant/claudexor/blob/84f354943c283cf3858558252f4292ecf6fdc4c8/packages/control-api/package.json)
reuse core/schema/journal/workspace/delivery/secrets packages. Both have pnpm locks;
a lock is reproducibility input, not a dependency audit. No third-party source is
copied into Gaffer. Original Gaffer code still lacks an applied licence; the bounded
[SQLite dependency review](../../internal/store/DEPENDENCIES.md) is retained, while
[#59](https://github.com/korallis/letmecook/issues/59) owns distribution review.

### Source anchors used in the comparison

- **I1:** intentic [topology](https://github.com/intentic/intentic/blob/31faac45ff8d88ead311b7b65b87a966ea367441/docs/architecture/topology.md)
  and [sandbox architecture](https://github.com/intentic/intentic/blob/31faac45ff8d88ead311b7b65b87a966ea367441/docs/architecture/sandbox.md)
  document browser-direct turns, approvals, transcripts, Git history, provider
  runtimes and separate hosted/owned-machine paths. These are documented capabilities,
  not locally validated UX or security results.
- **I2:** [owned-machine connect](https://github.com/intentic/intentic/blob/31faac45ff8d88ead311b7b65b87a966ea367441/_sandbox/ic/src/sandbox/connect.rs)
  runs Docker preflight; [Docker helper](https://github.com/intentic/intentic/blob/31faac45ff8d88ead311b7b65b87a966ea367441/_sandbox/ic/src/docker.rs)
  launches Docker CLI commands. This directly establishes a dependency in that path,
  not that every possible hosted/ported path needs Docker on the operator's machine.
- **I3:** [turn resume](https://github.com/intentic/intentic/blob/31faac45ff8d88ead311b7b65b87a966ea367441/_sandbox/sandbox/src/agent/run/turn/turn-resume.ts)
  owns auth renewal, outage/limit resumption and account moves; [composition](https://github.com/intentic/intentic/blob/31faac45ff8d88ead311b7b65b87a966ea367441/_sandbox/sandbox/src/composition.ts)
  constructs provider runtimes and credential grants. Replacing these with 9Router
  is more than configuring one base URL. No claim that optional resume is always on.
- **C1:** Claudexor [architecture](https://github.com/razzant/claudexor/blob/84f354943c283cf3858558252f4292ecf6fdc4c8/docs/ARCHITECTURE.md)
  documents immutable commands, journal recovery, typed route refusals, artifacts,
  review and API seams. These are meaningful reuse assets, not absent features.
- **C2:** [raw API adapter](https://github.com/razzant/claudexor/blob/84f354943c283cf3858558252f4292ecf6fdc4c8/packages/harness-raw-api/src/index.ts)
  accepts `baseUrl`, but its declared transport is Chat Completions with patch
  envelopes and no effort control. That seam alone does not carry the measured
  native Responses/tool/receipt contract. [Budget routing](https://github.com/razzant/claudexor/blob/84f354943c283cf3858558252f4292ecf6fdc4c8/packages/budget/src/router.ts)
  consults credential-subject cooldown and quota pace through its ledger.
- **C3:** [retired delegated confinement](https://github.com/razzant/claudexor/blob/84f354943c283cf3858558252f4292ecf6fdc4c8/docs/DELEGATED_CONFINEMENT.md)
  explicitly disclaims an outer OS wrapper. The [run loop](https://github.com/razzant/claudexor/blob/84f354943c283cf3858558252f4292ecf6fdc4c8/packages/core/src/runloop.ts)
  calls `spawnProcess` directly and preserves unconfirmed process death. Native
  harness permissions and scoped HOME are not Gaffer's complete confinement proof.
- **C4:** [delivery](https://github.com/razzant/claudexor/blob/84f354943c283cf3858558252f4292ecf6fdc4c8/packages/delivery/src/index.ts)
  has fresh verification, target-preimage recheck and explicit apply/branch/commit/PR
  modes. Its local mutation queue is not evidence of Gaffer lease fencing or remote
  lost-response reconciliation; the latter remain unknown rather than alleged absent.

### Common rubric results

**Partial** means inspected overlap with unmet Gaffer requirements; **fail** means
an identified mismatch in the named path; **unknown** means no adequate observation.
No complete workflow receives a pass. Source findings must not be read as executed
conformance, and a missing README paragraph is never evidence of absence.

| Required property | intentic | Claudexor | Narrow Go build |
| --- | --- | --- | --- |
| Local state ownership/export | Partial (I1): local sandbox files/transcripts/history; complete consistent export unknown | Partial (C1): local journal/artifacts; complete backup/restore fit unknown | Partial: #94 local SQLite fixtures only; #23 backup remains work |
| Phone capture, correction, approval | Partial (I1): browser/editor/approval surfaces; exact revision and phone fixture unknown | Partial (C1): control API/native UI; Gaffer phone flow unknown | Unknown: #95 absent; authenticated M2 flow not built |
| All inference through scoped 9Router | Partial: replace provider/translator/account machinery (I1/I3); integration unknown | Partial: endpoint seam exists, but native protocol and all-role integration unknown (C2) | Partial: real harness/router synthetic evidence; live path failed |
| #71 assessment, eligible overrides, fresh review | Partial: role/persona pins and resume routing (I3); full-graph requirements/overrides/fresh packet unknown | Partial: intent routing/quality tiers and review (C1/C2); replace account/quota ownership, prove full graph and fresh packet | Contract accepted; M1 deterministic enforcement and M2 semantic selector not built |
| Revision/scope-bound execution, acceptance, publication, merge | Partial: approvals/control-token rungs (I1); exact artifact/revision binding unknown | Partial: typed policy and exact-target verification (C1/C4); complete Gaffer authority separation unknown | Unknown in product; #93 types explicitly grant no authority |
| Runner-local policy and tested isolation | Unknown for Gaffer adversarial fixture; per-user sandbox design is not per-attempt proof | Fail as outer delegated boundary (C3); complete native worker/verifier controls unknown | Docker fixture partial; Docker-free #89 unmeasured |
| Lease, partition, unknown remote work, restart | Partial: journal/optional resumption (I1/I3); Gaffer leases/quiescence unknown | Partial: durable commands and unconfirmed death (C1/C3); Gaffer lease/remote-work invariants unknown | Partial synthetic protocol/authority evidence; real runner and network partition unmeasured |
| Exact artifact review/publication | Partial: worktree/history/land surfaces (I1); same fixture and custody unknown | Partial: strong preimage/verification seam (C4); crash custody and remote reconciliation unknown | Partial synthetic candidate custody only; #18/#21/#35/#36 required |
| Dependency integration, paused consistent restore | Unknown for required fixture and restored generation | Partial candidate composition (C1/C4); consistent paused generation restore unknown | Unknown; serial integration #33 and restore #23 not built |
| Docker-free complete installation/operation | Fail for inspected owned-machine bootstrap (I2). Hosted Fly path documented (I1), but full router/worker/verifier/recovery qualification unknown | Partial: native Node CLI/daemon requires no Docker per C1/package scripts; selected worker/verifier confinement and scoped router recovery unknown | Partial: fixture daemon builds CGO-free; worker/gateway/verification experiments still Docker-bound |

## Why build rather than adopt or extend

Adopting either unchanged fails the required control-plane ownership or execution
boundary, despite useful existing workflow. Extending **Claudexor is the strongest
alternative**: injected runner/control API, journal, artifact and review packages
could avoid substantial work. Rejecting it because it uses TypeScript would be
unjustified. intentic could avoid most browser workflow construction, but its
owned-machine runtime, Vue UI, integrated provider layer and hosted topology require
broader change for this particular requirement.

The deciding cost is **which correctness code would still be ours**. Both choices
still need qualified Docker-free worker *and verifier* confinement, 9Router-only
physical-request authority/unknown-work reconciliation, #71 full-graph admission,
and Gaffer's exact revision/artifact grants. Wrapping their existing orchestration
would add a second durable lifecycle to reconcile. Replacing that lifecycle or
account machinery requires a maintained fork across central seams rather than a
small UI extension. A narrow Go core keeps one owner of these specific invariants,
uses SQLite rather than a new journal, and limits first support to one adapter.
This is an engineering inference, not measured proof that building is cheaper.

| Option | Work avoided | New and recurring cost still owned |
| --- | --- | --- |
| Adopt intentic | Browser/editor, sessions, approvals, integrations | Required Docker-free and 9Router ownership changes prevent unchanged adoption; no measured acceptable full path |
| Extend intentic | Keep browser/workspace surface | Replace owned-machine launch and integrated provider/account/resume seams; reconcile grants/custody; either port Vue UI or change UI direction; track Node/Rust/vendor SDK and deployment changes |
| Extend Claudexor | Reuse journal, queue, adapters, review/delivery and control API | Add phone UI and outer confinement; replace credential/quota selection with named-route authority; qualify native Responses and unknown-work semantics; reconcile command/task/lease/artifact identity on every upstream update |
| Build narrow Go core | No external orchestration fork; reuse SQLite, Git, OS mechanisms, OpenCode and existing 9Router experiments | Own grants/outbox/leases/stop/journal/artifact custody/restore and later publication correctness; requalify each OS, harness, SQLite and router upgrade; independent fault review is mandatory, not optional overhead |

No credible candidate integration-day measurement exists. Do not manufacture a
numeric advantage for the build. Limit it to the package plan below. **Revisit before
#11 production promotion** if a pinned Claudexor adapter-only proof can carry the
same fixture through one lifecycle, remove account routing, and demonstrate all
required boundaries with fewer owned changes. Discarding all #93–#95 code would be
acceptable. Its sunk effort was excluded from this comparison and savings estimate.

## First harness and runtime: target versus support

**First adapter target:** OpenCode **1.18.30**, source
`3104c1428ec91f809e5ab86631300de41eb6952e`, binary SHA-256
`01edb5839aa10d5b09133fedcb335a062ecad6e82552933bb14f71756f2b296b`.
Use the existing native Responses profile
`opencode-1.18.30-responses-apply-patch-v1`, not an assumed Codex/Claude CLI contract.
The API planner retains its separate `planner-probe-responses-read-file-v1` profile.
Astra/xhigh wire preservation is synthetic evidence, not live model availability.

**Application target:** Go **1.26.5**, stdlib HTTP and `database/sql`, existing
`modernc.org/sqlite v1.59.0` (SQLite **3.53.4**), embedded React/TypeScript/StyleX
assets. Build tooling may use Node; the product UI needs no separate Node server.
The pinned router still needs its own Node/runtime and reviewed authority extension:
“Go daemon” does not mean the complete installation is one dependency-free binary.

**First supported worker/verifier runtime: none yet.** Proposed first qualification
is one operator-selected Docker-free Linux OS/native or dedicated Linux VM profile
under [#89](https://github.com/korallis/letmecook/issues/89), not generic Linux,
macOS, Windows, systemd or a VM brand. Exact kernel/architecture/mechanism/digests
and resource bounds remain unknown. Port the launcher only after its proof; preserve
separate trusted gateway, private state/control, network-denied worker, scoped UDS
inference, clean HOME/config and full-process-tree stop. Verification executes
repository code and needs the same proven boundary. Reproduce ambient plugin,
hooks/symlink, egress/DNS/IPv6, CPU/memory/PID/disk, interrupted launch/reboot and
watchdog failures on that exact profile and then integrate the actual adapter.

Retain pinned 9Router **0.5.75**, source
`17c4cc76877bd1755030a8414f8d0083f48dcccf`, as the integration reference, not a stock
runtime readiness claim. Source/overlay/dependency/egress attestation and receipt
semantics must survive Docker removal. Existing native 768 MiB worker/gateway
observations are starting measurements only; the historical 512 MiB OOM stays
failed. No resource limit can be omitted to make a new profile pass.

## Measured evidence and its limits

| Packet family | Retained observation | Decision consequence |
| --- | --- | --- |
| [Baseline](../evidence/baseline.md), [native synthetic case01](../evidence/baseline-native-case01.md) | Approved three intended outcomes; zero registered/started real cases or human output labels/minutes. Two synthetic mechanics runs, six retained development failures; inherited invented audit still fails | No product value or throughput estimate from a toy edit; #1 remains incomplete |
| [Worker](../evidence/first-harness.md), [native integration](../evidence/first-harness-native-integration.md), [builtin audit](../evidence/first-harness-native-builtins.md) | Real pinned binary, 24-case Chat/router matrix; separate 19-case native, 11-case entry and four-case final lifecycle packets, not one final-head run. Native candidates bind receipts/durable decisions | First adapter has reusable failure evidence, not arbitrary-repository or live support |
| [Preparation](../evidence/first-harness-native-preparation.md), [legacy closure](../evidence/first-harness-legacy-closure.md) | Preparation is not execution; later import-closure startup regression and corrected source-bound replays retained | Packaging and ambient-state regressions require requalification, not blind experiment imports |
| [Planner](../evidence/planner-probe.md), [native planner](../../experiments/planner-probe/native-integration.md) | 43-case legacy/router suite and separate 17-case native suite; proposal/clarification acknowledgements, one bounded repair and no execution tool | Reuse schema/restriction tests; durable M2 assessment/revision handling still required |
| [Linux isolation](../evidence/linux-profile.md) | Docker 29.7.2 / LinuxKit 6.12.76 ARM64 adversarial controls and process-tree stop; distinct interrupted-supervisor capture remains blocked with cleanup verified | Reuse cases only, never Docker-free qualification |
| [Boundary](../evidence/inference-boundary.md), [router authority](../evidence/router-authority.md), [extension](../evidence/router-authority-extension.md), [bridge](../evidence/router-boundary-bridge.md) | Real local/synthetic failure evidence; stock pending counter expires without completion and writer mutates during pending work. Four retained extension review packets expose control/metadata/stream defects; bridge has 34 cases and retained failures | Keep one router-side authority; dashboard counts and HTTP 200 are not receipts; upgrade cost is real |
| [Native evaluation](../evidence/native-evaluation.md) and review-fix packet | Real pinned router/SQLite with synthetic originals; physical debits, closed restart, release-time fencing, immutable acknowledgements and failed 512 MiB run | Preserve strict default versus explicitly authorized native local limits; remote uncertainty never releases reservation |
| [Continuity](../evidence/router-continuity.md), [combined suite](../evidence/router-initial-suite.md) | Synthetic A/B selection by stock router; six-send normal suite, unchanged shared scope, failures retained | Not two distinct billing subscriptions, natural exhaustion or live readiness |
| [First live attempt](../evidence/router-initial-live.md), [raw record](../evidence/router-initial-live-run.json) | `initial_20260915` failed after **one physical send**; `unsupported_provider_response`, HTTP-success gate passed but SSE MIME/body gate failed; exact status/media/body unavailable. Logged 502 is local. No acknowledged candidate; planner/continuity not started; original unknown and non-quiescent | Preserve spent count 1, one-use marker/state/evidence and reservations. No retry, new scope, baseline or inferred authentication diagnosis |
| [Response diagnostics](../evidence/router-response-diagnostics.md) | Thirteen synthetic original-response cases retain bounded status/media/body-presence and restart evidence without changing qualification | Diagnosability fixed; historical unknown response cannot be reconstructed or reconciled by this change |
| [Protocol](../../schemas/execution/README.md), [fixture daemon](../../cmd/gafferd/README.md) | Pure contract traces; real WAL/FULL SQLite commit/rollback/SIGKILL/SQLITE_FULL/read API tests described with limits | Reversible starting material, not lease safety, artifact custody, auth, power-loss or M1 acceptance |

Every linked family retains its raw JSON, source pins, constructed cases and failed
development/review observations. Counts above describe those packets, not checks
rerun for this documentation decision; do not sum overlapping suites into a new
sample size. Selected router [deployment](../contracts/9router-deployment-2026-09-14.json),
[route catalog](../contracts/task-route-catalog-2026-09-14.json),
[native limits](../contracts/native-subscription-limits.md) and
[migration](../contracts/9router-migration.md) remain separate contracts/observations.

## Bounded M1 ownership and path plan

Paths select owners, not empty packages to scaffold. Create a package only when its
assigned issue has executable behavior. No broker, general workflow DSL, plugin ABI,
second harness, semantic planner, fleet, publication or browser auth in M1.

| Issues | Selected owner/path | Boundary and acceptance still required |
| --- | --- | --- |
| #10 | `docs/contracts/execution.md`, `schemas/execution/`, `tests/fixtures/protocol/` | Reconcile provisional messages/states/limits with actual lease clocks, generations, grants and custody; fresh contract review |
| #11 | `cmd/gafferd/`, `internal/store/`, `schemas/readapi/`, `internal/httpapi/` | Promote only after #10; one schema owner, durable task/attempt/event/outbox transactions. Keep migrations in store until separate files are useful |
| #12/#15 | `internal/authority/`, `internal/workflow/`, `internal/scheduler/`; outbox SQL in `internal/store/` | Immutable grants/revisions, #71 deterministic eligible route decision and atomic reservation/assignment; no new account ledger or duplicate outbox store |
| #13/#14 | `internal/identity/`, `internal/repositories/`, `internal/httpapi/` | Disabled enrollment, revocable identities and runner-local repository policy; no unauthenticated product use |
| #16/#17/#19 | `cmd/gaffer-runner/`, `internal/runner/`, `internal/runnerjournal/`, `internal/control/` | One adapter, one qualified profile; persisted deduplication, issuance/watchdog bounds, output spool, durable desired stop and separate observed termination |
| #18/#20/#21 | `internal/artifacts/`, `internal/reconcile/`, `internal/verification/`, `internal/review/` | Complete blobs/manifests synced before ack; bounded retry only after reconciliation; verification and local acceptance bind exact candidate, never publication |
| #22/#23/#24 | `cmd/gaffer/`, shared `schemas/readapi/` and `internal/httpapi/`, `internal/backup/`, `tests/system/` | Typed local CLI; consistent state/artifact/context snapshot, paused restore/new generation; actual fault and sequential-task gate |

Existing TypeScript experiments remain reproducible reference implementations.
Do not import their fixture launchers into production or mechanically translate
all their code. Port only consumed semantics after #10, use shared protocol fixtures
for Go/TypeScript equivalence, and re-run real failure cases. The router-side
extension stays beside 9Router rather than becoming Go account-routing code.

M2 keeps `web/` for React/TypeScript/StyleX, Go domain owners for capture/intent/
planner/integration/review, and **`internal/httpapi/`**, not a parallel
`internal/http/` or `internal/api/` tree. #26 sessions, #27 router status, #32 events
and #34 review handlers join that API owner. #35/#36 alone introduce the trusted
publication executor in `internal/delivery/`; workers never receive its credentials.
The [implementation index](../implementation-plan.md#foundation-path-reconciliation)
records changed issue navigation hints without changing dependencies or outcomes.

### Provisional slice reconciliation

- **#93 — retain as provisional, reconcile before port/promotion.** Keep Go/TypeScript
  types and public cross-language fixtures at their current paths. #10 must review
  every message/refusal/state/size limit, measured lease assumptions and manifest
  format. Change the version for incompatible semantics; never silently promote
  `execution-provisional-v1` to execution authority. No promise of wire compatibility.
- **#94 — retain bounded storage/API mechanics, not product state.** Keep driver,
  notices, private directory/lock/transaction and adverse SQLite/read API tests.
  Reconcile schema/migrations under #11 after #10; extend or replace rows as required.
  Discard fixture seeding/fresh-store-only startup from the future product entry;
  there is no real user state to migrate and no import path from fixture stores.
  Do not enable execution behind a hidden flag or equate event durability with blobs.
- **#95 — tracked-pending: in-flight, pending merge.** Retain approved UI direction;
  no shell exists on the cutoff base. After its separate slice merges, inspect actual `web/`
  assets, StyleX tokens, same-origin embedding, decoders and visual/React Doctor
  evidence before choosing what to retain. Port only components matching the
  reconciled read API; discard fixture-only assumptions before authenticated #26.
  No login/stop/acceptance claim follows from a rendered fixture shell.

If reuse later wins, port only contracts/fixtures worth keeping and discard the
Go scaffold and shell as needed. No original downstream acceptance is unlocked by
any provisional slice or by this unaccepted decision record.

## Effort record and estimates

**Observed M0 effort is incomplete.** Git history at the cutoff spans
14 September 2026 10:52:52 UTC through 15 September 17:04:29 UTC: **30 h 11 m 37 s
of calendar span**, not focused engineering time. It contains 22 first-parent
commits (including planning and provisional changes), 108 non-merge commits across
merged history, repeated independent-review corrections, one failed live send and
zero real baseline tasks. Reproduce the activity record with
`git log --first-parent --reverse --format='%cI %H %s' 27aa5b367e4ae6a017931e6882fd58598a9efab6`
and `git rev-list --no-merges --count 27aa5b367e4ae6a017931e6882fd58598a9efab6`.
Commit counts include parallel work and are not a productivity measure.

No reliable active maintainer/agent person-hours, model cost or operator timers
were collected for the whole M0. They remain **unknown**, not zero or the roadmap's
five-day timebox. The initial live scope-to-relay-exit interval is 7.038 seconds;
that is not operator effort, successful throughput or provider completion latency.
The substantive measured cost signal is rework: four separate router-extension
review failures, packaging/closure races, a worker OOM, planner deadline/clarification
corrections and absent live diagnostics. A “thin HTTP wrapper” estimate is disproved
by these failure classes, not by an invented hours figure.

Planning ranges below are **estimates**, in focused solo-maintainer engineering
days including implementation, offline tests and independent-review fixes. They are
not extrapolated agent throughput, commitments or evidence that build beats reuse.
No credit is subtracted for provisional code. Re-estimate after #10 and the first
qualified adapter fault run; record actual focused effort from then on.

| Remaining delivery group | Estimate | Evidence/risk basis |
| --- | --- | --- |
| M1 #10–#15: contract, store, authority, identity/repository, atomic dispatch | 8–14 days | SQL/protocol fixtures reduce discovery risk, not production authority/auth work |
| M1 #16–#20: real runner, streams, artifacts, stop/reconciliation | 10–18 days | Native staging/stop/receipt failures and unmeasured Docker-free integration dominate |
| M1 #21–#24: verification, CLI, consistent restore and gate | 7–13 days | No real artifact crash/restore/partition acceptance yet; failure-path review cannot be skipped |
| **M1 total after entry gates** | **25–45 days** | One repository, one runner/adapter/profile, sequential tasks; no UI/publication |
| M2 #25–#32: sessions, phone capture, route status/assessment, approval/progress | 10–18 days | Planner restriction mechanics exist; real model suitability, sessions and phone behavior unmeasured |
| M2 #33–#39: serial integration, review/publication, explicit memory, metrics/digest | 8–14 days | Exact external-effect reconciliation and auth are new, not supplied by fixture UI |
| M2 #40: held-out/paired evaluation, corrections and evidence | 5–10 days | Zero real baseline cases; label and follow-up work cannot be synthesized |
| **M2 total after M1** | **23–42 days, plus at least seven consecutive calendar days of dogfood** | Study waiting is separate; no promise that fixes or evaluation finish inside seven days |

These totals **exclude unresolved M0 elapsed waits and unknown remediation**:
#89 host/profile proof and Docker-free gateway packaging; reconciliation of the
unknown original plus any separately authorized/reviewed live-trial retry; live
worker/planner/two-subscription conformance; #1 executable real-case registration,
output/held-out operator labels and measured minutes; candidate paired workflow
runs; fresh Opus decision review. No bounded calendar release date follows while
those inputs remain unavailable. A new foundation or failed confinement result
invalidates the estimates rather than silently enlarging M1.

## Acceptance and remaining gates

| #9 criterion | Coverage now | Unresolved blocker |
| --- | --- | --- |
| Two maintained pinned foundations, licence/dependencies, gaps/cost | Source-backed same-rubric comparison and explicit build/extend tradeoff above | Paired executed workflow and measured integration/operator cost absent; full dependency audit before import/distribution |
| Worker/planner/containment/continuity evidence, honest unknowns | Cited with raw packets, failed live attempt and historical/source distinctions | Live #6/#7/#8 and real #1 outcomes still not established |
| Explicit choice, first harness/runtime, bounded M1 paths | Build choice, exact adapter target and package owners recorded | No first supported execution runtime until newly measured #89 plus real adapter/verifier integration |
| #71 route assessment, overrides, fresh review without account router | Same-workflow requirement and M1/M2 owners recorded | Candidate end-to-end conformance unknown; alpha selector still implementation/evaluation work |
| Docker-free entire operating path | Each candidate's daemon, worker/verifier, router and recovery assessed separately | No measured passing complete path; Docker optional for tests only |
| Architecture and affected issue path hints | Specification, roadmap, evaluation and implementation index reconciled | Hints are planning only; original dependencies/authority unchanged |
| M0 effort, M1/M2 estimate or blockers | Observed activity and missing timers separated from estimates; blockers named | No claimed measured person-days, live throughput or release date |
| Provisional #93–#95 disposition | Retain/reconcile/port/discard plan per slice; #95 absence disclosed | #10/#11 production promotion and actual #95 review remain later gates |
| Independent decision lock | Fresh independent pipeline review is planned on **GPT-5.6 Sol-review, not Opus** | Fresh **Opus** review remains pending operator-side before lock; pipeline review cannot substitute |

Documentation verification for this change: `npm --prefix docs ci`,
`npm --prefix docs run build`, `npm --prefix docs run check` and `git diff --check`
passed. The generated reader has five documents and 127 valid unique anchors;
63 local Markdown targets in the changed navigation/decision files were checked.
Rendered specification inspection at 1280px and emulated 360px found no document
horizontal overflow; the mobile table retains keyboard-focusable horizontal
scrolling to its last column. Candidate source-link paths matched inspected clones.
No application or historical experiment suites were rerun for this docs-only change.

This document completes the evidence-based decision *record*, not every mandatory
measurement in #9. Keep #9 open, preserve original dependency edges, and keep
unattended execution blocked. The failed live attempt stays failed.
