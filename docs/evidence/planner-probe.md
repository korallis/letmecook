# Restricted planning: synthetic contract passes, live gate blocked

For [issue #7](https://github.com/korallis/letmecook/issues/7). This M0 experiment
demonstrates schema validation, restricted discovery and bounded coordinator
inference over the accepted attempt-scoped boundary. The [recorded run](planner-probe-run.json)
contains actual automated observations from synthetic HTTP/SSE integration on
14 September 2026. It is **not live 9Router or provider evidence**. #7 remains
incomplete and its PR remains a draft pending the mandatory live criteria.

The branch includes accepted #2/#3/#4/#5/#71 outcomes. This experiment does not
modify the inference boundary or selected Linux profile, create the durable Go
application core, select the worker harness, or implement #30's alpha task-aware
selector. The coordinator runs as trusted host code with typed read dispatch;
it does not execute repository code and therefore does not claim a new OS profile.
The selected [Linux execution proof](linux-profile.md) remains a prerequisite for
unattended repository execution, not evidence that this planner ran inside it.

## Reproduce

```sh
npm ci --prefix experiments/planner-probe
npm --prefix experiments/planner-probe run check
npm --prefix experiments/planner-probe test
npm --prefix experiments/planner-probe run demo
npm --prefix experiments/planner-probe run prove
node experiments/planner-probe/capabilities.ts
```

The final command exits 2 with `blocked_missing_live_boundary` and performs no
network call. `prove` records test names/results, code/schema/fixture digests,
Node/platform identity, redacted boundary attribution and the public toy proposal.
It excludes raw assertion errors, request headers, private paths and credentials.
Its successful result is `synthetic_planner_passed_live_blocked`, with
`issueComplete:false` and `unattendedSupported:false`. The synthetic router serves
scripted responses; a correct plan in this record is not a measurement of model
quality or resistance to prompt injection. All task-owned sockets/state are removed.

Pinned dependencies: Ajv 8.20.0, TypeScript 5.9.3 and `@types/node` 24.3.0, with
the exact dependency graph in the package lock. Observed host Node was 26.8.2.
CI repeats typecheck/tests/proof on Node 24 and Linux; its observed result belongs
to the PR checks, not this local record.

## Implemented restriction and bounds

The host constructs an immutable input revision from the brief, pinned public-file
metadata, complete synthetic route policy, schema digest, selector version and
budgets. The only bootstrap is `gaffer-planner-fixture` under the accepted
`chat-text-tools-v1` profile. Repository content is a labelled tool-result string;
it cannot add system messages, route candidates, model settings, budgets or tools.
There is no shell, Git, dependency installation, hook, write, network retrieval,
delivery, approval or merge dispatch function available to model output.

The [plan JSON Schema](../../tests/fixtures/planner/plan.schema.json) closes every
object to additional fields. Plans contain outcome, constraints, exclusions,
criteria, proposed paths/systems, assumptions, up to three questions, steps and a
small evidence-labelled assessment. Deterministic checks also enforce the input
revision, unique criterion/step IDs, existing criterion references and evidence
actually delivered. The only proposed path is `fixture.txt`; allowed systems are
empty. These fields are proposals; no grant or worker task is created. Free-text
descriptions remain untrusted even when their enclosing JSON is valid.

| Bound | Fixed fixture maximum |
| --- | --- |
| Trusted snapshot acquisition | One exact regular file, 4,096 bytes; 1 s abort signal; known SHA-256 required |
| Model discovery delivery | One `read_file` call from memory, 4,096 bytes total; actual fixture is 484 bytes |
| Semantic assessment / repair | One assessment and at most one completed-output repair per session/input revision |
| All inference requests | Three total, including tool continuation, discarded output, repair and failures; concurrency one |
| Request / response | 32 KiB each per request; at most 96 KiB each over the three-request allowance |
| Output tokens | `max_completion_tokens:1024` per request; synthetic fixture does not prove provider token accounting |
| Planning duration | 5 s across the assessment, tool delivery and repair |
| Boundary request / first semantic output / semantic idle | 4 s / 2 s / 1 s |
| Boundary attempt lifetime | 10 s, constrained by scoped credential/lease expiry |

The trusted extraction phase happens before assessment and has its own one-file
byte/time bounds; model read counters report delivery of the existing snapshot,
not another disk read. Fixed-buffer reads prevent growth from allocating an
unbounded buffer. Only the exact relative name is accepted. Root aliases, symlink
leaves, nonregular files and hardlinks are rejected; inode/size/mtime/root identity
and the operator-pinned content hash are checked before bytes can reach inference.
The caller must supply a stable, immutable fixture directory under trusted host
control. These checks are not a portable `openat2` replacement or OS containment
for arbitrary concurrently hostile filesystems. Failed/stalled host filesystem
operations and a blocked event loop do not have a hard wall-clock guarantee;
aborted or late acquisition results cannot be sent to the model.

One `PlannerSession` owns one immutable revision and allowance. Repeated or
concurrent `run()` calls reuse its terminal result/promise, including after failure.
There is no persisted assessment cache, restart accounting or scheduler in M0;
the future durable implementation must carry the charge across restarts and
replacement attempts. Changing a brief or policy requires a new explicit session.
The fixture does not rank semantic model candidates or retry alternate routes.

Only a clean terminal text response can enter schema validation/repair. Tool
fragments are withheld by the existing boundary until valid arguments, tool finish,
`[DONE]` and EOF. Partial output, invalid streams, unavailable routes, byte/time
exhaustion and cancellation are terminal without repair or transparent replay.
Cancellation propagates through the scoped transport but returns
`cancelled_unknown`; transport close does not establish provider cancellation.
The boundary retains its separate quiescence/reservation rules.

## Published protocol versus observed compatibility

Official documentation was fetched on 14 September 2026. Its upstream API contract
does not prove subscription-route compatibility through 9Router.

| Capability | Published source and exact experimental outcome |
| --- | --- |
| Astra tools | The current [Astra guidance](https://developers.openai.com/api/docs/guides/latest-model?model=gpt-6-astra) and [function calling guide](https://developers.openai.com/api/docs/guides/function-calling) state that Astra tool calls require Responses. The existing synthetic Chat tool profile cannot establish native Astra tool support. A router adaptation needs separate observed proof. |
| Responses function continuation | The [function calling guide](https://developers.openai.com/api/docs/guides/function-calling) describes application-executed tools with matching call IDs/results. This boundary supports only its fixed Chat assistant/tool continuation; `POST /v1/responses` remains unsupported. |
| Responses stream terminal | The [streaming guide](https://developers.openai.com/api/docs/guides/streaming-responses) identifies typed text deltas and response completion/error events. The accepted Chat parser validates Chat finish plus `[DONE]` plus clean EOF. It is not a Responses parser, and a translated DONE marker is not proof of upstream completion. |
| Strict JSON output | The [Structured Outputs guide](https://developers.openai.com/api/docs/guides/structured-outputs) distinguishes schema-constrained output from merely valid JSON and documents `text.format` for Responses. Here the schema is supplied as prompt data and enforced locally with Ajv **after generation**. Provider strict schema/`response_format` are unsupported by the boundary. |
| Reasoning and settings | [Astra guidance](https://developers.openai.com/api/docs/guides/latest-model?model=gpt-6-astra) distinguishes Responses `reasoning.effort` from Chat `reasoning_effort` and lists unsupported sampling fields. Both effort shapes are outside this boundary profile. No omitted setting is claimed to mean `xhigh`. |

The accepted [task-routing catalog](../contracts/task-route-catalog-2026-09-14.json)
pins 9Router 0.5.75 source commit `17c4cc76877bd1755030a8414f8d0083f48dcccf`.
It supplies source-discovery facts, not live capability or an approved named combo.
For the desired non-UI planning chain, the preflight retains these named outcomes:

- `cx/gpt-6-astra`, desired `xhigh`: `unverified_live_capability`; source preserves
  the effort, while native tool support requires Responses.
- `cc/claude-opus-5`, desired `xhigh`: `unsupported_exact_effort_translation`;
  the accepted source record maps the adaptive setting to `high`.
- `cx/gpt-5.6-sol`, desired `xhigh`: `unverified_live_capability`; source preservation
  is not upstream acceptance or successful tool/stream evidence.

No candidate is silently selected, substituted or promoted to eligible. Existing
inference-boundary tests deny the Responses endpoint; this suite also rejects its
request fields, strict output and reasoning settings with zero upstream calls.

Focused read-only inspection of that pinned public source finds a Chat-to-Responses
[request translator](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/open-sse/translator/request/openai-responses.js)
that converts tool calls/results and maps `max_completion_tokens` to
`max_output_tokens`. However, the subsequent
[Codex executor](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/open-sse/executors/codex.js)
explicitly deletes `max_tokens`, `max_completion_tokens` and `max_output_tokens`;
its final allowlist omits them. Both `cx` candidates therefore also retain
`unsupported_output_token_translation`. Local byte/time fences cannot be relabelled
as a provider output-token bound. A verified router/adapter fix or an explicitly
revised operator requirement is necessary; this source inspection is not a live
request or upstream rejection observation.

## Observed failure coverage

The proof executes real local HTTP/SSE traffic through `Boundary` and `PolicyGate`
with a planner-role attempt, including fragmented complete tool calls and a second
request carrying the public file. It verifies schema-invalid/duplicate/stale output,
one repair and exhausted repairs, bounded file/request/byte/time allowances,
unavailable routes, partial text/tool streams, pre-dispatch and midstream stop,
unknown tools and paths, symlink/hardlink/nonregular/changed-file denial, and
instruction-like repository data. An adversarial final response demanding route,
shell and execution authority is rejected while the policy and call count remain
unchanged. No proposal execution function exists to accept those demands.

## Live completion path and remaining acceptance criteria

Before live probes, an accepted adapter must fence all real 9Router writers and
prove upstream quiescence under the approved complete graph. The current #3
`Policy` explicitly accepts only a synthetic graph. Replacing its evidence label,
providing a router URL or asserting readiness in JSON would bypass that gate;
this experiment offers none of those switches.

The coordinated [source-module authority experiment (#74)](https://github.com/korallis/letmecook/issues/74) on this exact router revision
also found that the UI pending count reaches zero after 60 seconds without a
completion, disconnect clears that count before the delayed local abort, and the
combo writer can update a route while its count remains one. That experiment used
an isolated fake clock/in-memory database, not a running authenticated router or
provider. Consequently this planner must not implement `FrozenAuthority` using
dashboard counters or assume the existing route writer shares its epoch fence.
These are concrete live-adapter blockers in addition to the protocol/settings gaps.

Once that prerequisite exists, bind an exact operator-approved bootstrap route,
its full fallback graph, data policy and tested settings to a planner-role grant.
Use this same 484-byte public fixture/hash/schema and fixed budgets. Through that
scoped boundary, observe Responses text and typed terminal events; fragmented
function-call arguments; one approved read/result continuation; clean schema-valid
final output; actual requested/translated effort and strict-output support; route
outage; and local cancellation with separate upstream quiescence evidence. Record
route/build/graph/profile/settings fingerprints, safe request/attempt attribution,
named unsupported outcomes and redacted actual results. Every fallback needs the
same mandatory capability evidence. Do not use a direct provider endpoint, private
repository, provider key in a planner process, or a synthetic adapter labelled live.

Remaining mandatory criteria are the bounded request through the actual shared
9Router, exact approved bootstrap/settings and Responses/tool/stream observations,
and corresponding redacted live evidence. This draft completes the independent
restriction tooling and synthetic fixtures; it does not close those live gates.
