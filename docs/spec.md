# Gaffer — Technical Specification

**Status:** proposed baseline v0.4 · **Updated:** 15 September 2026

Companions: [product requirements](PRD.md), [evaluation](evaluation.md),
[delivery sequence](roadmap.md). This is a design to implement and test, not a
statement that the security or durability properties already exist. The
[foundation decision record](https://github.com/korallis/letmecook/blob/main/docs/decisions/0001-execution-foundation.md)
selects a narrow Go build from the available evidence, pending measured comparison,
Docker-free confinement and fresh Opus review. It does not complete #9 or unlock
original downstream work.

## 1. Architecture and ownership

Use one active daemon with SQLite on local storage, a responsive web application,
and independently supervised runners. The daemon owns workflow truth; each runner
owns enough durable execution state to survive a disconnect. Runners are replaceable
only after local work has been acknowledged or explicitly abandoned.

9Router is the single shared model-access control plane. It owns provider
connections, multiple subscriptions per provider, credential refresh, account
selection, model combinations and request-level fallback. Gaffer owns tasks,
authority, placement and recovery; it does not implement those router functions.

```text
Private HTTPS UI / CLI
        |
        v
Daemon: capture, brief/plan revisions, authority, scheduler, review
        |                  |                     |
        v                  v                     v
 SQLite + outbox      Git context          Artifact store
        |
        | authenticated runner-initiated TLS connection
        v
Runner supervisor + durable attempt journal + lease watchdog
        |
        +-- planner role: immutable inputs → structured proposal
        +-- discovery role: read-only repository inspection
        +-- worker role: isolated checkout → artifact + evidence
        +-- verifier role: isolated candidate checkout → checks
        +-- delivery role: approved artifact → scoped GitHub action

Every model call from planner / reviewer / harness
        → authenticated route boundary → 9Router
        → named combo → provider A: subscription 1 / subscription 2 / ...
                     → permitted fallback models/providers
```

Roles are permission boundaries, not necessarily separate binaries. One machine
can host several roles, but project-controlled processes cannot read supervisor
state, change placement policy or acquire delivery credentials.

**Source of truth:** SQLite for workflow/authority, Git for reviewed knowledge,
content-addressed files for artifacts, runner journal for unacknowledged local work,
and GitHub for remote branch, check, PR and merge state. Indexes and summaries are
derived. No model transcript is the authoritative task state.

9Router is authoritative for provider connection configuration, selection,
cooldowns and observed account usage. Gaffer stores route references and task-linked
observations, not a second provider credential database or account router.

**Network boundary:** browsers and runners need a reachable authenticated daemon
endpoint. Runners initiate connections; no public inbound endpoint is needed for
the baseline. This does not eliminate incoming traffic on the private interface,
nor outbound traffic from 9Router to model providers and from delivery to GitHub.

### 1.1 Initial deployment

One operator, one active daemon, one repository, one runner, one harness, one shared
9Router instance and one code-writing attempt at a time. The alpha acceptance
fixture uses two subscriptions from the same provider to prove central account
management; the design also permits a single-connection installation, not yet
validated. The decision target is Go 1.26.5 with SQLite, an embedded
React/TypeScript/StyleX UI and the pinned OpenCode 1.18.30 adapter first.
No supported worker/verifier runtime is established yet. Linux is the first intended unattended
execution target; an exact Docker-free OS/native or dedicated VM confinement
profile must be proven before it is supported. A Mac may host the daemon only
within the validated support matrix. Worker execution from that installation
requires an explicitly selected, proven Docker-free local or remote profile;
native macOS and Windows execution each require their own evidence.

The released app must install and complete every supported product execution
path without requiring Docker, including workers and verification. Docker is
optional for development and testing; normal product execution does not become
a test because it invokes a worker. Git, the harness, language/build tools,
9Router and a proven Docker-free isolation runtime may still be prerequisites.
Record exact versions and capabilities; an unavailable or unsupported profile
blocks execution. No automatic work is placed on client/production hosts.

### 1.2 Operator-selected deployment

Gaffer is intended to be downloadable and self-hosted on the operator's own
infrastructure. There is no mandatory maintainer host, hostname, IP address,
Tailscale network, router instance or provider account. An environment used to
collect development evidence is a test fixture, not an application dependency.
Exact tested versions belong in the compatibility/evidence matrix; capability
requirements and explicitly selected isolation profiles govern supported execution.
The complete supported topology must have a Docker-free setup and operating path
for the daemon, runner supervisor, scoped inference boundary, 9Router and any
required execution VM. This includes fresh setup, update and recovery. An operator
may reuse a compatible Docker-hosted router, but Docker cannot be a prerequisite
for supported use. Matilda remains an optional configured machine, with no
mandatory hostname, Tailscale endpoint or maintainer access.

- **Daemon:** choose its host, local state/artifact directories and authenticated
  private endpoint. One active daemon owns authoritative state.
- **Runner:** enroll a host and configure repository identities/roots, allowed
  projects, runtime profile and enablement. It starts disabled and executes only
  within both project grants and runner-local policy.
- **9Router:** supply an existing or fresh router origin, deployment identity,
  protected credential references and named routes. It owns provider credentials,
  subscriptions, account selection and fallback.
- **Private transport:** supply reachable authenticated endpoints and the supported
  TLS/private-access profile. Tailscale is optional; another verified VPN, private
  network or authenticated tunnel may satisfy the contract.

A single-host installation may colocate daemon, runner supervisor and router while
keeping their state, credentials and process permissions separate. Repository code
still runs in the supported containment profile. A split-host installation supplies
the corresponding endpoints explicitly; runner connections are outbound to the
daemon, and inference travels through the scoped route boundary. Being reachable
does not make a host eligible. Losing a selected host blocks or reconciles work
under its existing authority; it never silently selects an unapproved machine.

Setup must offer **connect existing 9Router** and **configure a fresh 9Router**
paths, with compatibility/authentication checks and actionable diagnostics. An
existing router does not need new provider logins merely because Gaffer connects
to it. Configure named worker/planner/reviewer routes in that router and validate
their actual capabilities and permitted provider/billing envelope. Credentials are
supplied through protected local references, never command-line flags, checked-in
examples or shipped defaults. Unknown compatibility is reported before execution.

Reusing an existing instance and moving it are different operations. Optional
operator-directed migration uses the selected router version's verified
backup/export/restore procedure. Check whether its configuration, encryption keys
and provider sessions are portable; do not promise that every provider session
can move without reauthentication. Transfer only between operator-approved router
hosts over an authenticated encrypted channel with restrictive storage permissions.
Protect the source and recovery copy, validate the destination's identity, routes,
authentication and policy before cutover, and reconcile/drain active requests before
changing Gaffer's endpoint or route grants. Preserve a tested rollback path.

Router backups remain separate from Gaffer's workflow/artifact backups. Gaffer
must never collect provider secrets into its database, repositories, worker
sandboxes, logs, browser downloads or release packages. Shipped examples use
placeholders. Clean-machine install tests must work with independently supplied
hosts and fresh credentials, without access to a maintainer's private resources.
These are implementation requirements, not claims of an existing installer or
already-tested router migration.

## 2. Authority and intent

### 2.1 Capture and bounded discovery

Persist a capture before analysing it. The client supplies an idempotency key;
retransmission returns the same capture. Retain original wording as provenance.
Models and workers can see the raw capture alongside the approved brief, clearly
labelled so an obsolete suggestion cannot override the agreement.

Project setup can grant a bounded discovery allowance for an exact repository and
approved provider route. Discovery reads files and repository metadata only. It
cannot run dependency installation, build scripts, tests, Git hooks or arbitrary
repository tools. If the chosen harness cannot enforce read-only discovery without
such execution, use a trusted reader to provide excerpts or require a different
profile. New/ambiguous repository routing requires resolution first.

Intent refinement produces a schema-validated proposal:

```text
brief_revision:
  capture_id, project_id, repository_id, revision, content_hash
  outcome
  constraints[]
  exclusions[]
  criteria[{id, statement, evidence_required}]
  allowed_paths[], forbidden_paths[], allowed_systems[]
  assumptions[{id, statement, source_refs[], status}]
  unresolved_questions[]
```

Criterion IDs persist across text edits; removing/replacing a criterion creates an
explicit revision. Ask up to three questions by default, then stop affected work,
propose a clearly narrower scope or continue clarification. Never fabricate an
answer to keep the question count low.

### 2.2 Execution authority

A grant records actor, repository identity, brief/plan hashes, permitted task kinds,
path/system scope, runner/provider allowlists, resource ceilings, start/expiry,
policy revision and revocation state. Record the approved base commit and the
policy for advancing it. The baseline requires review when the relevant base
changes; standing authority may permit rebase/reverification within stated scope.

A plan approval permits local execution within that envelope. It does not implicitly
permit push, PR creation, commenting, merge, deployment, purchasing or expanding
scope. The UI may offer a combined “approve work and open a PR when verified” action
whose effects are explicit and durably recorded.

Publication approval binds action, repository/remote, target branch, exact candidate
commit or digest, current policy and actor. If standing publication policy is in
force, evaluate it against the new candidate and record that decision. A changed
candidate cannot reuse a prior artifact-specific approval.

The daemon checks authority when enqueuing and dispatching. The runner rechecks its
local policy when accepting and starting an attempt. Delivery rechecks immediately
before an external mutation. The model can propose a grant, but cannot create one.

### 2.3 Changing intent or stopping

A material brief/plan change supersedes affected grants. Hold dependent tasks,
revoke affected active leases, preserve their work, and reconcile termination before
starting replacements. Unaffected work inside current authority may continue.

- **Pause:** prevent new dispatch; existing authorised runs finish unless cancelled.
- **Cancel task:** revoke that task's active lease and request process-tree termination.
- **Global stop:** transactionally latch stop, revoke execution/delivery leases and suppress dispatch; send stop messages to connected runners.
- **Resume:** an operator clears the latch; the scheduler reconciles artifacts and current authority before assigning work.

The server acknowledges the durable stop latch immediately. Connected runner
termination targets five seconds. A disconnected runner remains “unconfirmed”; its
watchdog kills the process tree on local lease expiry. Already accepted provider or
forge operations can still complete and must be reconciled. Global stop cannot
promise instantaneous reversal of external effects.

## 3. Durable execution model

### 3.1 Task versus attempt

A **task** describes the desired result and criterion mapping. An **attempt** is
one concrete execution with immutable input, identity and route. Retries create
new attempts. Reconnects and retransmission do not.

```text
Task:
 draft → ready → active → verifying → awaiting_review → accepted
                    |         |              |
                    v         v              v
                reconciling / blocked / failed / cancelled

Delivery (separate):
 not_requested → authorised → publishing → published → merged/closed
                                  |
                                  v
                           outcome_unknown
```

Attempt states include assigned, starting, running, stopping, result_pending,
succeeded, failed, cancelled, expired and unknown. State transitions use expected
row version and transactional validation. Accepted means local result acceptance;
merged requires an observed GitHub merge for the corresponding PR/head.

### 3.2 Assignment, leases and fencing

1. In one SQLite transaction, select a ready task, reserve capacity/budget, create
   an attempt with a monotonically increasing task epoch, and add an assignment to
   the transactional outbox. Commit before sending.
2. The runner validates authenticated assignment identity and execution generation, repository mapping,
   current local policy, route availability and isolation support. It durably
   journals acceptance before acknowledging. Duplicate assignment IDs return the
   existing attempt status rather than launching again.
3. Starting the process consumes a current execution lease. The runner supervisor
   renews it while connected and authorised. The watchdog lives outside project
   processes and uses a monotonic deadline; reboot invalidates old local leases.
4. Heartbeat loss marks availability unknown and stops new placement there. It does
   not by itself authorise concurrent retry. Reconcile the prior attempt and wait
   for acknowledged termination or lease expiry plus the documented safety margin.
5. Accept results, commands and publication requests only for the current daemon
   execution generation, attempt and epoch. Late output is retained as stale evidence, never silently selected.
6. If termination/isolation cannot be trusted, quarantine the runner and require
   reconciliation; do not use a timeout as proof that an uncontrolled process died.

The provisional [execution contract §4](https://github.com/korallis/letmecook/blob/main/docs/contracts/execution.md#4-initial-lease-renewal-and-replacement-barrier)
owns the initial handshake, request-send monotonic cutoffs, renewal/restart barriers
and timing examples. Publish measured clock, scheduling and stop bounds for the
selected runtime; examples are not qualification evidence. Unknown bounds quarantine
instead of permitting redispatch.

At-least-once message delivery is expected. Exactly-once arbitrary shell side
effects are not promised. Isolate retriable local work and mediate external effects
through a separate idempotent/reconciled delivery path.

### 3.3 Result preservation

The runner retains a journal containing assignment/epoch, process identity,
worktree, base commit, harness session reference, stream offsets, checkpoint records
and artifact upload status. Flush critical records before acknowledgment.

On completion, collect the tracked diff, explicitly included untracked files,
binary changes, exit status, summary and verification outputs. Detect submodules,
Git LFS and unsupported artifact shapes; block or preserve them through a documented
format instead of silently dropping content. Exclude credentials and transient
build output. Retain uncommitted recovery work in a bounded quarantine if it cannot
be safely included in the candidate.

Transfer artifacts with size limits, hashes and resumable/chunked upload. The daemon
writes to a temporary location, verifies the hash, atomically promotes and syncs it,
then commits its manifest/reference and result event before acknowledging. Orphaned
blobs are recoverable/collectable; a committed reference never intentionally points
to an undurable blob. The runner deletes local recovery state only after durable
acknowledgment and retention policy permit it.

Native session resume is optional and valid only with the recorded session,
compatible harness and intact environment. Otherwise start a new attempt from
verified artifacts plus a progress summary. A transcript cannot reproduce hidden
process state or undo side effects.

### 3.4 Recovery and retry classes

| Event | Required behaviour |
| --- | --- |
| Daemon crash before assignment acknowledgment | Replay outbox with the same attempt ID; runner deduplicates |
| Runner partition | Mark reconciling; block duplicate execution until fenced/terminated under the lease protocol |
| Lost result acknowledgment | Resend the same manifest; content hash and result identity deduplicate |
| Harness hang | Deadline then process-tree termination; bounded retry only after reconciliation |
| Provider connection auth/limit failure | 9Router handles connection cooldown/selection; continue the same attempt if it returns a successful response |
| Entire route unavailable | Consume router status, preserve partial work and apply bounded task-level backoff |
| 9Router unavailable or router credential rejected | Park model-dependent work and report the connection issue; never silently bypass 9Router |
| Invalid structured result | Keep raw output; at most the configured repair attempts, then block |
| Unknown push/PR outcome | Query GitHub by approved branch/head/action identity before reissuing |
| Dirty/corrupt checkout | Quarantine and preserve recoverable work before recreating; never automatically discard the only copy |
| Disk full | Stop new work, preserve state, show storage block; no hot retry loop |
| Stop/revocation | Cancel leases and reject stale results for acceptance/publication |
| Runner disk destroyed | Recover acknowledged artifacts; explicitly report loss of untransferred local work |

A retry must keep the authorised objective. “Reduce scope” is a new proposal when
it changes the acceptance criteria; it is not an automatic recovery trick.

## 4. Runner placement and isolation

### 4.1 Eligibility

Runner-local policy is authoritative for what that host permits. Record enabled
state, allowed repository identities and roots, reserved project IDs, capabilities,
route references, permitted egress, isolation profiles and policy revision.

The scheduler chooses from the intersection of:

1. Project's explicit runner list and current grant.
2. Runner-local repository/project permissions.
3. Required OS, tool, isolation and environment capabilities.
4. Harness compatibility with an approved 9Router route, and authenticated access to its inference boundary from that runner.
5. Available capacity, resource budget and non-expired lease eligibility.

An empty intersection blocks with the limiting facts. “Default runner” is only a
setup convenience that populates a project allowlist; changing it never redirects
existing projects automatically. A runner joins disabled. A reservation binds an
allowed project to its canonical repository/remote and local root, so relabelling
an unrelated repository with a permitted project ID is insufficient.

Assignments include policy revision. Drift or local refusal blocks that pairing
until reconciled; it is not retried indefinitely. A local policy tightening stops
affected active work. The daemon may request tighter policy but cannot weaken
runner-local restrictions through model tools.

### 4.2 Execution boundary

Git worktrees provide checkout convenience; they share Git administration and are
not a security boundary. Use a dedicated clone per isolated workspace/trust domain,
with no writable shared Git metadata exposed across projects. Worktrees inside one
trusted isolated environment are permitted only with a documented shared-metadata
boundary. [Git worktree documentation](https://git-scm.com/docs/git-worktree)

For supported unattended execution require a proven Docker-free OS/native or
dedicated VM confinement profile:
non-root execution, bounded CPU/memory/disk/processes, writable workspace only,
read-only or copied dependencies where practical, controlled egress and a supervisor
outside the workspace. Do not mount the host home directory, daemon state,
unrestricted caches, Docker socket, SSH agent or general forge credentials.
Check the selected runtime identity and required controls before launch. Missing,
unsupported or drifted controls fail closed without a host-shell, worktree-only
or Docker-required fallback. Unknown termination quarantines the profile until
reconciled. Docker-based development tests remain valid for their measured scope;
they cannot establish Docker-free product execution. The historical #5 profile,
[`m0-linuxkit-arm64-docker29-uds-v1`](https://github.com/korallis/letmecook/blob/main/docs/evidence/linux-profile.md), a Docker-based
synthetic experiment that does not prove native Linux, macOS, Windows or another VM.

Path allowlists and diff validation detect scope breaches. They do not replace OS
containment. Shell denylists are not a containment mechanism for arbitrary scripts.
Repository hooks, build systems, dependency installation, skills and tool plugins
are executable untrusted input and run inside the same boundary.

Provider credentials remain in 9Router's separate trust boundary. The harness uses
router access rather than provider account credentials. Keep 9Router's database,
admin credentials and unrestricted inference key outside project code. Where the
selected 9Router build lacks scoped inference keys, a thin trusted forwarding
boundary binds the caller to its authorised route and enforces request/resource
limits. It never chooses accounts, refreshes provider tokens or retries provider
requests; those remain 9Router responsibilities. Deny direct network access to the
unrestricted router/admin endpoint so a worker cannot bypass the boundary. Router
access still permits consuming model capacity; test that exposure and bound it.

M0 must test actual auth/config/sandbox behaviour for each supported profile.
A compromised runner can expose the data and credentials it can access. A compromised
daemon can misuse its existing grants; local restrictions reduce this blast radius
but do not make an allowed malicious assignment harmless.

### 4.3 Untrusted content and data policy

Issues, repository text, retrieved web content, skills, transcripts and context
proposals cannot change authority, run triggers or choose new providers. Delimit
and label them when supplied to models; validate every requested action against
server policy. Treat model-produced HTML/Markdown and terminal output as untrusted
when rendering. Sanitize HTML, disable unsafe URLs/control sequences, and limit
content sizes.

A project's data policy names approved repositories, providers, transports and
retention expectations. Failover cannot send code to an unapproved provider even
when that provider is cheaper or available. Logs may contain code/secrets despite
redaction; protect storage and provide controlled export/deletion.

## 5. Planning and harness interfaces

### 5.1 Coordinator as a restricted role

The durable scheduler is deterministic code. The coordinator is an event-driven
model invocation that proposes a plan, summarises evidence or requests clarification.
Do not keep one model generating indefinitely per project.

The planner calls a named 9Router route through the compatible API using an immutable
evidence packet, schema-validated output and explicitly registered typed tools.
The daemon validates proposals and performs authorised actions; the model has no
general shell-backed access to daemon state. Coding workers use harness adapters,
but their inference goes through the same router. A separate harness process is not
required merely to make a planning call.

Define role routes such as `gaffer-planner`, `gaffer-worker` and `gaffer-reviewer`
(illustrative user-configured combo names). These are starting profiles, not a
complete selection algorithm. The alpha coordinator assesses each task or scoped
step using the approved brief, authorised evidence, complexity/uncertainty,
modality, tools, context and budget requirements. Deterministic eligibility filters
the operator-approved route set before task-fit ranking. Show the proposed choice,
reason, unknowns and eligible alternatives; an operator can override within the
same authority envelope. See the [task-routing contract](https://github.com/korallis/letmecook/blob/main/docs/contracts/task-routing.md)
for assessment bounds, evidence, immutable decisions and evaluation.

Routes can use different models while
sharing the same underlying provider connections. All intent, planning, coding,
model-assisted review and later curation usage is attributed to tasks/projects.
Embeddings are absent initially; any future model integration must use an explicitly
supported route or a separately documented local embedding implementation.

Future typed model tools can include plan.propose, task.inspect, context.query,
context.propose and human.ask. A schedule or authority change is a proposal awaiting
operator/policy approval. The CLI calls the same typed API for humans or workers
with restricted tokens; the coordinator does not need a general shell to use it.

Keep a compact decision log linked to full evidence. Summaries must not hide failed
checks, uncertainty or divergent results to make the transcript look clean.

### 5.2 Versioned adapter contract

Use built-in adapters first. Do not parse human terminal screens where the harness
provides structured output. The abstract contract remains small:

```go
type Harness interface {
    Describe(ctx context.Context) (Descriptor, error)
    Probe(ctx context.Context, req ProbeRequest) (ProbeResult, error)
    Start(ctx context.Context, req RunRequest) (RunHandle, error)
    Events(ctx context.Context, h RunHandle, after string) (EventStream, error)
    Cancel(ctx context.Context, h RunHandle) (CancelResult, error)
}
```

The descriptor declares supported harness versions, 9Router protocol/endpoint modes, structured-output,
approval, resume, sandbox, model-settings and usage capabilities. RunRequest contains
attempt ID/epoch, input/base/context hashes, role, workspace, route reference,
policy, adapter-specific model settings and limits. Never invent a universal effort
enum; reject unsupported settings. Probe has a read-only configuration phase and an
explicit bounded execution phase.

Normalize events as started, activity, tool_requested, approval_required, usage,
checkpoint, completed and failed, retaining raw versioned events. Persist actual
harness/model/config versions and session IDs. An unsupported permission prompt
must block safely; do not bypass it to maintain unattended operation.

| Initial candidate | Documented interface | Gaffer-specific validation still required |
| --- | --- | --- |
| Codex | Noninteractive `exec`, JSONL events, schema-constrained final output, explicit session resume | Pinned harness→9Router Responses path, structured/tool events, sandbox/approval behaviour, cancellation and usage attribution |
| Claude Code | Noninteractive print mode, JSON/streaming output | Pinned harness→9Router Messages path, ambient hooks/MCP loading, containment, process-tree stop and usage attribution |
| Later harnesses | Select a documented API/CLI, or ACP where actually supported | Same conformance suite; no promise of uniform features |

Sources: [Codex noninteractive mode](https://learn.chatgpt.com/docs/non-interactive-mode),
[Claude programmatic execution](https://code.claude.com/docs/en/headless).
Configure the harness endpoint and router credential explicitly; do not inherit a
direct provider login as an unnoticed fallback. Claude's bare mode changes ambient
configuration/auth behaviour, so verify it with the chosen router endpoint. Record
and test each launch profile instead of copying flags between harnesses.

Built-in Go adapters require a new binary. Later external adapters are separately
installed executables speaking a versioned stdio protocol, with timeouts and pinned
provenance. A source file discovered in a directory does not execute itself. ACP,
App Server, MCP or another protocol is selected by required capability and stability,
not as an ideological rule.

### 5.3 Compact tools and encoding

Canonical API, persistence, tool input and structured model output use validated
JSON-compatible schemas. Summaries list IDs, state and next action; detail is fetched
when needed with byte/token limits and explicit truncation.

Adopt AXI ideas such as small useful responses, actionable errors and lazy detail.
Expose JSON for scripts and a compact agent view as appropriate. Evaluate TOON for
uniform tables; retain code/diffs/prose verbatim and use compact JSON for nested
structures. Provider tool schemas remain in the provider-required format. No claim
that CLI beats MCP on every workload or that encoding savings imply proportional
subscription capacity. See [encoding evidence](evaluation.md#5-engineering-evidence-and-its-limits).

## 6. Git, dependencies and delivery

### 6.1 Reproducible candidate

Every attempt declares repository identity, immutable base SHA, approved brief and
plan hashes, ordered prerequisite artifact digests, context commit and verification
profile. Materialize these exact inputs before work. Record environment/toolchain
versions, commands, exit codes and output hashes with the result.

The trusted checkout step validates remotes, disables unmanaged hooks and resolves
refs. Workers receive a local checkout and may commit locally, but cannot push.
Use branches such as `codex/gaffer/<project>/<task>/<attempt>` by default in this
workspace's GitHub workflow; branch naming is configurable to repository policy.

### 6.2 Dependency composition and parallelism

Initially, use a single writer and integration queue per repository. A task dependent
on another starts from a composed, verified predecessor candidate, not from the
original base with only a textual dependency edge. Gaffer may continue a sequential
approved plan before external merge, but labels downstream results speculative
until predecessor acceptance.

A trusted integration component owns one integration workspace. It applies ordered
prerequisite commits/artifacts, checks base freshness, resolves mechanical conflicts
or returns a scoped resolution task, then delegates required checks on the combined
candidate to the isolated verifier. Trusted integration code never runs repository
commands with supervisor or delivery authority. Rejecting a predecessor invalidates dependent candidates and their
artifact-specific approvals. A changed base yields a new candidate identity.

Beta may allow disjoint code tasks in separate isolated clones. Serialize their
integration and reverify combined behaviour. File disjointness is a scheduling hint,
not proof of semantic independence. Bound speculative work so parallel throughput
does not create an unreviewable backlog.

### 6.3 Verification and acceptance

Verification runs on the candidate actually offered for acceptance. Required checks
come from the approved repository profile and task criteria; workers cannot silently
delete failing checks or rewrite the policy that determines success. Check that the
patch respects protected paths and accepted scope, then run relevant tests/builds
inside containment. Record environmental failures separately from code failures.

An agent's summary, zero exit code or clean patch application is insufficient.
The review package states what was checked, what failed or could not run, the exact
candidate and any remaining uncertainty. Changes from rebases, feedback, per-hunk
edits or dependency updates require renewed verification and acceptance/policy evaluation.

### 6.4 External effects

The trusted delivery executor receives only the approved artifact and action, with
scoped forge credentials. A worker cannot ask it to publish arbitrary refs.

Persist an action record, current execution generation and stable idempotency identity
before push/PR creation. Idempotency identity survives restoration for remote
reconciliation; a new generation does not authorise creating the same effect again.
Use a deterministic branch and recorded expected head; if a ref moved unexpectedly,
block instead of overwriting user work. After an ambiguous timeout, inspect the
remote branch and matching PR before retrying. A PR/action marker aids reconciliation
but does not imply GitHub provides universal exactly-once requests.

GitHub branch protections, required checks and review policy remain authoritative.
Do not push to protected/default branches, force-push unexpected state or bypass
checks. v1 does not auto-merge. The UI records remote merged/closed state by polling,
not by assuming that local acceptance completed delivery. Deployment is outside v1.

## 7. Memory and context

Start with one context repository per code repository, partitioned by project:

```text
context/
  repository/
    architecture.md
    conventions.md
    runbook/testing.md
  projects/<project-id>/
    charter.md
    decisions/
    progress.md
    preferences/                 # later, explicit confirmed entries
  proposals/<proposal-id>.md
```

Repository facts may be shared within the same data/trust scope. Project goals,
assumptions, preferences and client-sensitive findings are not globally retrieved.
User-global preferences require explicit promotion and cannot override current
repository/project instructions. Search indexes and embeddings, if any, live outside
the versioned knowledge tree and are rebuildable.

Entries record stable ID, source task/attempt, source commit, relevant path/symbol,
verification/environment evidence, author, confirmation time, review state and
scope. Confidence alone is not provenance. Always provide current brief/constraints
and a bounded relevant context slice. If mandatory instructions exceed the budget,
surface the conflict; do not silently truncate required constraints.

The alpha write path is human-approved proposals under a single context writer.
A model may suggest contradictions or duplicates but cannot declare a new fact
verified. Commit accepted changes with proposal/attempt IDs, then update the derived
index and SQLite pointer. A crash between Git commit and indexing is repaired by
scanning commit/proposal identities. Detect external dirty edits before writing;
never auto-stash, overwrite or resolve conflicts without preserving them.

Start retrieval with explicit references and SQLite FTS/BM25. Add embeddings only
if held-out retrieval trials show better task outcomes or lower rediscovery effort.
Flag a sourced command failure for review with its cause; network/fixture failures
do not automatically invalidate the command. A pinned note can still be stale.

## 8. 9Router integration and task capacity

### 8.1 Ownership and connection model

9Router is required from the first working slice. One operator-managed instance
serves every harness and Gaffer's own model calls. Its documented multi-account and
combo mechanisms are the foundation, not functions Gaffer needs to recreate.
[9Router](https://github.com/rickicode/9router)

| Concern | Owner | Gaffer's interface |
| --- | --- | --- |
| Provider login, credentials and refresh | 9Router | Router credential reference only; no provider tokens |
| Multiple subscriptions per provider | 9Router connection records | Read-only connection identity/status where exposed |
| Connection priority, rotation and cooldown | 9Router | Consume outcome/availability; never choose an individual token |
| Named model combos and request fallback | 9Router | Select an authorised combo/model route by task role |
| Task dependencies, placement and execution leases | Gaffer | Dispatch a compatible harness on an eligible runner |
| Task/project concurrency, attempts and budget | Gaffer | Reserve work and enforce request limits at the route boundary |
| Workflow/usage visibility | Gaffer consuming 9Router | Task-correlated observations and links to router management |

A **connection** identifies one provider account/subscription in 9Router. A
**route** identifies a model or combo exposed to callers. Multiple connections from
the same provider can serve one route. Distinct subscriptions retain independent
capacity unless the provider imposes a shared limit; duplicate aliases for the same
account never imply additional quota. Do not collapse all subscriptions merely
because they have the same provider name.

Illustrative flow: two Claude subscriptions back the coding route; a Codex route
can back review. If the first coding subscription becomes unavailable, 9Router
selects the second according to its configured strategy. The worker keeps its
current task and router endpoint. It does not need a new Gaffer account profile.

### 8.2 Inference and management contract

Configure router identity, pinned version, private endpoint, credential reference,
authorised model/combos, permitted provider/billing set and a route configuration
fingerprint where the integration can obtain one. A separate read-only status
adapter consumes management data. Gaffer is the work surface; 9Router remains the
single place to configure accounts and fallback. Do not build parallel login or
account-rotation settings into Gaffer.

The inspected fork implements `/v1/chat/completions`, `/v1/messages`,
`/v1/responses` and model discovery including combos. Its dashboard has combo,
provider, availability and usage routes. Treat those management routes as a
version-pinned implementation interface, not an assumed stable public SDK. The
adapter returns a minimal normalized view and excludes provider credential fields.
Never read the router's private database or expose a broad management credential to
agents. Missing signals are explicit unknowns or a small upstream/integration task.

Use field allowlists rather than forwarding/redacting whole responses: the inspected
build can expose nested provider tokens and raw router keys in usage aggregates.
Protect management paths independently of the dashboard login, and distinguish
passive status retrieval from usage endpoints that actively refresh credentials.
The pinned source evidence is in [the integration review](evaluation.md#concrete-integration-boundaries-from-source).

The configured role route is recorded in each attempt. Capture actual provider,
model, connection, usage and fallback events when available, with source/freshness
and request/attempt correlation. Do not invent per-task attribution if the pinned
router cannot supply a correlation signal; record harness totals and unattributed
router observations separately until that integration gap is closed.

9Router connection selection can legitimately change during an attempt. The
approved provider/model/billing envelope must remain valid throughout. If per-key
route scopes or immutable route versions are absent in the chosen build, the
trusted forwarding boundary fixes/validates the requested model and checks allowed
configuration, with direct unrestricted inference access denied to workers. This
boundary enforces Gaffer authority only; routing and account selection stay in
9Router. Changes that widen an authorised route require updated authority.

A preflight configuration fingerprint alone cannot prevent a concurrent combo edit.
Freeze the authorised provider/model/billing definition while affected requests are
active, or drain them before changing it and issuing fresh grants. Enforce that
through the router's configuration-write boundary or an upstream versioning feature;
if the chosen build cannot support it, record an unresolved integration gap for
policies requiring that guarantee. Normal account rotation, credential refresh and
cooldowns continue within the approved definition.

### 8.3 Availability and spending

Use 9Router's connection/route status, cooldown and usage signals first. Token
consumption may not reveal remaining subscription allowance, and other clients can
consume the same subscriptions. Gaffer does not decrement its own guessed account
balances or run a competing reset scheduler. It may keep a short-lived observation
cache and task-level admission limits:

```text
route_observation:
  router_id, route_id, source, observed_at, validity_until
  state: available | degraded | exhausted | unknown
  eligible_connections?, remaining?, reset_at?, retry_after?
```

Task admission order: authority/data policy → runner/harness/route compatibility →
task budget and concurrency reservation → router-reported route availability →
priority/fairness. Unknown quota does not mean zero or unlimited capacity; begin
with bounded concurrency and react to observed availability/errors.

Task-fit selection uses the [versioned route decision](https://github.com/korallis/letmecook/blob/main/docs/contracts/task-routing.md)
from the approved plan or standing policy. Revalidate its requirements, complete
fallback graph and current eligibility during atomic dispatch. A preferred route
cannot override a mandatory capability, data/billing restriction or resource cap.
An attempt's route stays immutable; ranked alternatives are not a Gaffer request
fallback loop. Bootstrap assessment itself uses an explicitly permitted 9Router
route and bounded allowance. The initial alpha supports semantic task assessment;
beta adds richer availability/fairness and measured preference calibration.

Account rotation and model fallback live in 9Router. Gaffer permits only routes
whose complete provider/model/billing set fits project policy. A subscription-only
route excludes paid fallthrough; paid fallback is an explicit choice configured
centrally. Verify that the selected router build enforces the configured policy.

Track wall time, attempts, concurrent processes, daily work and metered-spend limits
at task/project/global level. Where a hard monetary ceiling is required, enforce it
using bounded requests, in-flight reservations and router/provider-side limits. If
request cost or hidden fallback cannot be bounded, show advisory spend and block
that route only under hard-cap policy. Cancellation cannot refund already processed
requests. Include planning, coding, model review, retries and summaries in accounting;
report marginal cash separately from allocated subscription/hardware cost.

Each grant binds an explicit versioned limits profile together with exact router
build, complete route graph, protocol/harness/settings, capability evidence and
authority. `strict-provider-output-v1` is the default and interpretation of existing
grants: a positive finite provider output-token cap must be preserved and enforced
on every approved path. The operator-approved optional
[`native-subscription-local-v1` contract](https://github.com/korallis/letmecook/blob/main/docs/contracts/native-subscription-limits.md)
requires explicit authorization and finite enforced local bytes, concurrency,
request/subattempt/retry counts and deadlines. It declares provider output-token
and monetary bounds unavailable and rejects tasks requiring either hard bound.
Every reachable connection/fallback must have reviewed subscription classification
and required compatibility. A profile change needs a new policy/grant identity
and freeze/drain; no automatic downgrade is allowed. The approved bounded evaluation
envelope is separate from public installation defaults and is not live evidence.

### 8.4 Failure boundary and conformance

A connection rate limit or pre-output request failure that 9Router successfully
handles is not a Gaffer task failure. Keep the same attempt. Gaffer only backs off
or reconciles/retries when the router returns a terminal failure or the harness
cannot continue. Bound retry/time budgets across router, harness and task layers
so nested retries do not multiply without limit.

Test tool calls, structured output, streaming, model settings, cancellation and
continuation through each selected harness/protocol path. In particular, force a
connection failure before output and during a partial stream. Do not replay a
partially executed tool action merely because another subscription is available;
if the router/harness cannot safely continue, preserve the artifact and reconcile
at the task boundary. Model fallback must satisfy required capabilities.

Local timeout, process exit and transport EOF never establish provider cancellation
acknowledgement or refund. Unknown remote work retains its reservation and blocks
replacement and policy-drain completion. A trustworthy original terminal can prove
that an individual request ended without proving a configurable provider-token or
monetary cap. Quiescent failed/incomplete requests remain failures; successful
final/tool release still requires schema validation, original completion, scoped
authority, lease/fence checks and durable decisions.

Router downtime parks inference-dependent work with a visible reason. Capture,
review of stored artifacts and stop remain available. There is no silent direct
provider bypass. Deployment documentation includes 9Router health, credentials,
configuration backup and restore; Gaffer backup references the corresponding router
configuration identity without copying provider secrets into its own store.

M0 must prove the actual single-provider, two-subscription route and the first
harness path. Alpha includes this integration; advanced task admission based on
richer router observations can mature at M4.

## 9. Storage, API and operations

### 9.1 Logical data model

This is an entity/constraint sketch, not executable migration SQL:

```text
daemon_generation(id, created_at, reason, reconciled_at)
repository(id, canonical_remote, allowed_roots, default_branch, policy_revision)
project(id, repository_id, state, context_path, current_brief, current_plan)
capture(id, client_request_id UNIQUE, raw_text, source, route, created_at)
brief_revision(id, project_id, revision, hash, content, state)
plan_revision(id, brief_revision_id, hash, content, state)
authority_grant(id, actor, scope, revisions, limits, expires_at, revoked_at)
task(id, project_id, criterion_ids, dependencies, state, version, current_epoch)
attempt(id, generation_id, task_id, epoch, runner_id, route_id, input_hash, state, lease_id)
lease(id, generation_id, attempt_id, epoch, renewal_nonce, expiry, revoked_at)
runner(id, identity, policy_hash, capabilities, availability, last_seen)
project_runner(project_id, runner_id)
router(id, base_url, inference_credential_ref, status_credential_ref, version, health)
model_route(id, router_id, combo_or_model, protocol, policy, configuration_hash)
runner_route(runner_id, adapter, route_id, probe_version, probe_result)
route_observation(id, route_id, values, source, observed_at, validity_until)
model_usage(id, attempt_id, route_id, request_id, resolved_connection_id,
            model, usage, source, attribution_quality)
budget_reservation(id, attempt_id, scope, resource, amount, state)
artifact(digest PRIMARY KEY, size, kind, path, state, retained_until)
attempt_artifact(attempt_id, digest, role)
verification(id, candidate_digest, profile_hash, evidence_digest, outcome)
approval(id, grant_id, action, candidate_digest, actor, decision, expires_at)
delivery_action(id, generation_id, approval_id, idempotency_key UNIQUE, remote_ref, state, result)
context_proposal(id, source_attempt, content_hash, review_state, commit_sha)
context_index(entry_id, scope, source_commit, index_version, metadata)
trigger(id, project_id, authority_id, rule, timezone, cursor, enabled)
trigger_event(id, source_key UNIQUE, semantic_key, head_sha, disposition)
event(sequence PRIMARY KEY, scope, kind, payload, created_at)
outbox(id, action_key UNIQUE, payload, acknowledged_at)
device_session(id, actor, token_hash, expires_at, revoked_at)
```

Enable foreign keys; enforce acyclic task dependencies, unique attempt epochs,
legal state transitions and one current acceptance per task. Transactions reserve
budget and capacity with assignments. Single daemon ownership uses an OS lock;
refuse a second scheduler against the same live state directory.

### 9.2 Protocols

Use TLS with revocable runner identities. A short-lived single-use bootstrap token
is exchanged for a runner credential; it is not reused as a perpetual password.
Do not log join/session tokens. Protocol messages carry version and message ID.

The provisional [execution contract](https://github.com/korallis/letmecook/blob/main/docs/contracts/execution.md#2-version-and-wire-contract)
owns execution message semantics, version negotiation and acknowledgements, using
the shared Go/TypeScript schema. Bootstrap, journal/state sync, stream and upload
encodings remain named reconciliation obligations there; this design does not
define a competing wire schema.

Restore admission follows the contract's
[fencing and replacement barrier](https://github.com/korallis/letmecook/blob/main/docs/contracts/execution.md#4-initial-lease-renewal-and-replacement-barrier),
including original-daemon ownership and every possibly active old runner.

All commands use the existing runner-initiated connection. Artifacts upload through
an authenticated bounded endpoint. Streams are backpressured; spool locally within
a cap and indicate truncation rather than allowing logs to exhaust memory/disk.

HTTP API lives under `/api/v1`; mutating requests need authenticated actor, CSRF/origin
validation for browser sessions, idempotency keys and expected revision where
applicable. Never put action credentials in model prompts or URL query parameters.
Worker tokens, if used, are short-lived and scoped to that attempt's permitted actions.

UI updates use SSE with event IDs and bounded replay. On an expired cursor the client
loads a current snapshot then resumes the stream. Reconnection alone does not recover
lost events. Authenticate SSE with the same session policy as the API.

### 9.3 Private web application

Bind loopback by default. Setup creates the owner with a one-use local bootstrap;
remote access requires configured private HTTPS and authentication. Use secure,
HttpOnly, SameSite cookies, session revocation and explicit idle/absolute lifetimes.
A private network is not a substitute for access control. Reauthentication for
sensitive settings is an implementation decision that must not cause repetitive
approval for already-authorised routine work.

v1 PWA caches the application shell and, on opt-in trusted devices, unsent captures
in IndexedDB. Do not cache authenticated API responses, approvals, provider secrets
or transcripts by default. Service workers require a secure context; a bare private
HTTP address on a phone is insufficient. Reopen/foreground reconnect is the reliable
sync path, with client IDs deduplicating resubmission. Logout offers deletion/export
of pending drafts. [MDN service workers](https://developer.mozilla.org/en-US/docs/Web/API/Service_Worker_API)

### 9.4 Backup, restore and storage limits

SQLite WAL runs on a local filesystem with an explicitly selected durability setting
(initial target `synchronous=FULL` for acknowledged authority/state writes). A live
WAL database is not backed up by copying only its main file. Use SQLite's online
backup API or an equivalent documented consistent snapshot procedure.
[SQLite WAL](https://sqlite.org/wal.html), [backup API](https://www.sqlite.org/backup.html)

Backups contain a consistent state snapshot, manifest-referenced artifacts and the
context commits it references, plus schema/version metadata. Copy immutable blobs
reachable from the snapshot and retain them until backup finishes; pin context
commits. Provider credentials are separately managed and must be re-established on
restore. Restore starts paused with a new execution generation and runner/authority
reconciliation, never blindly replaying a restored outbox against newer remote state.

Test process-crash recovery separately from host/disk loss and full restoration.
Record achieved RPO/RTO for the backup schedule; a 60-second scheduler restart target
is not a disaster-recovery guarantee.

Events are append-only during normal operation, but have explicit retention/export
and sensitive-data deletion policy. This is an operational audit trail, not tamper
proof against a host administrator. Configure artifact/log limits, free-space
thresholds and visible GC. Never collect artifacts supporting pending review,
unknown outcomes, active attempts or incomplete backup.

## 10. Triggers and extensibility

v1 uses timezone-aware schedules and outbound GitHub polling with durable cursors,
pagination, overlap for missed events, rate-limit backoff and semantic deduplication.
Define DST handling explicitly: skip nonexistent local times; execute a repeated
local-time occurrence once using a stable schedule occurrence ID. Restart catch-up
is capped and visible rather than dispatching every missed run at once.

A trigger template has standing authority, repository/head filters, budget, expiry
and deduplication horizon. Repeated CI failures coalesce by repository, workflow and
head SHA; ignore stale heads and bound the fix/retry cycle. Fork-origin/untrusted
content cannot run with trusted-project credentials. Templates cannot call arbitrary
external actions based on event prose.

A future public webhook option needs an explicit authenticated relay/ingress design,
signature verification, bounded payloads, delivery-ID deduplication and queue-before-ack.
It is not compatible with a literal “no inbound anywhere” claim. GitHub documents
signature/secret and delivery practices; polling avoids that ingress dependency in
v1. [GitHub webhook guidance](https://docs.github.com/en/webhooks/using-webhooks/best-practices-for-using-webhooks)

Skills and adapters are optional code/instruction dependencies, pinned and reviewed.
They inherit existing permissions and cannot modify authority. No marketplace or
team execution pool is required for the solo product.

## 11. Technology decisions and unresolved gates

| Choice | Current decision | What can change it |
| --- | --- | --- |
| Core/runner | Narrow Go 1.26.5 build selected in decision record; lock and execution remain blocked | Same-workflow reuse proof materially reduces owned correctness code; provisional effort is not a selection reason |
| State | SQLite, local WAL, transactional outbox; no broker | Measured single-daemon limitations, not hypothetical enterprise scale |
| UI | React + TypeScript + StyleX, embedded in daemon; product SSE in M2 | Actual authenticated/mobile validation; #95 fixture shell is not product acceptance |
| Knowledge | Markdown under git, explicit review, derived FTS index | Held-out evidence for embeddings/curation |
| Model interface | Harness structured events plus 9Router inference APIs; direct API planner through the same router | Need for a tested richer protocol |
| Tool format | JSON contracts, compact summaries; optional measured TOON | Accuracy/latency/cost results for real payloads |
| Model-access control plane | 9Router from M0; multiple subscriptions per provider, named routes and account fallback | Validate the pinned integration and fill specific API gaps; do not rebuild account routing in Gaffer |
| Auto-merge | Beyond v1 | Explicit policy and safety evidence; always subject to GitHub branch rules |

The expensive architecture choice is not the programming language. It is owning
process containment, durable distributed execution and external
effect reconciliation. The
[decision record](https://github.com/korallis/letmecook/blob/main/docs/decisions/0001-execution-foundation.md)
compares pinned intentic and Claudexor source against the same workflow, names
missing measurements and bounds M1 ownership. Existing `cmd/gafferd/`,
`internal/store/`, `internal/httpapi/` and `schemas/` stay provisional until
#9/#10/#11 reconciliation; do not create parallel `internal/http/` or `internal/api/`
owners. Future `cmd/gaffer-runner/` owns one qualified runner and `cmd/gaffer/` the
local CLI. The [provisional-slice reconciliation](https://github.com/korallis/letmecook/blob/main/docs/decisions/0001-execution-foundation.md#provisional-slice-reconciliation)
owns the disposition and acceptance limits of the `web/` shell and merged protocol.
The [execution contract](https://github.com/korallis/letmecook/blob/main/docs/contracts/execution.md)
owns message semantics and reconciliation obligations.

[M0](roadmap.md) is not passed: the first live trial
failed with unknown remote work, #89 has no newly measured Docker-free profile,
and real baseline/candidate workflow measurements and fresh Opus review remain
pending. No first supported execution runtime or retry authority follows from
this design choice. Provider credential handling and account/request routing
remain delegated to 9Router.
