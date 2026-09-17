# Gaffer

**Turn an idea into reviewed software, and keep the work moving on machines you own.**

Gaffer is a proposed self-hosted control plane for coding agents. You capture an
idea from your phone or laptop, correct a brief that states the intended outcome,
and approve a bounded plan. Gaffer runs that work through supported agent harnesses,
retains decisions and progress, and returns changes with verification evidence.
An always-on machine holds the project when your laptop is closed.

**An operator-configured model gateway is the shared model-access boundary from
the first release; the current selection is CLIProxyAPI.** All agent harnesses and
Gaffer's own planning calls route through its configured HTTPS base URL. The gateway
owns provider credentials, accounts and subscriptions, rotation, cooldown and
request fallback; Gaffer coordinates tasks above that layer without becoming a
second account router or quota ledger. See the [model gateway contract](contracts/model-gateway.md).

The alpha design includes **task-aware model selection**. Gaffer assesses the
actual work, filters gateway model IDs or aliases by permission and verified
capabilities, and explains which eligible target fits. Operator preferences guide
the choice; request fallback remains in the configured gateway. See the
[selection contract](https://github.com/korallis/letmecook/blob/main/docs/contracts/task-routing.md)
for bounded assessment, uncertainty, override and independent review requirements.

The thing worth building is continuity between **what you meant, what you authorised,
what ran, and what you accepted**. More agents and more tokens are means to that end.

> Gaffer means the foreman on a building site. The name is provisional.

## Status and reading order

**Revised planning baseline v0.4 · 17 September 2026.**

For current implementation status, see the
[project README](https://github.com/korallis/letmecook/blob/main/README.md).

These documents describe intended behaviour, not implemented or verified features.
The revisions preserve the original product intent while changing scope, execution
semantics and evidence requirements. Start with the evaluation to see what was
challenged and why.

| Document | Purpose |
| --- | --- |
| [Evaluation and research](evaluation.md) | Findings, primary sources, decisions, remaining uncertainty and build-versus-reuse gate |
| [Product requirements](PRD.md) | Audience, end-to-end journey, release scope and measurable acceptance |
| [Technical specification](spec.md) | Authority, task lifecycle, isolation, storage, adapters, delivery and failure handling |
| [Roadmap](roadmap.md) | Dependency-ordered milestones and tests that determine whether to continue |
| [Model gateway contract](contracts/model-gateway.md) | Generic endpoint, credential-reference and inference-boundary requirements |

Open [the documentation reader](index.html) for all six documents. Markdown is the
source of truth; the HTML is generated from it. Maintainers can rebuild and verify
it with Node.js 24 or newer:

```sh
npm --prefix docs ci
npm --prefix docs run build
npm --prefix docs run check
```

The documentation tooling is separate from Gaffer's proposed runtime.
The build layout lives in `reader-template.html.in`; `index.html` is the reader.
The old `reader-template.html` address redirects to the reader.

Development is tracked in the public [korallis/letmecook repository](https://github.com/korallis/letmecook).
The [GitHub issues](https://github.com/korallis/letmecook/issues) divide specification,
implementation and validation into one-PR outcomes. Use the
[implementation index](https://github.com/korallis/letmecook/blob/main/docs/implementation-plan.md)
to navigate dependencies, and the
[Astra playbook](https://github.com/korallis/letmecook/blob/main/docs/astra-playbook.md)
for issue execution instructions.

## What you own

You own the orchestration process, task records, context files, execution machines
and the configured model gateway. Gaffer stores its endpoint and a protected credential
reference, not provider or gateway credential values. The gateway owns provider
login, accounts and subscriptions, refresh, rotation, cooldown and request-level
fallback; Gaffer does not duplicate those functions.

You choose the hosts for the daemon, each runner and model gateway. They can share a
suitable machine or use separate machines. Register and explicitly enable eligible
runners, then choose which projects they may execute; discovery of a machine never
enrolls it or grants execution authority. A maintainer's test machine is only one
optional environment, with no built-in hostname, address or account dependency.

Configure an operator-selected HTTPS model gateway endpoint, the protocols and
model IDs or aliases it may serve, and a credential reference. Gateway credentials
and provider sessions stay inside its own trust boundary and are never bundled
with Gaffer. Gateway-specific setup and migration remain operator concerns. Tailscale
is one optional private networking choice; deployments may
use another tested transport satisfying the same authentication and TLS contract.
See [deployment choices and migration boundaries](spec.md#12-operator-selected-deployment).

Cloud harnesses still send prompts, code and tool results through the configured
gateway to model providers. Self-hosted orchestration does not mean local inference,
air-gapped operation or independence from provider policy. Workers never receive
the gateway credential: a trusted per-attempt boundary injects it and pins permitted
protocols and models. Subscription and paid-API routing policy stays with the gateway; missing status
or usage signals remain explicit unknowns in Gaffer.

## Why build this

Persistent coordination, specifications and quota-aware tools already exist.
[Cursor Projects](https://cursor.com/blog/projects) describes persistent coordination;
[Kiro Specs](https://kiro.dev/docs/web/specs/) addresses requirements clarification;
open-source tools overlap with local execution and scheduling. Gaffer must earn its
place through the complete operator workflow, rather than a claim that these ideas
are new. The [comparison and reuse gate](evaluation.md#3-build-or-reuse-before-building)
comes before substantial infrastructure work.

The test is practical: does Gaffer deliver more accepted work with less of your
attention than a well-configured standalone harness using the same model gateway,
model targets, good brief and progress notes?

## First useful release

One operator, one repository, one explicitly enabled runner, one supported coding
harness, and sequential tasks, using the configured model gateway from day one.
The current selection is CLIProxyAPI; gateway-specific account and fallback
behaviour must be proven before Gaffer relies on it. A responsive web application
provides capture,
brief-and-plan approval, status, review evidence and stop. The foundation includes
durable attempts, bounded retries, local memory files and explicit publication.

A runner may share the always-on machine with the daemon, but project code runs in
an isolated execution environment. Client and production hosts are excluded from
the initial supported deployment.

The later product adds a second harness, multiple eligible machines, bounded
parallelism, task scheduling informed by gateway observations where available
and recurring work. Automated memory
curation and auto-merge must prove value before becoming release
requirements.

## Shape of the system

```text
Phone / laptop over authenticated private HTTPS
  → capture → bounded discovery → brief + plan approval
  → durable scheduler → explicitly eligible runner → harness
  → preserved artifact → verification → review
  → separately authorised publication → GitHub checks and merge policy

SQLite: workflow state, authority, events and artifact references
Git context: reviewed knowledge and decision history
Runner journal: active attempts, unsent output and recoverable local work

All model calls: planner / reviewer / worker harness
  → trusted per-attempt inference boundary → configured gateway model ID or alias
Model gateway: provider credentials, accounts/subscriptions, rotation, cooldown, fallback
Gaffer: task dependencies, runner placement, authority, task budgets and recovery
```

## Product commitments

1. Material ambiguity is visible before code changes begin.
2. Authority is enforced by application code and runner policy, not inferred by a model.
3. Losing a connection does not silently authorise a second active writer.
4. Placement never expands beyond the machines and providers the operator permitted.
5. Memory remains inspectable, attributable and correctable.
6. Unknown quota is shown as unknown; spending and retries are bounded.
7. Completion means checked evidence against the agreed outcome, not an agent saying “done”.

## Licence

MIT is intended for original Gaffer code and documentation, but no licence has yet
been applied. Reuse candidates require their own licence and dependency review.
