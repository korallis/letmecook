> Native integration update: [closed read-only Responses profile and actual synthetic gateway evidence](../../experiments/planner-probe/native-integration.md). Historical Chat/translated-native observations below remain preserved; live conformance is still outstanding.

# Restricted planning: synthetic contract passes, live gate blocked

For [issue #7](https://github.com/korallis/letmecook/issues/7). This M0 experiment
demonstrates schema validation, restricted discovery and bounded coordinator
inference over the accepted attempt-scoped boundary. The [recorded run](planner-probe-run.json)
contains actual automated observations from synthetic HTTP/SSE integration on
14 September 2026. The schema-1 fixture remains preserved. The added [actual-router run](planner-router-run.json) uses pinned router modules, real SQLite and synthetic original HTTP with the real `PlannerSession` through accepted #79. Neither record is **live provider or deployed-router evidence**. #7 remains
incomplete and its PR remains a draft pending the mandatory live criteria.

The branch includes accepted #2/#3/#4/#5/#71/#77/#79 outcomes. This experiment does not
modify the inference boundary or selected Linux profile, create the durable Go
application core, select the worker harness, or implement #30's alpha task-aware
selector. The original schema-1 coordinator runs as trusted host code with typed read dispatch. The added schema-2 proof places the real planner and its public snapshot in a separate 128 MiB consumer, using the accepted bridge’s 768 MiB trusted gateway controls. This measured placement is recorded separately below; neither mode executes repository code.
The selected [Linux execution proof](linux-profile.md) remains a prerequisite for
unattended repository execution. The original record is not evidence of that profile; the new run includes effective Docker controls for its distinct placement.

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
budgets. Schema 1 keeps `gaffer-planner-fixture` under `chat-text-tools-v1`; schema 2 pins `gaffer-planner` under one accepted synthetic router profile. The complete validated schema-2 policy—including deployment, boot/generation, graph, route and approved source/runtime envelope—is hashed into the revision. Only a public policy projection enters the model packet; scoped tokens, account identity and private control state do not. Repository content is a labelled tool-result string;
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

Fixture setup also cleans up after post-listen budget or snapshot failures. A
subprocess regression must exit naturally with its owned directory removed, so
unlinked-but-listening servers cannot pass. Injected cleanup failure preserves
both the original setup error and the cleanup error; other listeners are still
closed, while uncertain boundary state is retained for inspection.

## Actual-router synthetic PlannerSession integration

The [schema-2 run](planner-router-run.json) imports the accepted `RouterAuthority`,
`PolicyGate`, `Boundary`, source-verifying loader and original terminal observers.
There is no copied receipt/account authority adapter. The original network endpoint
is synthetic loopback HTTP inside the trusted gateway. Actual 9Router 0.5.75 modules
at `17c4cc76877bd1755030a8414f8d0083f48dcccf` and actual SQLite handle every admitted
request. No real provider, credential operation, private repository, live router or
paid substitute is involved. Fixture configuration and fault injection are local
proof machinery, not deployed conformance.

```sh
GAFFER_BRIDGE_ROUTER_SOURCE=/absolute/path/to/pinned/public/9router \
  node experiments/planner-probe/router-prove.ts /absolute/path/result.json
```

The public source must match the accepted source lock. This command uses Docker
context `desktop-linux` and the locally locked
`gaffer-router-extension-deps:0.5.75-locked` image; it checks the image digest and
selected Docker engine/kernel/architecture before starting. Host Node is 24.19.0;
container Node is 24.21.0. The measured image is
`sha256:f95ae5b2218838d3da613273705b7914a149593c400a33087e9c383c2e12650a`.
Optional `GAFFER_BRIDGE_CASES=compatible:read,native:read` selects diagnostics and
produces a selected-cases result, never a full-suite pass. Preserve the original
schema-1 proof by writing follow-up `prove.ts` output to a different path.

The distinct topology is `planner-consumer-128m-router-gateway-768m-uds-v1`.
The consumer receives selected public planner/reader/protocol files, the 484-byte
fixture, schema, and pinned Ajv runtime dependencies. Router source, overlay,
control, SQLite journal and synthetic ingress/provider configuration remain in the
768 MiB gateway. Both containers run as uid 1000, with network none, read-only root,
all capabilities dropped, no-new-privileges, bounded CPU/PIDs/memory/tmpfs/logs and
separate namespaces under the accepted bridge controls. The only shared mount
exposes the boundary UDS read-only to the consumer. Effective controls, memory/PID
observations, denied management/private paths/direct TCP, stopped processes and
owned container/volume/staging cleanup are captured. This is a separately measured
synthetic planner placement, not an expansion of the selected execution guarantee.

Schema-2 requests deliberately omit `tool_choice`; they advertise `tools` only
while the one-read allowance permits discovery, and omit tools after the read and
during repair. The transport validates the consumer JSON, bounds the actual bytes
sent, and lets the boundary alone map `max_completion_tokens:1024` to `max_tokens`.
The parser independently enforces each request’s local tool allowance and validates
the boundary’s safe request ID. It reconstructs canonical per-request call IDs;
the continuation pairs assistant `content:null`, the complete call and matching
result. Blank malformed output is omitted from assistant history for the one
charged repair; it is never fabricated into valid model output.

A clean consumer terminal is available only after the shared boundary has joined
original evidence and persisted acceptance. The trusted proof joins each completed
planner request ID to the saved policy/reservation, actual router receipt, durable
`validated_success` decision, completion digest, completed delivery and observed
release time. These safe references are not a new authority source. Original EOF,
router stop text or native completion alone cannot trigger a read, proposal or
repair. Normal read/final uses exactly
`{assessments:1,repairs:0,requests:2,files:1,readBytes:484}`; read plus one repaired
proposal uses three requests. Repeated `run()` reuses the same result and charges.

The final suite contains 43 cases (36 compatible Chat and 7 native translated diagnostics). Coverage includes read/final and repair; blank, whitespace, stale, duplicate and
schema-invalid proposals; forced repair tools; repository authority demands;
read/file/request/byte/time allowances; delayed original EOF and receipt visibility
before reads, proposals and repairs; missing/mismatched receipts; zero-operation
stopped failure; scope and lease expiry; cancellation before/after acceptance;
actual guarded graph replacement at admission, receipt and release; failed decision persistence; and
partial or malformed original transport. A malformed rejection with a second
configured account stays at one physical send and retains its unknown operation;
there is no transparent retry after uncertain transport.

The native diagnostic exercises actual Chat→Responses request translation,
fragmented function calls and correlated Responses call/result continuation, then
Responses→Chat output through the accepted private receipt-gated completion path.
Failed/incomplete originals translate to apparent Chat stop but release no effects.
Native output-token fields are actually absent at the original endpoint; this
remains `unsupported_output_token_translation`, not an approved bound. The consumer
still speaks Chat and has no Responses request/continuation codec. No actual model,
requested effort or provider strict-output support is established.

[Retained failed observations](planner-router-failures.json) include the initial
incorrect expectation that a malformed rejection would permit fallback, and a
native fixture that supplied text only inside `response.completed`: the pinned
translator dropped that summary text, so three requests were charged and the plan
remained invalid. The corrected native fixture includes text-delta/content/item
lifecycle events. A dedicated terminal-text-only diagnostic retains the measured
no-proposal result. The earlier authority-fence-only graph observations are retained separately; the final graph cases use the accepted guarded writer and verify a changed graph digest. Earlier schema-1 fixture cleanup review and original proof are
also retained; later success does not erase those observations.

## Live completion path and remaining acceptance criteria

Before live probes, an accepted adapter must fence all real 9Router writers and
prove upstream quiescence under the approved complete graph. The accepted schema-1/schema-2 policies explicitly accept only synthetic graphs. Replacing its evidence label,
providing a router URL or asserting readiness in JSON would bypass that gate;
this experiment offers none of those switches.

The coordinated [source-module authority experiment (#74)](https://github.com/korallis/letmecook/issues/74) on this exact router revision
also found that the UI pending count reaches zero after 60 seconds without a
completion, disconnect clears that count before the delayed local abort, and the
combo writer can update a route while its count remains one. That experiment used
an isolated fake clock/in-memory database, not a running authenticated router or
provider. Consequently this planner must not implement `FrozenAuthority` using
dashboard counters or assume the existing route writer shares its epoch fence.
Accepted #77/#79 now implement and exercise the separate enforced authority against actual pinned router modules and SQLite with synthetic upstream HTTP. The stock-source failures remain retained observations; deployed conformance and the protocol/settings gates remain incomplete.

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

Remaining mandatory criteria are the bounded request through the operator-selected shared live
9Router, exact approved bootstrap/settings and Responses/tool/stream observations,
and corresponding redacted live evidence. This draft completes the independent
restriction tooling and synthetic fixtures; it does not close those live gates.

The first follow-up CI head `eca0a1e` passed typecheck and 34 unit tests but failed
its second test execution inside `prove.ts`; the old log omitted failing names and
uploaded no artifact, so that original CI failure's exact identity remains unknown.
On the unchanged head, a Linux replay with a 0.5 CPU quota reproduced the `total
time` test expecting one physical send after a 50 ms deadline when the correct
observed count was zero. Deadline expiry does not guarantee completed admission.
The regression now advances the actual planner timer explicitly before dispatch
and after a request is observed, preserving exact charged-request, no-repair and
no-retry assertions. Proof logs include safe failing test names/runtime, and CI
retains its redacted JSON on failure. The original failure and reproduction are
recorded in the retained observations; a later green check does not identify the
original opaque failure retroactively.
