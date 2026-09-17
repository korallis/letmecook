# Provisional owner workflow

The [typed CLI](../../cmd/gaffer/README.md) and `/api/v1` owner routes compose existing
store APIs. They do not accept #9/#10, qualify a runtime, route provider accounts,
publish changes, or merge. Owner mTLS is mandatory; an enrolled runner credential
cannot invoke owner workflow routes. The API uses closed bounded JSON, version
`workflow-provisional-v1`, immutable UUID intent keys and revision preconditions.

## Normal sequence

1. Bootstrap the owner locally, enroll/enable a runner, register a repository
   profile, and import measured eligibility. Registration/validation are durable
   jobs; neither grants execution authority.
2. Create a task pinned to the repository base, explicit criteria and exact paths.
   Persist `harness` and direct harness-specific `settings` with the brief. The
   canonical `{brief,criteria,paths,operations,harness,settings}` digest is the grant
   brief binding and the runner's execution-input digest.
3. Inspect the server proposal. Approve its exact digest with an explicit grant
   UUID and expected prior grant, then dispatch against that grant/revision.
   Unsupported development isolation requires **both** daemon and owner consent.
4. Poll task/attempt views or durable event sequence cursors. Gateway unavailability
   does not prevent reads, stop intent, or reconciliation. No second valid attempt
   is authorized while the first remains unresolved.
5. After finalized artifact custody, request verification as a job. Trusted checks
   come only from the approved repository profile. Recreate exact content in a
   private checkout, compare every changed/untracked/ignored/deleted file with the
   candidate attempt's retained dispatch envelope, and save verification before job
   success. A later wider approval cannot expand the authority used to verify an
   earlier candidate.
6. Accept or reject the exact verification and selection IDs, retaining coverage,
   evidence and limitations. Acceptance requires verified current evidence. A CLI
   override reason is merely a recorded request, never a verification bypass.

The default verification profile is `unqualified`: it saves refusal evidence and
executes nothing. `macos-sandbox-exec-dev` is development-only, **supported: false**;
its verifier requires measured launcher digests, controls, confinement canaries
and explicit limitations. Construct it with
`verification.NewDevelopmentProfile(profile, timeout, stateDir)`. The explicit
private state directory owns `verification-canary`; each probe is cleaned up.
There is no HOME fallback or write into the real HOME outside configured state.
Canary roots inside verification workspaces or system-temp exceptions (`/tmp`,
`/private/tmp`, `/var/folders`, `/private/var/folders`) are refused. Measured
launcher limitations are retained in each report, including the current
allow-default profile's open mach/keychain IPC and unproven escaped-session
termination. Escape/drift is retained as unqualified refusal evidence.
No evidence here establishes a supported unattended runtime.

Task create has a 65,536-byte **total encoded JSON envelope** ceiling, including
brief, criteria, scope, settings, IDs and escaping; normalized input must also fit.
This is not a promise that a 65,536-byte brief fits. Repository/eligibility request
bodies share that total ceiling, and durable repository job payloads include an
additional internal wrapper within their own 65,536-byte bound. CLI creation and
flow require explicit `--harness fake|opencode`; there is no implicit harness.

Task views and `/tasks/{id}/verification` expose only verification `{id,status}`
summaries, with bounded diagnostic reasons. Full reports (up to 8 MiB) come from
`/verifications/{id}`; ordinary JSON replies stay bounded at 1 MiB. Reports are
immutable history; current status also depends on selection, generation and custody.

## Retries, jobs and control

Reuse the same message UUID and body after a lost response. A stored response is
returned byte-for-byte with `Idempotent-Replay: true`; conflicting intent under the
same UUID returns 409. Repository/eligibility revisions, expected grant heads,
dispatch grant revisions, and review selection IDs remain enforced. Receipts are
written after domain commits, not before them. Jobs keep their original input for
replay; interrupted running jobs fail `daemon_restart` on startup.

Stop/cancel receipts mean the intent is durable, not that termination was observed.
Read the stop view and reconciliation report for process and remote-work evidence.
Pause blocks admission but does not kill processes. `POST /api/v1/tasks/{id}/resume`
(`gaffer task resume UUID`, mutation key `task.resume`) appends clearing markers
for that task's pause latches only. `daemon resume` clears global stop latches in
the same transaction that unpauses. Markers use the reconciliation contract
`reconcile_reports.id = "latch-cleared:" + stop_id`; stop history stays immutable.
The domain receipt freezes the cleared list across retries, including a lost HTTP
receipt followed by a newer stop. Neither resume clears `cancel_attempt`, proves
termination, releases a reservation, or grants execution authority. Cancel clearance
belongs to reconciliation. The store/reconciler suppression readers must honor
these markers before post-resume admission is available (integration-owned).
Resume after restore requires
`--confirm-source-fenced`; generation changes alone stop nothing. Retry delegates
to the reconciler and remains grant-, budget-, release- and stop-gated.

Events use durable sequence numbers, bounded pages and `poll_after_ms:500`.
An expired retained cursor returns 410 `cursor_expired`. Streams and blob downloads
use separate bounded routes; raw blob downloads are not JSON.

## Composition and present limits

Compose `httpapi.WorkflowJobHandlers(deps, VerificationOptions{StateDir, Profile})`,
add a backup handler calling the configured backup service, then construct one
`jobs.NewWorker` before opening listeners. Close it before the store. Backup stays
S6-owned. Do not start multiple workers against one store.

This lane deliberately leaves `cmd/gafferd/main.go`, `store/grants.go` and
`store/dispatch.go` to integration. Their required hooks are a transaction-level
`workflowOwner` check, `workflowDecisionTx` before grant commit, and a paused-state
check inside dispatch admission. The route layer's immediate reauthentication
narrows but does **not** close the revocation window without that store patch.
Run `go test -tags ownercommit ./internal/store -run TestOwnerCommit` after applying
it; the tagged tests intentionally fail on the standalone seams base.

The real-daemon smoke test reaches **assigned only**. Full execution, custody,
finalization, retry, restore and accepted-flow verification require the other M1
lanes; no unrun end-to-end claim follows from the CLI's existence.
