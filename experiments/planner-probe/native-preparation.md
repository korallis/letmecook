# Native planner consumer preparation

This is consumer-owned preparation for issue #7, separate from the historical
Chat fixtures and actual-router translated-Chat diagnostics. It has no production
native boundary adapter, deployer, provider client, authority journal or account
selector. Its in-memory port cannot validate an authority policy, issue a grant or
start a physical attempt. PR #73 and live acceptance remain incomplete.

`PlannerSession` accepts the small `PlannerTransport`/`PlannerConversation`
interface. `BoundaryTransport` retains the old Chat settings and normalization.
`NativePlannerTransport` consumes only completed, receipt-released output from
`NativePlannerBoundaryPort`. That port is an integration obligation, not evidence
that the durable gate ran: the fixture substitutes a deliberately incomplete,
non-admissible synthetic policy and in-memory responses.

The native protocol is `planner-probe-responses-read-file-v1`. Consumer requests
select the trusted named router model; its native authorization and the expected
physical response model are exactly `gpt-6-astra`. Requests fix `xhigh`, summary
`auto`, streaming, `store:false`, encrypted-reasoning inclusion, the planner's own
`ses_planner_...` identity and the complete `read_file` schema. There is no output
cap field, alternate model/account, OpenCode identity, `apply_patch` or strict
provider JSON-schema claim. Native results report provider token and monetary caps
as unavailable (`null`). The full private policy still hashes into the input
revision; only the explicit schema-3 public projection enters the model packet.

`NATIVE_PLANNER_BUDGET` explicitly retains 5000 ms, three consumer requests, one
assessment, one repair, one file, 4096 aggregate read bytes and 32768-byte request
and response limits. `DEFAULT_BUDGET` remains unchanged, including Chat's
1024-token request. A hard provider-cap requirement is rejected. The consumer
clock does not start, extend or replace the shared physical-attempt scope; no live
timeout has been selected by this preparation.

## Continuation contract

1. Assessment may call exactly `read_file({"path":"fixture.txt"})` once when its
   file/byte budget permits. Calls with extra properties, duplicate JSON keys,
   escaped paths, duplicate calls or worker tools fail before a read.
2. The final request replays the exact accepted ordered output, opaque encrypted
   reasoning and original `call_id` with its paired result. Every native request
   retains the read-only tool schema. Final and repair set `tool_choice:none`;
   consumer validation also rejects calls in those phases.
3. Only a completed, released but locally invalid plan enters one charged repair
   for the unchanged revision. Replay includes blank terminal output and all
   reasoning. An empty completed output appends only the fixed repair instruction.
   Invalid JSON/schema/references use the same local validator as Chat. No new
   discovery or evidence enters repair.
4. A valid plan, invalid repair, transport failure, cancellation, elapsed deadline
   or uncertain original work ends the session. `run()` caches its result. The
   native transport also rejects reuse for another conversation. Consumer
   cancellation ends waiting even if a port is late; shared authority must retain
   unresolved original work. Neither event authorizes replacement.

The versioned consumer projection accepts raw completed items with optional
output-item IDs/status fields, and omits those fields on stateless replay. It
preserves `call_id`, original argument strings, encrypted content and order.
Reasoning summaries contain only `summary_text`; messages contain only
`output_text` with absent/empty annotations and logprobs. Failed/incomplete items,
refusals, unknown item fields, duplicate present IDs and text mixed with a call
are rejected. The shared codec must validate and freeze this exact normalization
before any real port is enabled. Current worker normalization is not compatible.

## Required shared integration after #83

The frozen shared analysis reference is `70bacc54cce839e5d0b6e168dceee10977a4eecb`.
It was inspected, not merged into this branch, and supports only the OpenCode
worker profile. This preparation changes no shared policy, codec, scope or
launcher. The following shared seams still block native integration:

| Shared API | Required planner extension and verification |
| --- | --- |
| `NativeProfile` / `validateNativeProfile` | Closed planner descriptor with its own source, settings and plan-schema digests; exact `read_file` definition/path; never accept an arbitrary caller tool registry or claim the worker binary. |
| `validateNativeRouterPolicy` / binding validation | Validate the full schema-3 graph/profile/authority policy before constructing the real port. Bind planner protocol to `role:planner` and worker protocol to `role:worker` at grant issuance, admission, send, persistence reload and release. Preserve schema-3 `Binding.native` profile/scope/authorization digests. |
| `validateNativeRequest` / `continuationOutput` / `NativeResponsesStream` | Dispatch the closed planner shape; match exact ordered continuation and session identity; allow the bounded final/repair transitions and complete schema with `tool_choice:none`; independently forbid further calls. Preserve worker validation unchanged. |
| `PolicyGate.nativePrevious`, saved decisions and reservations | Persist or derive phase and remaining repair allowance from accepted history. A repair must follow a locally invalid released plan, use the unchanged revision and fixed instruction, and consume one request. A restart or session ID cannot create another allowance. |
| Original receipt gate / port implementation | Parse bounded SSE only through the reviewed native parser. Match the original physical request/output digests, scope, binding, request ID and original receipt. Resolve `complete()` only after durable validated success and final release fencing. Missing/delayed/mismatched receipts, persistence failure, stop, expired lease and unknown original work reject without reads, repairs or proposals. |
| Profile registry and serial selection | Predeclare both role descriptors under one deployment/authority and aggregate scope. Select only after quiescence and generation fencing; retain the same scope row, spent count, start and deadline across roles. No restart, second store or renewed initial scope. |

`NativePlannerBoundaryPort.complete(request, signal, responseBytes)` returns only
`{requestId, acceptedAt, response:{model,status,error,incomplete_details,output}}`
from a completed released response. It exposes no receipt authority. Its
`policy` is the **full shared-validated** frozen policy; `binding` is the trusted
role/protocol/session projection of the same bound grant. The consumer compares
both before send and after release. Its local response-size check supplements,
and does not replace, the port's complete raw SSE byte limit.

The real consumer port should use `/v1/responses` on the private scoped boundary
socket with ordinary boundary headers. Stage only the necessary consumer files,
Ajv, the shared full policy validator and pure native profile/Responses/terminal/
scope dependency closure. The existing Chat staging stays unchanged in this
preparation because it imports none of the new native files. Do not mount gateway
state, credentials, configuration or its control socket to solve missing imports.

Before enabling the port, verify the exact physical `tool_choice:none` and
read-only schema against the pinned router's strict-false removal and default
instruction injection. Current official Responses/tool/stream parameter checks,
actual malformed/failed/incomplete SSE cases, receipt failure/reload cases,
per-send debit/retry evidence and kernel isolation still require an integrated
native fixture. These are not supplied by in-memory port refusal tests.

## Preparation checks

```sh
npm --prefix experiments/planner-probe run check
npm --prefix experiments/planner-probe test
npm --prefix experiments/planner-probe run check:native
npm --prefix experiments/planner-probe run test:native
npm --prefix experiments/planner-probe run prove:native -- /absolute/path/result.json
```

The separate native proof records exact source digests, safe test names, failed
observations, aggregate usage and an explicit in-memory cleanup statement. It
contains no model request/response bodies or private identities. CI retains both
Chat and native proof files after failure. No test spends a physical model call.
The recorded run passed [81 native consumer checks](evidence/native-preparation-run.json)
plus the [35 legacy fixture tests and 43 actual synthetic-router cases](evidence/legacy-regression-run.json).
The latter includes 36 compatible Chat cases and seven translated-native
diagnostics, with all owned containers and volumes removed. Its 58 consumed
runtime source hashes still match after preparation-only edits; the compact
artifact identifies the changed files outside that legacy runtime and hashes the
retained full raw proofs. No in-memory native result upgrades router capability
evidence.

Before the single approved initial 10-physical-attempt / 600000-ms aggregate scope
starts, finish both consumers, their fresh independent reviews and the concrete
deployment packet. All retries and consumer roles share that allowance. Review or
preparation delays and baseline scopes cannot replenish it. Keep PR #73 draft
until integrated and authorized live results receive fresh review on the final
head. This preparation does not mark issue #7 complete.
