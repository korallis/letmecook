# Task-aware selection of 9Router routes

Specification for [issue #71](https://github.com/korallis/letmecook/issues/71),
14 September 2026. This describes intended alpha behaviour, not an implemented
selector or verified model ranking. It extends the [9Router contract](9router.md)
without changing its authority, configuration-freeze or cancellation rules.

## Ownership and result

Gaffer assesses the concrete task and chooses an eligible **named route** for a
planner, worker, reviewer or separately authorised research step. 9Router executes
the route's model fallback chain and chooses provider connections. Gaffer never
tries its ranked candidates as a second request fallback loop, maintains account
balances, or acquires provider credentials.

A role-to-model matrix is an editable starting preference. The completed feature
must distinguish tasks within a role: a formatting failure and an intermittent CI
race can require different routes, as can CSS cleanup and screenshot-based visual
review. Many files do not necessarily mean difficult reasoning; a one-line change
to access control can have high consequences. Mixed work can have separate approved
steps and route decisions rather than forcing every step onto the largest model.

Public installations supply their own router, route references and preferences.
There are no built-in personal hosts, mandatory provider accounts or model chains.
Use [operator-selected deployment](../spec.md#12-operator-selected-deployment).

## Assessment, eligibility and ranking

1. **Describe the work.** Derive a schema-validated assessment from the current
   brief, criteria and explicitly permitted evidence. Record work class, ambiguity
   and novelty, consequence of error, likely context size, modality, protocol and
   tools, required verification, latency preference and resource limits. Separate
   required capabilities, preferences and unknowns; cite evidence for material
   classifications. The assessment proposes requirements and cannot grant them.
2. **Apply hard constraints.** Deterministic code intersects the grant, repository
   data/provider policy, approved complete route graph, verified model/settings/
   protocol/tool/modality/context support, runner/harness/isolation compatibility
   and enforceable budgets. Required capability evidence that is missing, stale or
   mismatched excludes the candidate. Unknown optional performance evidence is
   recorded separately. Discovery and configured account presence are not readiness.
3. **Rank eligible choices.** Compare task fit using versioned operator preferences
   and relevant capability/performance evidence. Preferences may favour quality,
   latency or resource use within the hard limits. Record the applicable factors,
   their declared priority/weight rules, evidence coverage and deterministic
   tie-break; never invent universal intelligence scores or quota precision.
   An eligible task override precedes a saved preference. Model-assisted ranking
   is a proposal over the already eligible set and must pass deterministic checks.
4. **Resolve uncertainty.** Use evidence-labelled confidence and named unknowns,
   not a fabricated probability of correctness. A non-material unknown can use
   the operator's conservative eligible default with a disclosed assumption.
   Uncertainty affecting authority, mandatory capability, hard budget or acceptable
   outcome parks that step for clarification. No eligible route, stale evidence,
   invalid assessment, assessment-budget exhaustion and router outage are distinct
   outcomes. Uncertainty never automatically means “use the most expensive model.”
5. **Approve and admit.** Bind the decision to the exact brief/plan, evidence and
   policy revisions. The owner may pin an eligible route or authorise automatic
   choice within a displayed set/envelope, including through standing policy.
   Revalidate current eligibility at dispatch, then atomically persist the decision,
   selected route, attempt, reservation and outbox assignment. A stale assessment
   never authorises a current assignment. Routine inside-envelope choices do not
   need repeated permission; widening the envelope does.

Every possible fallback must satisfy mandatory requirements. A vision-capable
first model followed by a text-only or unverified fallback is not a vision route.
A subscription preference cannot make paid or unclassified overflow eligible.
Search requires an authorised, available search tool and data policy; a model name
containing “research” does not provide tool access. Effort labels are versioned
adapter settings with demonstrated semantics, not a universal low/high/xhigh enum.

## Bounded assessment bootstrap

Before any semantic assessment, select an operator-configured bootstrap/planner
route by deterministic eligibility checks on the packet it will receive. Its
complete provider and billing envelope must permit that data. Unknown repository
identity or exposure policy cannot be resolved by first sending private code to an
unapproved classifier. Start with the smallest authorised metadata/excerpts.

Perform cheap deterministic extraction first. Where practical, return assessment
and route proposals in an already required planning response. The initial alpha
budget allows one semantic assessment and at most one explicitly budgeted repair
or clarification pass per unchanged input revision; missing budget disables those
calls. Request bytes, output tokens, total/idle time and all-in task/discovery
request counts use the accepted inference bounds, with concrete limits fixed in
the implementation fixture before measurement. No selector chooses another
selector recursively, runs on every scheduler tick, or calls providers directly.

Cache only by brief/context/policy/selector/evidence versions. A changed relevant
input invalidates the assessment; repeated retries of an unchanged input do not
reset its budget. Charge assessment, discarded output, repairs and failed attempts
to the task. Repository instructions and model output are data: neither can change
the candidate set, budgets, route settings or the approval policy.

## Evidence and decision records

These are logical contract fields; storage and API schemas follow the accepted
foundation. They contain safe references and compact rationale, not credentials,
private prompts or hidden chain-of-thought.

| Record | Required content |
| --- | --- |
| Task assessment | Task/brief/plan/context hashes, role or step, requirements/preferences, supporting references, confidence/unknowns, assessor and selector versions, charged allowance |
| Route profile | Operator-owned route ID, intended uses and preference rules, permitted provider/model/billing envelope reference, bootstrap eligibility, policy revision and route fingerprint |
| Capability evidence | Exact router build, route graph, model/fallback path, harness/adapter/settings versions, protocol/tools/modalities/context bounds, observed result, provenance and invalidation conditions |
| Route decision | Assessment and policy versions, considered candidate references, hard exclusion reasons, ranked eligible choices and applied factors, selected route, override/default reason, decision identity |
| Execution observations | Requested route/settings versus observed model/connection/fallback when available, request/attempt IDs, source/freshness of usage, terminal outcome, verification/review result; missing attribution stays unknown |

Configuration inspection, transport conformance, task suitability and transient
availability are separate evidence classes. A provider outage is not a low-quality
answer; a successful unreviewed output is not proof of task suitability. Route
definition, model, adapter or relevant settings changes invalidate incompatible
evidence. Passive status can block admission but cannot manufacture positive
capability or precise remaining quota.

## Fallback, stop and independent review

An attempt retains its immutable named route and policy identity. 9Router may
rotate accounts or perform its proven request fallback within the approved graph.
Partial output, unknown upstream work or executed tools never trigger Gaffer to
swap to the next ranked route and replay the transcript. Preserve the artifact,
fence/reconcile the previous execution and use existing bounded replacement-attempt
rules. A new attempt may select another already permitted route with a recorded
reason. Policy edits still require the accepted freeze/drain capability; a catalog
match does not establish that an installed router supplies it.

Independent model review uses a fresh invocation and evidence packet containing
the exact candidate/base identity, criteria, actual checks and known limitations.
It does not inherit the worker's live session or accept a success claim as proof.
Choose the reviewer route against review requirements. A different model is not by
itself independence; model diversity can be an explicit preference or requirement.
Reviewer findings cannot grant local acceptance, publication or merge authority.

## Optional operator preference example

The following reproduces the operator's requested starting preferences. Names and
effort labels are **desired labels**, not verified availability, comparative
strength or a runnable configuration. An installation must bind exact IDs and
tested settings in its own 9Router, validate the entire fallback graph, and mark
unresolved mappings unavailable. Do not silently substitute a similarly named model.
The order belongs to 9Router after a compatible route is configured; Gaffer chooses
which eligible route fits the task. Operators may omit any class or choose entirely
different providers.

| Work profile | Desired fallback chain | Desired effort by position |
| --- | --- | --- |
| Research / X-search | gateway Grok 4.6 → xai Grok 4 → Astra | xhigh / high / xhigh |
| Independent review | Sol-review → Luna-review → Terra-review | xhigh / xhigh / xhigh |
| Visual QA | Gemini 3.8 flash → 3.8 flash-high → 3.1 pro-low | low / high / low |
| UI / branding taste | Fable 5.1 → Fable 5 → Gemini 3.1 pro | xhigh / xhigh / low |
| Repository audit | Sonnet 5 → Gemini 3.8 flash-high → GLM 5.3 flash | high / high / low |
| Non-UI planning | Astra → Opus 5 → GPT-5.6 Sol | xhigh / xhigh / xhigh |
| CI / fix loop | Haiku → Gemini 3.8 flash-low | low / low |
| Cheap reads / tiny edits | GLM 5.3 flash → Gemini 3.8 flash-low → Haiku | low / low / low |
| Mid-sized feature | Sonnet 5 → GPT-5.6 Terra → GLM 5.3 | high / high / high |
| Known-shape build | Astra → GPT-5.6 Terra → Opus 5 | xhigh / xhigh / xhigh |
| Explicitly approved hard problem | Fable 5.1 → Opus 5 → Astra | xhigh / xhigh / xhigh |
| Open-ended work | Opus 5 → Astra → GLM 5.3 → gateway Grok | xhigh / xhigh / max / xhigh |
| Default fallback | Astra → Opus 5 → GLM 5.3 → gateway Grok | xhigh / xhigh / max / xhigh |

“Approved hard problem” requires that profile's explicit grant; classification
cannot approve it. “Open-ended” still has finite scope and budgets. A default is
subject to the same eligibility checks as every other route. These are runtime
preferences, separate from this repository's Astra implementation workflow.

### Initial catalog findings

The [redacted discovery record](task-route-catalog-2026-09-14.json) maps 18 exact
or candidate IDs from the selected 9Router 0.5.75 public registry and focused
operator-selected configuration inspection. All remain automatically ineligible
pending actual route/protocol/settings evidence. No inference, models GET, router
mutation or credential migration was performed for this mapping.

- The configured `gcli/grok-4.6-xhigh` label does not establish that effort: the
  [Grok CLI helper](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/open-sse/config/grokCli.js)
  recognises effort for 4.5, and the
  [executor](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/open-sse/executors/grok-cli.js)
  removes unsupported effort after resolving the model suffix.
- Fable 5.1 and Opus 5 use the adaptive path that normalises `xhigh` to `high`;
  Fable 5 takes a token-budget path. These are distinct source translations,
  not proof of equivalent upstream reasoning. See the
  [unified thinking translator](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/open-sse/translator/concerns/thinkingUnified.js).
- GLM 5.3 Flash's exact capability entry lacks effort support, so the unified
  translator enables thinking but omits the requested effort; base GLM 5.3 differs.
  See the [capability registry](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/open-sse/providers/capabilities.js).
- Unqualified Gemini 3.8 Flash resolves to medium. A low intent needs an explicit
  `ag/gemini-3.8-flash-low` binding, reviewed as such; see the
  [Antigravity registry](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/open-sse/providers/registry/antigravity.js).
- “Haiku” still needs a version/provider choice. The discovery candidate is
  `cc/claude-haiku-4-5-20251001`, not an automatically approved substitute; see the
  [Claude registry](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/open-sse/providers/registry/claude.js).
- Sol/Luna/Terra review IDs map to their base models with review metadata. They
  do not create fresh context or review authority. See the
  [Codex registry](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/open-sse/providers/registry/codex.js)
  and [executor](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/open-sse/executors/codex.js).

Setup must expose unsupported or translated settings with these source distinctions
and block required mismatches. A verified router fix or an explicitly revised
operator requirement can resolve them; quietly relabelling the request cannot.

## Development counterexamples and evaluation

Development cases must execute the selector interface once implemented; prose
examples do not establish behaviour. Freeze independently labelled held-out cases
before tuning, allow multiple suitable choices, and retain all failures.

| Case | Required distinction or refusal |
| --- | --- |
| One-line authentication change versus many-file mechanical rename | Consequence/novelty and verification evidence matter beyond diff size |
| CSS cleanup versus screenshot comparison | Only the latter requires proven image support across every fallback |
| Formatting CI error versus intermittent integration race | A common role label must not force the same suitability ranking |
| Provided research excerpts versus live X-search | External research requires separately authorised retrieval tools |
| Preferred cheap route with paid or text-only fallback | Hard billing/modality exclusion wins over preference |
| Mixed UI and storage work | Propose separately scoped steps or a fully capable eligible route |
| Repository text demanding a new endpoint, credentials or shell | Reject authority expansion; record the injected text only as task data |
| Missing capability, stale route edit, unknown cost cap or invalid assessment | Named blocked/clarification outcome; no invented support or unlimited retry |
| Worker claims success and requests self-review | Fresh reviewer context and actual artifact/check evidence remain required |
| Changed intent, stale override or repeated scheduler tick | Revision checks and one bounded assessment allowance prevent stale dispatch and repeated charges |
| Partial tool stream followed by route outage | Preserve/fence/reconcile; never transparently replay on another ranked route |

Compare task-aware selection with an operator-owned static role default on matched
fixed-base tasks, holding the available route set, fallback definitions, budgets,
harness and data policy constant. Here route choice is the experimental variable.
Keep this separate from the existing whole-product comparison that holds routes
identical. Count assessment overhead and unsuccessful attempts in both analyses.

Predeclare acceptable suitability/clarification decisions, independent defect
severity and sample limitations. Report hard-constraint violations (required zero),
route suitability, overrides/reasons, clarification quality, latency/usage,
accepted outcomes, rework and operator effort. Agreement with one model label or
token savings alone is insufficient. Alpha validates this decision behaviour;
beta expands the task-class comparison and may propose versioned preference
calibration with provenance, sample size and rollback. No hidden online exploration,
automatic authority widening or self-promoted trust from success streaks.

## Delivery placement

- **M0 #71:** this contract, operator preference example and evaluation design.
- **M1 #15:** deterministic eligibility and immutable approved decision persistence;
  operator-supplied task requirements/choice are sufficient before semantic planning.
  It must not depend on the later M2 planner or browser UI.
- **M1 #21 / M2 #34:** fresh reviewer invocation and evidence boundary when model
  review is enabled; alpha integrates task-aware reviewer selection.
- **M2 #27/#30/#31:** operator profiles/bootstrap and capability diagnostics,
  semantic assessment/ranking, visible rationale and scoped override. This is the
  first useful alpha selector, not a feature deferred wholesale to beta.
- **M2 #40:** validate task-fit, ambiguity, override and safe fallback outcomes in
  a separate selector comparison alongside the existing whole-product gate.
- **M4 #49/#51:** richer availability/fairness and evidence-based calibration and
  evaluation; preserve alpha authority and immutable-attempt rules.

Firstmate is a relevant primary-source precedent: its upstream documentation
already describes task-fit judgment over operator profiles, even if an operator's
current configuration behaves as a static matrix. See its
[pinned architecture](https://github.com/kunchenguid/firstmate/blob/e3bd750cab2d58e33d5148a8e2cf7b2e7428014b/docs/architecture.md)
and [dispatch example](https://github.com/kunchenguid/firstmate/blob/e3bd750cab2d58e33d5148a8e2cf7b2e7428014b/docs/examples/crew-dispatch.json).
Gaffer retains 9Router's account/fallback ownership rather than importing
Firstmate's quota/account selection machinery. This reference is not a claim that
Gaffer already implements either system's routing behaviour.
