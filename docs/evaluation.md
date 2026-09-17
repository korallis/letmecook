# Gaffer — Evaluation and Research

**Reviewed:** 14 September 2026 · **Scope:** all four original Markdown documents
and the static HTML reader · **Result:** proposed planning baseline v0.4

This review challenges the original plan and supplies the rationale for revisions
to the [PRD](PRD.md), [specification](spec.md) and [roadmap](roadmap.md). The folder
contained planning documents, not an application implementation. Research verifies
what primary sources document; it does not establish that a competitor, harness or
security mechanism works as advertised in Gaffer's intended environment.

**17 September 2026 clarification:** the product requirement is one
operator-configured model gateway, currently CLIProxyAPI, not 9Router specifically.
The gateway owns provider credentials, accounts/subscriptions, rotation, cooldown
and request fallback. Gaffer builds work orchestration above that boundary without
duplicating account routing or quota accounting. The v0.4 9Router decision and its
source evidence remain historical gateway-specific material. The v0.3
recommendation to defer a shared gateway was an overcorrection; v0.4 selected
9Router, and this revision generalizes that selection to any operator-configured
gateway.

## 1. Overall judgment

The product intent is coherent: you want to describe work naturally, retain the
agreement and progress over time, run agents on explicitly chosen machines, and
return to work you can understand and review. Phone access and existing model
accounts make that useful in your actual life, rather than only at a terminal.

The original plan makes delivery harder by treating almost every supporting idea
as essential to v1. It simultaneously proposes novel intent inference, a distributed
execution engine, task scheduling across model capacity, an automated knowledge
curator, a review editor and an integration platform. Several absolute promises
are stronger than the architecture can provide.

**Recommendation:** prove a complete durable single-worker workflow first. Keep
local ownership, intent, permissioned placement, portable memory, coding harnesses
and shared model access through the configured gateway. Delay sophistication until a measured operator problem requires it.

The differentiator to test is continuity from intention through accepted change.
There is no evidence yet that this requires multiple agents, a separate strong
coordinator model, automatic curation or mandatory TOON. These can be useful options
without defining the product's first release. The model gateway boundary is
different: it is the chosen provider/account-routing foundation that reduces what
Gaffer has to build. CLIProxyAPI is the current operator selection.

## 2. Findings and resulting changes

Priority describes implementation dependency, not a claim of an exploitable bug
in software that does not yet exist.

| Priority | Original assumption/problem | Evaluation and revision |
| --- | --- | --- |
| P0 | Heartbeat loss immediately requeues work | A partition does not prove the previous process stopped. Add attempt identities, leases, local watchdogs, fencing, journals and outcome reconciliation before autonomous execution. |
| P0 | Worktrees, path fences and command denylists contain adversarial agents | Checkout isolation is different from process containment. Require a tested execution boundary; document credential exposure and keep client/production hosts outside the initial profile. |
| P0 | Stop always halts every machine in five seconds | Distinguish durable stop intent, connected acknowledgment and disconnected lease expiry. Never show unreachable work as confirmed stopped. |
| P0 | First plan approval, local acceptance and opening a PR flow together | Bind authority to revision/scope and candidate/action. Execution, publication and merge have distinct effects; reuse standing authority only inside its recorded envelope. |
| P0 | Parallel patches work before an integration strategy exists | Record exact bases and predecessor artifacts. Start with serial integration and rerun checks on the combined candidate before enabling parallel writers. |
| P1 | The coordinator never uses shell, but must invoke a shell CLI | Give planning a restricted structured-proposal role or typed tools; expose the CLI separately over the same API. Explicitly account for planner model auth and spend. |
| P1 | “Before it spends anything” yet findings appear before brief approval | Discovery and planning consume resources. Permit capped read-only discovery under standing project authority; code mutation follows the approved brief/plan. |
| P1 | Three questions maximum and never ask again | Keep a default per-round budget, but block or clarify unresolved material ambiguity. Scoped preferences must not override current instructions or become permissions. |
| P1 | Self-hosting serves people who cannot send code out | Cloud model calls still transmit code/context. Target the solo operator; defer air-gapped and no-egress claims. |
| P1 | Account routing and task scheduling were conflated | Keep provider credentials, accounts/subscriptions, rotation, cooldown and request fallback in the configured model gateway. Gaffer consumes allowed model IDs or aliases and safe availability observations, and owns task admission/recovery. Verify concrete interface gaps early; missing status and quota stay unknown. |
| P1 | Runtime state is one easily copied file; runners hold nothing valuable | A live SQLite WAL database needs a consistent backup. Preserve runner work until acknowledged; back up state, artifacts and context together and test paused restore. |
| P1 | “Nothing inbound” coexists with browser access and public webhooks | Define no public inbound exposure for the baseline; document private HTTPS and application auth. Start triggers with schedules and outbound polling. |
| P2 | Intent, memory and task quota scheduling are largely unsolved elsewhere | Primary sources show substantial overlap. Replace novelty claims with a reuse comparison and a measurable workflow hypothesis. |
| P2 | Every model payload must be TOON; CLI excludes MCP by principle | Keep compact responses and lazy detail; benchmark encoding/transport on actual task quality and end-to-end overhead. |
| P2 | Busy hours, context hits and cheap PRs establish value | Measure acceptance-criteria fidelity, operator effort, defects, retries and all-in cost. Treat the original numeric savings claims as hypotheses. |
| P2 | A static HTML copy is maintained independently | Generate the reader from Markdown and check it for stale content, broken navigation and mobile layout. |

### What remains central

The local control plane, an always-on home for projects, capture from a phone,
versioned briefs, a coordinator that plans rather than edits code, opt-in placement,
gateway-owned provider/account access, portable context,
explicit paid opt-in and review
before delivery all remain. The revision changes how to prove and deliver them.

### What has been removed as a release promise

Guaranteed savings versus hosted fleets; precise subscription headroom everywhere;
instant recovery from every failure; adversarial containment through shell denylists;
all-context TOON; transparent arbitrary provider failover; one-hour installation
with no isolation dependencies; an air-gapped persona using cloud subscriptions;
and automatically trustworthy memory. These claims can return only with evidence
and an accurately bounded deployment profile.

## 3. Build or reuse before building

The original alternatives are relevant enough to evaluate before owning a new
execution engine. The table describes documented overlap, not a quality ranking.
All sources were checked on 14 September 2026.

| Candidate / primary source | Documented overlap | What still needs testing for this decision |
| --- | --- | --- |
| [Cursor Projects](https://cursor.com/blog/projects), 10 September 2026 | Persistent coordination, shared context and recurring work | Operator workflow baseline and the value of local control-plane ownership |
| [Cursor Self-Hosted Machines](https://prod.cursor.com/blog/self-hosted-machines), 2 September 2026 | User-hosted execution while coordination/planning/inference remain in Cursor's cloud | The original ownership distinction is supported; self-hosted execution alone is not a local control plane |
| [intentic](https://github.com/intentic/intentic) | Persistent agents on owned hardware, browser/phone operation, planning/review and automations | Closest broad workflow candidate: test durability, authority and integration boundaries |
| [Claudexor](https://github.com/razzant/claudexor) | Local orchestration, native harnesses, profiles and quota-aware rotation | Actual account pooling semantics, billing safety, task continuity and extension points |
| [Guild](https://github.com/mathomhaus/guild) | Local SQLite, shared memory/search and task coordination | Fit for an operator-facing application and durable execution rather than memory alone |
| [Gas Town](https://github.com/gastownhall/gastown) | Multi-agent coordination and persistent work tracking | Operational complexity and whether its architecture fits one operator's constrained workflow |
| [Kiro Specs](https://kiro.dev/docs/web/specs/) | Clarifying requirements and deriving implementation work | Baseline quality for intent/brief interaction; not evidence of Gaffer's required ownership model |
| [GitHub Spec Kit](https://github.com/github/spec-kit) | Specification, clarification, planning and task decomposition | Reusable intent workflow and evaluation baseline; not itself the entire Gaffer runtime |

Do not infer “missing” from an absent README paragraph. Mark a property unknown
until documentation, code inspection or a controlled test establishes it.

**M0 comparison rubric:** integration with the configured model gateway; local state ownership/export;
phone capture and correction; scope-bound authority; runner-local policy; tested
isolation; leases/reconciliation; exact-artifact review/publication; dependency
integration; backup/restore; licence and dependency compatibility; maintenance and
upgrade burden. Use pass/partial/fail/unknown with an evidence link and pinned version.

Choose the two closest candidates after a short source inspection and run the same
small capture-to-review fixture on each. Record the cost of adding missing workflow
versus owning foundational execution/security code. Adopt a tool outright if it
solves the need. Extend it if the missing pieces fit stable boundaries. Start a
new core only with a written explanation of the gaps and recurring maintenance cost.

**15 September decision update:** the
[foundation decision record](https://github.com/korallis/letmecook/blob/main/docs/decisions/0001-execution-foundation.md)
compares freshly pinned intentic and Claudexor on this rubric and selects a narrow
Go build, preserving 9Router ownership. It challenges the strongest alternative,
extending Claudexor's existing lifecycle, and records licence/dependency limits,
control gaps, costs, first adapter target and provisional-slice disposition.
This is a decision record, not a completed measured comparison or locked M0 exit.
Its 9Router selection is historical and does not establish CLIProxyAPI conformance.
Neither candidate was executed; paired workflow/effort results remain unknown.
Fresh Opus review, Docker-free #89 proof, live #6/#7/#8 acceptance and real #1
baseline evidence remain blocked. The failed live attempt remains failed; no
retry is authorized by the decision. Historical shortlist findings above retain
their 14 September source-only scope.

## 4. Provider and gateway feasibility

### Current configured gateway requirement

Gaffer now expects an operator-selected HTTPS model gateway endpoint, a protected
credential reference, one or more supported protocols and an exact model-ID/alias
allowlist. The current selection is CLIProxyAPI. On 17 September 2026, the
operator-configured endpoint answered `POST /v1/chat/completions`,
`POST /v1/responses`, `POST /v1/messages` and `GET /v1/models`. The host, URL and
credential are operator configuration and are not recorded. These observations do
not establish streaming, tools, structured output, fallback, cancellation,
status/usage or account behavior. See the [generic gateway contract](contracts/model-gateway.md).

### Historical 9Router gateway profile

The v0.4 baseline selected 9Router to centralize every agent's model access and
support several subscriptions from the same provider. At that time the requirement
was part of the architecture, not an optional optimisation. It simplified Gaffer:
one endpoint and named routes replaced per-agent provider authentication and a new
account router. That choice is no longer a generic product requirement.

A source inspection of the referenced fork at commit
`69724d86d4486fa5d722e63ebb6c9700e71aebff` confirms two separate implemented mechanisms:

- **Provider connection selection:** priority-based fill-first or sticky round-robin
  among eligible connections, with failure/cooldown handling. Separate subscriptions
  are separate connection records; they are not all one quota pool merely because
  they share a provider. [Connection selection](https://github.com/rickicode/9router/blob/69724d86d4486fa5d722e63ebb6c9700e71aebff/src/sse/services/auth.js),
  [request handling](https://github.com/rickicode/9router/blob/69724d86d4486fa5d722e63ebb6c9700e71aebff/src/sse/handlers/chat.js)
- **Named model combos:** ordered fallback or model round-robin, selected using the
  combo name as the requested model. This is distinct from choosing among accounts
  for one provider. [Combo implementation](https://github.com/rickicode/9router/blob/69724d86d4486fa5d722e63ebb6c9700e71aebff/open-sse/services/combo.js)

This is stronger evidence than a README feature claim, but it is still source
inspection, not an end-to-end test of the user's installed instance. The historical
M0 profile called for one real harness through a named route backed by two
subscriptions from one provider. That test would establish stream/tool behaviour
and verify fallback without Gaffer creating a duplicate task attempt.

### Historical thin integration, with one source of account configuration

The inspected 9Router fork implements Chat Completions, Messages and
Responses-compatible inference paths. Model discovery includes combos, but can
return models without an active
connection; discovery alone is not readiness. Use a bounded real request to probe
the selected route. [Model discovery](https://github.com/rickicode/9router/blob/69724d86d4486fa5d722e63ebb6c9700e71aebff/src/app/api/v1/models/route.js)

That historical design kept router/route references, task-linked usage and read-only
status views. For that profile, 9Router was authoritative for account credentials,
configuration, rotation, provider cooldowns and model fallback. Its management UI
owned account changes. Do not build competing login, subscription balancing or
refresh logic in Gaffer.

The dashboard routes provide concrete integration points, not a stable public
management contract. Pin their version and project only allowlisted status fields.
Per-connection usage retrieval can refresh credentials/update state; distinguish
snapshot reads from active refresh rather than polling everything indiscriminately.
[Usage endpoint](https://github.com/rickicode/9router/blob/69724d86d4486fa5d722e63ebb6c9700e71aebff/src/app/api/usage/%5BconnectionId%5D/route.js)

The original hard-coded SQLite path should still be removed: the referenced
[README](https://github.com/rickicode/9router) describes a different storage layout,
and private storage is not the right API contract regardless of backend. This is a
reason to integrate any gateway through an adapter, not through its private storage.

### Concrete integration boundaries from source

The inspected inference handler makes API-key enforcement configurable and does
not implement per-key combo/model scope. Enable authentication and use a narrow
trusted forwarding boundary to bind attempts to permitted routes where needed.
It validates authority and request limits; it does not select accounts or retry
provider requests. [Inference handler](https://github.com/rickicode/9router/blob/69724d86d4486fa5d722e63ebb6c9700e71aebff/src/sse/handlers/chat.js)

Do not mirror raw management responses. Provider responses remove certain top-level
secrets but retain provider-specific data that can include nested tokens; usage
aggregates can include raw router API keys. Return a deliberately small status
schema and keep these responses out of agent context, UI payloads and logs.
[Provider response](https://github.com/rickicode/9router/blob/69724d86d4486fa5d722e63ebb6c9700e71aebff/src/app/api/providers/route.js),
[token storage](https://github.com/rickicode/9router/blob/69724d86d4486fa5d722e63ebb6c9700e71aebff/src/sse/services/tokenRefresh.js),
[usage aggregation](https://github.com/rickicode/9router/blob/69724d86d4486fa5d722e63ebb6c9700e71aebff/src/lib/usageDb.js)

Inspected application guards cover selected routes, not every provider/combo/usage
management path. Keep the router management plane on a private, authenticated,
path/method-restricted boundary. Workers reach only approved inference endpoints.
These findings are pinned to the inspected fork/version and must be rechecked for
the deployed build. [Proxy matcher](https://github.com/rickicode/9router/blob/69724d86d4486fa5d722e63ebb6c9700e71aebff/src/proxy.js),
[dashboard guard](https://github.com/rickicode/9router/blob/69724d86d4486fa5d722e63ebb6c9700e71aebff/src/dashboardGuard.js)

### Harness and provider compatibility

Keep the documented harness execution interfaces: Codex structured events/resume
and Claude print/streaming modes. Configure their inference endpoints through the
trusted boundary to the selected gateway, and test the full protocol path. Gaffer's
planner calls an allowed model ID or alias through the compatible API; workers and
planners receive neither a gateway credential nor a separate native provider login.
[Codex noninteractive mode](https://learn.chatgpt.com/docs/non-interactive-mode),
[Claude programmatic execution](https://code.claude.com/docs/en/headless)

Technical compatibility and provider authentication/billing terms remain separate
facts. The configured gateway owns provider credentials, accounts/subscriptions,
rotation, cooldown and request fallback; Gaffer does not collect those credentials.
Verify each selected model target against its current provider agreement without
building that provider's auth lifecycle again in Gaffer. The retained 9Router
findings apply only when that historical gateway profile is selected.
[Codex authentication](https://learn.chatgpt.com/docs/auth),
[Claude credential guidance](https://code.claude.com/docs/en/legal-and-compliance)

The earlier research correction still applies: Claude's plan article has a June 15
update pausing the announced SDK-credit change, while lower sections preserve the
superseded proposal. Do not hard-code that old credit model into usage estimates.
[Claude plan update](https://support.claude.com/en/articles/15036540-use-the-claude-agent-sdk-with-your-claude-plan)

### Failure and budget responsibilities

The configured gateway handles provider-account selection, rotation, cooldown and
request fallback within the authorised provider/model/billing set. If it succeeds,
the Gaffer attempt continues. If the model target or gateway is unavailable, or a
partial stream cannot safely recover, Gaffer preserves work, parks or reconciles the
attempt and applies its task retry policy. Gateway downtime must not silently switch
agents back to direct provider access.

Unknown headroom remains unknown, but it does not require rebuilding a quota system.
Use safe gateway observations where available, bounded task admission and explicit
paid fallback policy. Keep gateway request retries and Gaffer task retries separately
bounded. The integration contract and owner table are in
[spec section 8](spec.md#8-model-gateway-integration-and-task-capacity). The retained
9Router source evidence demonstrates only that historical gateway profile.

## 5. Engineering evidence and its limits

### Persistence is explicit state, not one immortal conversation

Anthropic's long-running-agent engineering describes incremental sessions and
progress artifacts as a useful approach. This supports a baseline of durable briefs,
checkpoints and verified progress. It does not prove automatic curation stays true
for months. [Effective harnesses for long-running agents](https://www.anthropic.com/engineering/effective-harnesses-for-long-running-agents)

**Our design inference:** make the workflow database and immutable artifacts the
recovery contract. Native transcripts are useful evidence and optional resume
inputs, but not an exactly-once execution mechanism.

### Multi-agent value depends on task shape

Anthropic's multi-agent research engineering describes benefits from independent
parallel work and substantial resource overhead. Its findings concern a research
system, so they cannot be transferred as a coding productivity or cost multiplier.
[Multi-agent research system](https://www.anthropic.com/engineering/multi-agent-research-system)

**Our design inference:** compare a durable single worker against delegation before
building a fleet. Preserve sequential mode for tightly coupled code changes and
compare outcomes after integration, not independent patch production.

### Isolation and recovery require mechanisms

Git explicitly documents shared administration among worktrees. This is sufficient
to reject worktrees as a host security boundary.
[Git worktrees](https://git-scm.com/docs/git-worktree)

Claude's sandbox documentation describes OS-backed filesystem/network controls and
configuration that affects escape/fallback behaviour. The lesson is to verify the
actual boundary, not assume a prompt or a denylist supplies it.
[Claude sandboxing](https://code.claude.com/docs/en/sandboxing)

SQLite documents WAL behaviour and consistent online backup. These support a local
embedded database, but not the old promise that copying one live file is a complete
backup. Gaffer's artifact/context consistency and paused restore are additional
application responsibilities. [SQLite WAL](https://sqlite.org/wal.html),
[SQLite backup API](https://www.sqlite.org/backup.html)

**Our design inference:** leases, fencing, a transactional outbox, local journals
and idempotent reconciliation belong in the first runnable core. None of the source
material independently validates Gaffer's proposed lease protocol; fault injection
must do that.

### Token and tool benchmarks are prompts to measure

[TOON's own documentation](https://github.com/toon-format/toon) describes advantages
for uniform data and tradeoffs for nested/nonuniform structures. It does not justify
re-encoding source code, diffs or provider tool schemas.

[AXI's benchmarks](https://github.com/kunchenguid/axi) concern particular GitHub and
browser task sets with a specified model. They support compact schemas and efficient
retrieval; they do not isolate every saving to encoding or establish Gaffer's
end-to-end cost. The original “roughly four agents versus eight” extrapolation is
unsupported.

**Our design inference:** JSON contracts and compact summaries first. Compare
TOON on representative repetitive tables, measuring fidelity, tool success,
latency, cache behaviour and total tokens per accepted outcome. Do not impose a
no-regression token gate that blocks a correctness improvement without review.

### A phone app needs a real network and session design

Service workers require secure contexts; private HTTP on a remote phone is not the
same as localhost development. Offline draft capture is distinct from a successful
server-side approval or stop. [MDN service worker API](https://developer.mozilla.org/en-US/docs/Web/API/Service_Worker_API)

GitHub's webhook guidance covers secrets/signatures, identifiers and processing
practices. Public service delivery still needs reachable ingress. Schedules and
outbound polling test recurring work without making public ingress part of the
first deployment. [GitHub webhook guidance](https://docs.github.com/en/webhooks/using-webhooks/best-practices-for-using-webhooks)

## 6. How to test the product hypothesis

Use your own representative work, but keep development examples separate from
held-out evaluation. Label the intended outcome and acceptable scope before seeing
the system's brief. Include small bug fixes, ambiguous cleanup, multi-session
features, dependency changes and multi-step migrations.

Run the following paired comparisons on pinned starting commits:

| Comparison | Question |
| --- | --- |
| Standalone harness with the same configured gateway model targets and good brief/progress notes vs. Gaffer single worker | Does the operator workflow itself add value? |
| Gaffer single worker vs. delegated workers | Does parallelism save operator time after coordination and integration? |
| Explicit approved memory vs. no supplied memory | Does memory reduce rediscovery without causing stale-instruction errors? |
| Simple brief editor vs. model clarification | Does inference improve delivered intent rather than just produce agreeable prose? |
| Compact JSON vs. TOON on suitable payloads | Do format changes improve total task efficiency without reducing correctness? |

Alternate/randomise execution order and repeat where practical. Keep the gateway build, protocols/model targets,
permissions, repository fixtures and task classes comparable; record any difference
instead of attributing it to Gaffer. Use human criteria review and objective checks;
a model grader cannot be the only judge of the central intent claim.

Record active operator minutes, accepted result, scope breaches, verification
failures, follow-up defects/reverts, total model usage, cached usage when known,
incremental cash, retries, elapsed time and blocking reason. Include unsuccessful
runs in the analysis. Report medians, spread, sample counts and limits; a 20-task
pilot is useful direction, not statistical proof for all software work.

The [PRD metrics](PRD.md#6-validation-and-success-measures) provide provisional
gates. “At least 24/30 faithful feasible-task outcomes, including bounded retries” and
“20% lower median operator effort” are
chosen pilot targets, not externally established thresholds. Zero unauthorised
external effects and zero lost acknowledged results are release blockers in the
defined test suite, not a claim that exhaustive correctness has been proven.

Count economics in two ways: marginal cash added to accounts you already pay for,
and fully allocated ownership cost including subscriptions, hardware/electricity and
operator time. A monthly plan is not free inference; token counts are not necessarily
a reliable measure of its remaining allowance.

## 7. Recommended decisions and outstanding uncertainty

| Decision | Recommendation now | Revisit when |
| --- | --- | --- |
| User | One solo operator who accepts configured cloud model processing | A real second-user requirement appears |
| Value | Intent-to-acceptance continuity and less operator effort | Pilot fails to improve the standalone-harness baseline |
| Core language | See the [recorded application target](https://github.com/korallis/letmecook/blob/main/docs/decisions/0001-execution-foundation.md#first-harness-and-runtime-target-versus-support) | M0 packaging/reuse evidence favours another choice |
| First release | Sequential durable loop plus online phone workflow | M1–M2 gates pass |
| Code review | Gaffer evidence summary, GitHub review/check authority | Demonstrated need for an in-app editing workflow |
| Memory | Reviewed files and explicit progress | Retrieval evaluation justifies extra automation |
| Model routing | One operator-configured model gateway is required from M0; current selection CLIProxyAPI; all roles use its scoped boundary | Validate exact protocol/model paths and extend only necessary status/policy interfaces; never add account routing or a quota ledger to Gaffer |
| Fleet | Small eligible pool with single integration writer | Paired task evidence supports more concurrency |
| Production hosts | Excluded from initial supported execution | Separate deployment/threat review and explicit user need |
| Release timing | See the [effort record and conditional estimates](https://github.com/korallis/letmecook/blob/main/docs/decisions/0001-execution-foundation.md#effort-record-and-estimates); gates before dates | M0 yields observed implementation effort |

Unknowns remain real: which Docker-free harness/configured-gateway/isolation
combination passes on your hardware, the measured cost of extending either candidate versus
the selected narrow build, how much the brief workflow improves your work, and
what level of concurrency your actual accounts sustain.
The choice of one shared model gateway is settled; CLIProxyAPI is the current
selection. Its deeper conformance remains an integration experiment, while 9Router
contracts and observations remain historical gateway-specific evidence.

## 8. Documentation maintenance

All 28 original story IDs are retained with revised acceptance and release scope.
The README now distinguishes ownership from inference; the PRD states observable
behaviour; the spec defines mechanisms; the roadmap orders dependencies; this file
records evidence and decision rationale. Markdown is authoritative and the HTML
reader is reproducibly generated.

Future changes should update the requirement, mechanism and milestone together.
Record the date/version of any provider or competitor claim. A successful unit
test, a model's self-report or a marketing page must not silently become a claim
of verified end-to-end product behaviour.
