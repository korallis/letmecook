# Gaffer — Product Requirements

**Status:** proposed baseline v0.4 · **Owner:** solo maintainer · **Updated:** 17 September 2026

This revision follows the [evaluation](evaluation.md). Requirements below are
prospective. Story IDs from the original plan are retained; their release assignment
now distinguishes the first useful product from later capability.

## 1. Outcome and product hypothesis

Gaffer turns an informal idea into a clear agreement, keeps authorised work moving
across sessions, and returns verified changes for the operator to accept.

The primary hypothesis is that explicit intent, durable progress and a coherent
review flow reduce operator effort on work spanning multiple sessions. Neither a
large fleet nor fully automated memory is required to test that hypothesis.

An operator-configured model gateway is the required shared model-access boundary,
even with one harness; the current selection is CLIProxyAPI. The gateway owns
provider credentials, accounts and subscriptions, rotation, cooldown and request
fallback. Gaffer owns the work, assesses each task's requirements and selects an
eligible gateway model ID or alias rather than an account credential. An operator's
preference matrix seeds that choice; task evidence, verified capabilities and
constraints determine suitability. A coordinator
is a logical role that plans and reports; it is not necessarily an always-running
model conversation. Models wake for useful work, then stop consuming quota.

## 2. Audience and boundaries

**Primary:** one developer with an always-on machine, a laptop, one or more coding
agent accounts and projects that outlast a single session. They want to capture
ideas away from the desk and return to comprehensible, reviewable progress.

Three constraints define this person: their attention is scarce; reachable machines
are not automatically available for work; and their existing subscriptions have
limits they do not fully control.

**Later:** small teams that permit specified cloud model processing but want to own
execution and project state. Collaboration, tenant isolation and shared accounts
require a separate design. Tinkerers can benefit from the single-user product.

**Outside the initial audience:** air-gapped or “no code may leave the network”
deployments, production-host execution, and enterprises requiring SSO/RBAC. Cloud
harness use is incompatible with a blanket no-egress promise. Local-only inference
would require a complete separately tested deployment profile.

## 3. Principles

- **Make the agreement inspectable.** Show outcome, exclusions, assumptions and evidence of completion together.
- **Spend attention proportionally.** Small work gets one combined brief-and-plan approval. Reuse existing authority without repeated prompts; ask when a material uncertainty or change exceeds it.
- **Treat clarification as a budget, not a ceiling on understanding.** Default to at most three questions per round, with options and free text. Never guess a consequential answer merely to meet that target.
- **Current instructions win.** Remember scoped preferences, but reconfirm when context changes. A preference is not permission.
- **One model-access boundary.** All agents use the operator-configured model gateway, currently CLIProxyAPI. Configure provider credentials, accounts/subscriptions, rotation, cooldown and fallback there; Gaffer consumes allowed model targets and owns task orchestration without a second account router or quota ledger. Configured providers may receive code.
- **Separate planning from execution.** A model can propose work. Deterministic policy decides what can run, where, and with what authority.
- **Protect continuity before increasing concurrency.** Persist attempts, preserve partial work, bound retries and reconcile uncertain outcomes.
- **Use simple memory first.** Reviewed files and explicit progress precede learned idioms, embeddings and automatic curation.
- **Measure accepted outcomes.** Tokens, busy hours and agent count are costs or diagnostics, not success by themselves.

## 4. Release boundaries

| Release | Included | Deliberately absent |
| --- | --- | --- |
| Private alpha, after M2 | One operator/repository/runner/harness through the configured model gateway; exact protocols and allowed model IDs or aliases; gateway-owned provider access and request fallback; sequential tasks; responsive capture and approval; durable recovery; stop; explicit memory; verified artifact review and authorised PR publication | Parallel writers, auto-merge, chat, offline capture, automated curation |
| Beta, after M4 | Second harness on the same configured gateway; explicit multi-runner placement; bounded parallelism; integration before acceptance; task scheduling informed by gateway availability where exposed; manual context retrieval | Inferred quota precision, multi-user team pools |
| v1, after M6 | Reliable scheduled/GitHub-polled work; standing authority; digest; PWA draft capture; release/install/restore documentation; measured solo-operator workflow | Full code review editor, auto-merge, automatic memory promotion, generic plugin ecosystem |
| Later experiments | Learned idioms, hybrid retrieval, additional adapters, skills authoring, browser/desktop runners | No commitment until their own evidence gates pass |

Milestones and dependency gates are in [the roadmap](roadmap.md). No release label
implies those capabilities have been built.

## 5. End-to-end journey

### Phase A — Set up an explicitly permitted workspace

#### US-A1 — Get to a first useful result

**Alpha.** The operator connects the daemon to a configured model gateway, connects
a runner, verifies a harness, and sees one concrete next action.

- [ ] `gaffer up` creates private state and prints the local setup URL; a diagnostic explains missing git, gateway connectivity, harness, authentication or isolation support.
- [ ] A packaged daemon serves its UI without a separately installed UI runtime. The runner may require the documented isolation runtime and provider CLI.
- [ ] First-run setup shows where state, execution files and the gateway credential reference live; provider credentials live in the gateway.
- [ ] Uninstall stops services and removes program files; context and recovery artifacts are preserved unless the operator chooses to delete them.

#### US-A2 — Decide which machines can work

**Alpha foundation; multiple machines in beta.** Joining is not permission to execute.

- [ ] A one-use, expiring join token registers a runner in `off` mode.
- [ ] Each project has an explicit runner allowlist. First-project setup can copy the selected default runner into that list; later default changes never migrate existing projects silently.
- [ ] Required capabilities only filter already-permitted runners.
- [ ] No match yields a named blocked reason; the scheduler never broadens placement.
- [ ] The machines view separates eligibility, capabilities, connectivity and active attempts.

#### US-A3 — Protect machines allocated to other work

**Alpha exclusion; reservation support in beta.** Client or production hosts are
not used by the initial product. On dedicated development machines, local policy
can reserve a runner to exact repositories/projects.

- [ ] Runner-local policy refuses tasks outside its allowed identities, regardless of scheduler selection.
- [ ] Policy files and runner control credentials are inaccessible to project code.
- [ ] Weakening a reservation requires an operator action on that host; refusals appear with a reason in the audit view.
- [ ] Reservation is described as placement control, not as process or data isolation.

#### US-A4 — Use one configured model gateway

**Alpha: operator-selected gateway, currently CLIProxyAPI.** See the
[model gateway contract](contracts/model-gateway.md) and
[integration findings](evaluation.md#4-provider-and-gateway-feasibility).

- [ ] Configure one gateway with a stable ID, operator-selected HTTPS base URL, protected credential reference, supported protocol set and exact allowed model IDs or aliases. Never store a credential value in Gaffer.
- [ ] Alpha assesses concrete tasks and scoped steps before choosing among approved gateway model targets; a fixed role-to-model matrix alone is insufficient. Separate mandatory capabilities, data/billing limits and uncertainty from preference ranking.
- [ ] Explain the proposed choice, relevant evidence/unknowns and eligible alternatives; support an operator override or standing automatic-selection policy within the permitted envelope.
- [ ] Bound any model-assisted assessment through an allowed bootstrap model. Record selection and evidence versions; revalidate at dispatch and preserve the selected target throughout an attempt.
- [ ] Provider login, credentials, accounts/subscriptions, rotation, cooldown and request fallback are managed in the gateway. Gaffer does not expose parallel account controls or a quota ledger.
- [ ] Where multiple accounts or subscriptions back one target, gateway-owned rotation or fallback does not require changing agent configuration or redispatching the Gaffer task. Do not claim this behavior without gateway-specific evidence.
- [ ] Duplicate aliases for one account never imply extra capacity; separate accounts count as separate capacity only where the gateway establishes it; shared organisation limits are respected where applicable.
- [ ] A probe verifies the exact harness→boundary→gateway→model path, including required streaming/tool behavior. Workers receive only attempt-scoped boundary access; the boundary injects the gateway credential and pins permitted protocols and models.
- [ ] Usage and status observations name their source and freshness. Missing management APIs, headroom, reset, resolved account or fallback details stay unknown. Paid fallback must be explicit in the gateway policy and proven enforceable where required.

The [task-routing contract](https://github.com/korallis/letmecook/blob/main/docs/contracts/task-routing.md) defines the optional
operator preference example, hard eligibility, assessment budget, fresh review
context and comparison with static selection. Exact model/settings support is
verified per installation; labels in a preference table are not capability facts.

#### US-A5 — Connect a repository with scoped access

**Alpha.** The operator selects the repository, base branch, protected paths and
verification commands once.

- [ ] A read-only clone/access check verifies the repository identity and allowed remote.
- [ ] Checkout/build execution receives no general-purpose forge write credential or host SSH agent.
- [ ] Publication uses a separate trusted component with a reference to scoped credentials.
- [ ] A repository with uncommitted operator work is copied or isolated without overwriting those changes.

#### US-A6 — Use a private application from a phone

**Alpha online; offline drafts at v1.** One authenticated URL exposes the complete
operator flow, with detailed diffs linked to GitHub when needed.

- [ ] Private-network HTTPS is documented for remote devices; localhost serves local setup.
- [ ] Private networking supplements application authentication. Device sessions can be revoked and expire according to explicit policy.
- [ ] Capture, brief review, answer, pause and stop work at phone widths with keyboard/screen-reader access.
- [ ] Offline drafts are opt-in on trusted devices, visibly unsent, deduplicated on submission, and retained if sync fails. Sending is retried on foreground reconnect; background sync is not guaranteed.
- [ ] Approvals, publication and stop require a live server acknowledgment; offline UI never claims they succeeded.

### Phase B — Turn an idea into an agreed, verified change

#### US-B1 — Capture before the idea disappears

**Alpha.** Prose is enough to save an idea; routing is a suggestion the operator can correct.

- [ ] Capture is prominent on the home screen. Project selection is optional where context identifies one unambiguously.
- [ ] Uncertain repository routing leaves the capture in the inbox. It does not authorise exploring other repositories.
- [ ] The UI distinguishes saved, analysing, awaiting clarification and executing.
- [ ] Acknowledgment is fast; a separate target measures bounded discovery starting within 60 seconds when authorised, online and resources are available.
- [ ] Browser-dictated text is ordinary editable text; no transcription service is required.

For “session refresh may be racing token rotation”, Gaffer may inspect the permitted
repository under an existing discovery allowance and prepare findings. It does not
implement the suggested lock merely because the user mentioned it.

#### US-B2 — Correct a brief before code changes

**Alpha.** The brief contains outcome, constraints, exclusions, acceptance criteria,
allowed paths/systems, assumptions and unresolved questions.

- [ ] A bounded read-only discovery job may precede approval under explicit project setup authority, with a wall-time/usage cap. No build scripts, installs or hooks run as part of that discovery allowance.
- [ ] Each assumption names supporting context or is labelled inference. Confidence is descriptive, not a calibrated probability unless validated.
- [ ] Prefer up to three questions per round. Material unresolved ambiguity blocks affected work or narrows the proposed scope explicitly.
- [ ] Brief edits create immutable revisions with stable acceptance-criterion IDs; tasks reference IDs, not mutable line numbers.
- [ ] Current user instructions override prior idioms. Remembering an answer requires scope and evidence, not automatic global promotion.
- [ ] Compare completed work with the criteria using deterministic checks and human review; an optional model critique is supplementary evidence.

“Tidy up auth” can become “remove dead code and pass lint, preserve behaviour,
limit changes to server auth.” The operator must be able to see and correct the
assumption that “auth” means the server module.

#### US-B3 — Learn only what the work needs

**Alpha.** Build a small repository map and record verified commands and relevant
constraints. Avoid a full repository research project before every small task.

- [ ] Discovery records repository commit, paths and evidence; findings can be corrected.
- [ ] Read-only inspection precedes mutation; executing repository commands needs execution authority and containment.
- [ ] Relevant approved context is supplied automatically within a visible budget.

#### US-B4 — Approve a short plan

**Alpha list; beta graph when helpful.** A plan contains concrete tasks, dependencies,
expected verification, permitted execution and budget.

- [ ] Small work presents brief and plan together for one approval; multi-step work remains editable before dispatch.
- [ ] Every task serves a criterion or an explicit enabling step. Unrelated additions are rejected.
- [ ] Approval binds the exact brief/plan revisions, repository, scope, allowed runners/providers, operations and budget.
- [ ] Changes outside this envelope require new authority; retries and refinements inside it do not repeatedly ask the same permission.
- [ ] Superseding intent holds affected queued work and revokes affected active attempts before replacement work begins.

#### US-B5 — Understand current progress

**Alpha.** The board distinguishes queued, running, reconciling, blocked,
awaiting review, accepted, published, merged, failed and cancelled.

- [ ] Show current attempt, runner, harness, requested gateway model ID or alias, resolved provider/model/account when available, activity age, limits and next action.
- [ ] Reconnecting or refreshing recovers stored events and indicates any log gap.
- [ ] Pause stops new dispatch. Cancel requests active termination; the UI shows whether termination was acknowledged.
- [ ] Re-run creates a new attempt with a reason and bounded retry allowance.

#### US-B6 — Review the outcome with its evidence

**Alpha.** A local review package contains the exact artifact/commit, diff, summary,
criterion mapping, verification results, known limitations and context proposals.

- [ ] The reviewer can accept, reject with feedback, or inspect the full patch; failed or missing required checks prevent “verified” status.
- [ ] Feedback continues the same task as another attempt, preserving prior evidence.
- [ ] Accepting a result, publishing a branch/PR and merging are distinct actions. A combined action names all effects explicitly and reuses recorded standing authority when applicable.
- [ ] GitHub is the source of truth for PR review, branch rules, checks and merge status.
- [ ] New commits invalidate artifact-specific acceptance. Per-hunk editing in Gaffer is deferred because it creates a new result requiring verification.

#### US-B7 — Use the right permitted machine

**Beta.** Match capabilities after placement and data-access policy.

- [ ] Required environment/isolation/harness capabilities are checked at dispatch and claim.
- [ ] Integration tests use dedicated development services; production credentials and databases are not fallback dependencies.
- [ ] Interactive desktop/browser runners are deferred until a separate permission and isolation profile exists.

### Phase C — Work unattended within an explicit envelope

#### US-C1 — Return to a useful digest

**Alpha in-app; v1 scheduled digest.** Show decisions needed first, then accepted
work, verification failures, stopped attempts and resource blocks.

- [ ] Each item links to the current task and evidence; stale approvals cannot be applied to new artifacts.
- [ ] No-action updates remain quiet by default. Notification destinations are opt-in.
- [ ] Sending to a chat channel is a later integration with separately configured authority.

#### US-C2 — Respond to capacity limits

**Alpha task-aware gateway-model selection and caps; beta capacity-aware task scheduling.**
Gaffer selects an eligible gateway model ID or alias for the work; the configured
gateway owns provider accounts/subscriptions, rotation, cooldown and request fallback.

- [ ] A successful gateway-owned rotation or fallback continues the current attempt without Gaffer redispatching the task.
- [ ] Gaffer consumes safe aggregate availability where exposed rather than maintaining competing per-account balances; missing account/subscription observations stay unknown.
- [ ] When the whole model target or gateway is unavailable, preserve the attempt and work. A replacement attempt starts only after the prior execution is fenced and its artifacts reconciled.
- [ ] Allowed providers, model capabilities and billing classes constrain the complete fallback envelope. No cross-provider transcript portability is assumed; an uninspectable envelope makes the target ineligible rather than assumed compliant.
- [ ] Show gateway-reported reset/next probe time where available, otherwise “unknown”; use bounded task-level backoff without thrashing.
- [ ] Concurrency, duration, attempts, daily work and enforceable spend caps apply to discovery, planning, coding and verification.

#### US-C3 — Recover from provider outages

**Alpha through the configured model gateway.** Request-level account/provider
fallback belongs to the gateway; Gaffer handles only failures that escape that layer.

- [ ] Use approved model IDs or aliases across agents; keep provider account priority, rotation, cooldown and request fallback in the gateway.
- [ ] Distinguish a provider/account observation where exposed, a model target failure and the gateway itself being unavailable. Gateway downtime parks inference-dependent work without direct-provider bypass.
- [ ] Test any claimed request fallback before output and during streaming. Where continuation is unsafe, surface the failure and reconcile the attempt rather than replaying tool effects.
- [ ] Surface gateway health and supported usage/error signals where a safe interface exists. Missing management APIs stay explicit unknowns or scoped integration work, not a second account router.

#### US-C4 — Start recurring work

**v1.** Begin with schedules and outbound GitHub polling. Public webhook and chat
integration are deferred options.

- [ ] Each trigger has a named repository, allowed operation template, schedule/event filters, budget, expiry and standing approval.
- [ ] External issue/PR text is task data and cannot expand authority.
- [ ] Deduplicate delivery IDs and semantic events; bound retries and prevent a fix from repeatedly triggering itself.
- [ ] CI fixes require a failing current head and owned/authorised branch. Stale events are ignored; other branches produce proposals for approval.
- [ ] Dry-run records proposed actions without dispatch; triggers can be paused independently or globally.

#### US-C5 — Stop with an honest acknowledgment

**Required from M1.** Stop durably prevents new execution/publication and revokes leases.

- [ ] Connected runners target termination acknowledgment within five seconds, including child processes.
- [ ] Disconnected runners are shown as unconfirmed until acknowledged. Their local watchdog must terminate at lease expiry; the UI displays the bound.
- [ ] Stop cannot retract a provider request or forge operation already accepted externally; those outcomes are reconciled.
- [ ] Partial work is retained. Resume evaluates artifacts and authority before creating another attempt; it does not blindly replay a transcript.

### Phase D — Retain useful knowledge over months

#### US-D1 — Stop rediscovering established facts

**Alpha explicit files; beta retrieval.** Keep approved brief, progress, decisions and
verified run commands. Repository facts are reusable across projects; project intent
and sensitive findings remain scoped.

- [ ] Context comes with repository revision, evidence and review status.
- [ ] Workers propose changes; a single writer commits operator-approved proposals to the context repository.
- [ ] Retrieval distinguishes supplied, cited and demonstrated-useful context. Retrieval alone is not a success metric.

#### US-D2 — Remember how the operator uses words

**Later experiment.** Store only confirmed, scoped preferences with an edit/remove UI.

- [ ] Applied preferences appear as assumptions; contradictory current instructions win.
- [ ] Changes in repository/scope can require reconfirmation. Cross-project promotion is explicit.
- [ ] Evaluate misunderstanding rate as well as question count; fewer questions with more errors is a regression.

#### US-D3 — Challenge stale memory

**Alpha provenance/manual review; beta staleness signals.** Failed sourced commands
flag evidence for review without declaring the note false automatically.

- [ ] Record source commit, relevant paths, confirmation time, owner and review state.
- [ ] Contradictions and dirty external edits block automatic context updates.
- [ ] Retire/archive entries explicitly; pinned guidance still displays stale-evidence warnings.
- [ ] An unavailable dependency or network error is distinguished from an obsolete instruction.

#### US-D4 — Reduce review selectively

**Deferred beyond v1.** Auto-merge requires a separate policy, recorded authority,
current checks and branch rules. No trust level increases itself after a streak of
successful tasks. Revocation applies to pending actions, including in-flight work.

#### US-D5 — Survive process failure and restore

**Required from M1.** Persist accepted transitions and retain runner journals until
artifacts are durably acknowledged.

- [ ] Restart reconciles active attempts without assuming a missed heartbeat means they stopped.
- [ ] Unknown outcomes hold dependent work. Stale attempts cannot publish or become accepted results.
- [ ] Recovery and backup restoration are tested, including daemon/runner interruption and missing artifacts.
- [ ] Recovery distinguishes durable acknowledged work from unsent local work lost with a destroyed runner disk.

#### US-D6 — Measure what was delivered

**Instrumentation from alpha.** Record accepted work, human review/edit time,
rework, blocking causes, observed usage and spending with confidence labels.

- [ ] Metrics export as CSV and JSON, with cohort size and denominators.
- [ ] Costs distinguish incremental cash, allocated subscription/hardware costs and operator time.
- [ ] Planning/discovery/verification overhead counts in comparisons, including failures and retries.

#### US-D7 — Reduce overhead without reducing quality

**Compact output from alpha; encoding experiments later.** Use small summaries,
lazy detail retrieval and schema-validated JSON contracts.

- [ ] Compare compact JSON, TOON and other suitable encodings on representative payloads with task accuracy and latency, not token count alone.
- [ ] Keep code, diffs and prose in their native form; do not require all context to be TOON.
- [ ] Measure total tokens per accepted outcome and cache effects. Adopt encoding only when the measured benefit outweighs complexity.

### Phase E — Make the tool maintainable and portable

#### US-E1 — Support another harness predictably

**Alpha internal adapter; beta second implementation; extension ABI later.** A
small versioned contract exposes run, event, cancellation, capabilities and best-effort usage.

- [ ] Adapter conformance covers auth, approval blocking, cancellation, isolation, structured output, retries and version drift.
- [ ] Unsupported capabilities are explicit. ACP is an optional transport where verified, not a universal assumption.
- [ ] Built-in Go adapters compile with the binary. Future external adapters are explicit executable plugins with a versioned protocol, not dynamically loaded source files.

#### US-E2 — Keep memory usable without Gaffer

**Alpha.** Context is readable Markdown under git, with a documented export.

- [ ] Reviewed changes record task/proposal IDs and source evidence.
- [ ] External edits are detected and reloaded without overwriting dirty work; reindex when indexing is enabled.
- [ ] Uninstallation preserves knowledge. Full workflow recovery also requires the state/artifact backup; the context repository alone is not a full backup.

#### US-E3 — Reuse workflows as skills

**Later.** Support reviewed Agent Skills files without granting extra authority.
Pin source/version, expose loaded skills in the run manifest and run their code
under the same task boundary. A skill file is an instruction source, not a sandbox.

## 6. Validation and success measures

Targets below are provisional engineering gates, not market claims. M0 records a
baseline; changes to targets require a written reason. Use matched task cohorts,
fixed starting commits and recorded harness/model/config versions.

| Measure | Definition and gate |
| --- | --- |
| Outcome fidelity | Delivered result satisfies the operator-labelled acceptance criteria with no material scope breach; alpha pilot target at least 24 of 30 feasible task cases, each including its bounded attempts, with all failures reported |
| Brief usefulness | Fraction approved without material scope/criteria edit, plus correction time and false-assumption count; diagnostic, not proof of intent fidelity |
| Operator effort | Per-task active capture, planning, approval, review, correction and recovery minutes, including unsuccessful cases; also report total cohort minutes per accepted outcome and completion rate. Beta target: median paired operator-time reduction of at least 20% versus the standalone baseline on 20 tasks, with spread and task classes reported |
| Unattended completion | Accepted tasks needing no intervention between dispatch and review, divided by all eligible started tasks; report alongside failure/block rates, not busy hours |
| Delivery defects | Scope misses, regressions and reverts within seven days of acceptance; no efficiency claim if this worsens materially |
| Authority/durability | Zero unauthorised publication, duplicate publication or lost acknowledged result in release fault tests; any occurrence blocks release |
| Recovery | p95 safe scheduler recovery under 60 seconds for the 100-task test fixture; provider availability and lost runner disks measured separately |
| Phone flow | p95 online capture acknowledgment under two seconds on the defined private network; discovery-start target under 60 seconds under stated prerequisites |
| Resource cost | All-in usage per accepted outcome, incremental cash and allocated ownership cost; no predetermined “40% of hosted cost” claim |
| Memory utility | Matched-task rediscovery/correction effort with and without approved memory, plus stale-context incidents; citation rate is only supporting evidence |
| Onboarding | Three external evaluators reach a verified local artifact within 30 minutes after prerequisites; report clean-machine prerequisite time separately |

Use 30 development captures and at least 30 held-out captures with operator-defined
intended outcomes. Include ambiguous routing, changed intent, misleading preferences
and impossible requests. Score impossible or unauthorised captures separately for correct blocking, clarification
or refusal; exclude them from the 30 feasible execution cases. A small pilot informs
iteration; it does not establish a universal accuracy rate.

Predeclare defects as critical (unauthorised effects, data loss or exploitable boundary
failures), major (material scope miss or functional regression), or minor (other
corrections). Any critical defect blocks release. For the small paired beta pilot,
Gaffer must have no more major defects than its comparator; report minor defects
without claiming equivalence from a small sample. See [evaluation design](evaluation.md#6-how-to-test-the-product-hypothesis).

## 7. Decisions still requiring evidence

1. **Build or reuse:** compare the closest existing tools using the same acceptance checklist before M1.
2. **First harness:** select the best proven harness/configured-gateway/isolation combination in M0; verify each required protocol/model path and any gateway behavior the product will rely on.
3. **Coordinator implementation:** call an allowed gateway model ID or alias through its compatible API and trusted inference boundary with structured output and typed tools. A model receives no general shell or gateway credential in the planner role.
4. **Parallelism:** retain sequential execution when delegation increases operator effort or defects.
5. **Packaging:** keep the proposed Go core, but prove process supervision, SQLite and distribution on target systems before treating “one binary” as solved.
6. **Advanced memory:** add it only after measured pain justifies the maintenance cost. One operator-configured model gateway is already an architectural decision; CLIProxyAPI is the current selection, not a mandatory product dependency.
