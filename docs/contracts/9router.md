# 9Router integration contract v1

**Status:** historical and optional 9Router gateway profile. The generic product
requirement is [specification §8](../spec.md#8-model-gateway-integration-and-task-capacity)
and the [model gateway contract](model-gateway.md). This profile applies only when
an operator selects 9Router; its source observations and evidence remain intact.

For [issue #2](https://github.com/korallis/letmecook/issues/2), amended by the
operator-approved [issue #81](https://github.com/korallis/letmecook/issues/81)
limits profiles on **14 September 2026**. This specifies the boundary proposed for
the M0 experiments; it does not implement or certify a deployment. Fixture
identifiers below are in
[the conformance corpus](../../tests/fixtures/9router/README.md).

## Evidence and deployment gate

| Item | Evidence / decision |
| --- | --- |
| Inspected repository | `https://github.com/rickicode/9router.git` |
| Inspected commit | `69724d86d4486fa5d722e63ebb6c9700e71aebff` |
| Package version at that commit | `9router-app` **0.3.98**, read from `package.json`; not a unique deployment identity |
| Method | Read-only clone, detached checkout, focused source inspection; no upstream install, server launch, provider login or inference |
| v0.4 reconciliation | Same commit as the evaluation; the additional limitations below come from inspecting that exact source |
| Selected deployment | Existing operator-selected npm `9router@0.5.75`; registry source association `decolua/9router` commit `17c4cc76877bd1755030a8414f8d0083f48dcccf`; Next build `lT_m29PZoyMEswZrqhKVs` |
| Artifact verification | All 3,253 installed regular package files matched the registry tarball by SHA-256, with no changed/missing files. Tarball SHA-512 verified; see the [redacted deployment record](9router-deployment-2026-09-14.json) |
| Reconciliation / live results | Both pinned sources inspected below. Read-only management denial/discovery observations exist; no inference, fallback, cancellation or containment conformance was run |

The inspected pin is a reproducible reference, not an instruction to install it.
A deployment record must supply router ID, private origin reference, exact source commit or
artifact digest and its source mapping, package version, local patches, enabled
Go/wrapper/relay path, and operator-provided credential **references**. Record no
credential values. Reinspect differences from the pin before accepting the adapter.
Version strings alone, a successful discovery call, and a dashboard screenshot do
not satisfy this gate. The selected record supplies artifact identity and source
reconciliation; this completes the version investigation without making that
instance ready for unattended work. Its host is optional operator infrastructure,
never a required product host, network or installation location. Every installation
supplies its own origin and credential references. The remaining sections are
normative **proposed Gaffer requirements**; source observations are identified.

### Inspected source observations

This table is the historical `rickicode` 0.3.98 profile, retained to reconcile the
v0.4 review. It is not a description of the selected 0.5.75 deployment. Links below
pin each finding to the inspected commit, not upstream `main`.

| Finding | Source |
| --- | --- |
| Chat Completions, Messages and Responses handlers call `handleChat`; `/v1/*` rewrites to `/api/v1/*`. Additional `/v1/v1/*` and `/codex/*` aliases exist. | [rewrites](https://github.com/rickicode/9router/blob/69724d86d4486fa5d722e63ebb6c9700e71aebff/next.config.mjs), [chat handler](https://github.com/rickicode/9router/blob/69724d86d4486fa5d722e63ebb6c9700e71aebff/src/sse/handlers/chat.js) |
| Authentication is conditional on `requireApiKey`. Bearer and `x-api-key` are recognized. Key validation checks value and active state, without route scope. | [authentication](https://github.com/rickicode/9router/blob/69724d86d4486fa5d722e63ebb6c9700e71aebff/src/sse/services/auth.js), [key validation](https://github.com/rickicode/9router/blob/69724d86d4486fa5d722e63ebb6c9700e71aebff/src/lib/localDb.js#L957) |
| Connections are chosen by 9Router; combos are selected by name and try ordered or rotated model lists. Combo success is `Response.ok`, before stream completion. | [account selection](https://github.com/rickicode/9router/blob/69724d86d4486fa5d722e63ebb6c9700e71aebff/src/sse/services/auth.js), [combo fallback](https://github.com/rickicode/9router/blob/69724d86d4486fa5d722e63ebb6c9700e71aebff/open-sse/services/combo.js) |
| Model discovery can return static models with zero active connections; compatible-provider discovery may itself fetch the upstream model catalog. | [discovery](https://github.com/rickicode/9router/blob/69724d86d4486fa5d722e63ebb6c9700e71aebff/src/app/api/v1/models/route.js) |
| Provider GET removes selected top-level secrets but spreads the rest of each connection, including nested provider data. Tokens can live in that data. | [provider GET](https://github.com/rickicode/9router/blob/69724d86d4486fa5d722e63ebb6c9700e71aebff/src/app/api/providers/route.js), [nested tokens](https://github.com/rickicode/9router/blob/69724d86d4486fa5d722e63ebb6c9700e71aebff/src/sse/services/tokenRefresh.js) |
| Availability GET lists negative observations; its POST clears cooldowns. Per-connection usage GET can refresh tokens, contact providers and update router state. Aggregate usage can include API keys as values and object keys. | [availability](https://github.com/rickicode/9router/blob/69724d86d4486fa5d722e63ebb6c9700e71aebff/src/app/api/models/availability/route.js), [active usage GET](https://github.com/rickicode/9router/blob/69724d86d4486fa5d722e63ebb6c9700e71aebff/src/app/api/usage/%5BconnectionId%5D/route.js), [usage aggregation](https://github.com/rickicode/9router/blob/69724d86d4486fa5d722e63ebb6c9700e71aebff/src/lib/usageDb.js) |
| Dashboard protection does not cover every management API. Some covered paths also bypass authentication for a localhost Host header. | [matcher](https://github.com/rickicode/9router/blob/69724d86d4486fa5d722e63ebb6c9700e71aebff/src/proxy.js), [guard](https://github.com/rickicode/9router/blob/69724d86d4486fa5d722e63ebb6c9700e71aebff/src/dashboardGuard.js) |
| Combo PUT updates a mutable record without a request-scoped policy revision. Model resolution also reads mutable aliases/provider nodes. No atomic authority barrier was found on the inspected inference/configuration paths. | [combo mutation](https://github.com/rickicode/9router/blob/69724d86d4486fa5d722e63ebb6c9700e71aebff/src/app/api/combos/%5Bid%5D/route.js), [resolution](https://github.com/rickicode/9router/blob/69724d86d4486fa5d722e63ebb6c9700e71aebff/src/sse/services/model.js) |
| Streaming success is recorded before body consumption. Disconnect detection creates its own AbortController; the incoming request signal is not passed into `handleChatCore`. The stream cancel path delays abort by 500 ms. End-to-end cancellation, especially before headers, is unproved. | [core](https://github.com/rickicode/9router/blob/69724d86d4486fa5d722e63ebb6c9700e71aebff/open-sse/handlers/chatCore.js), [stream response](https://github.com/rickicode/9router/blob/69724d86d4486fa5d722e63ebb6c9700e71aebff/open-sse/handlers/chatCore/streamingHandler.js), [disconnect handling](https://github.com/rickicode/9router/blob/69724d86d4486fa5d722e63ebb6c9700e71aebff/open-sse/utils/streamHandler.js) |
| Stream flush synthesizes `[DONE]`, invokes translator flush and may estimate usage; transport EOF/200/usage are not proof of complete provider output. OpenAI-to-Claude JSON schema is converted into prompt instructions. | [stream flush](https://github.com/rickicode/9router/blob/69724d86d4486fa5d722e63ebb6c9700e71aebff/open-sse/utils/stream.js), [JSON-schema translation](https://github.com/rickicode/9router/blob/69724d86d4486fa5d722e63ebb6c9700e71aebff/open-sse/translator/request/openai-to-claude.js) |

### Selected 0.5.75 source reconciliation

The [npm artifact](https://registry.npmjs.org/9router/-/9router-0.5.75.tgz) publishes
the `gitHead` above. Its build ID matches the inspected deployment. Registry
metadata associates the package with this source; no independent source rebuild
was performed. The file comparison proves equality to the distributed package,
not absence of upstream defects. No local package patches were found. The observed
runtime is the packaged Next CLI; Go-wrapper environment flags were unset.

| Boundary | Change at `decolua/9router@17c4cc7` and decision |
| --- | --- |
| Management authentication | The matcher now covers nearly all paths and `/api/*` defaults to authenticated access. Production local-peer trust uses custom-server stamped headers with a per-process token rather than trusting Host alone. CLI-token and dashboard sessions remain broad authorities, and disabling login still weakens management auth. Retain a separate private GET-only status ingress; no CLI token or dashboard cookie belongs in a worker. [guard](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/src/dashboardGuard.js), [trusted peer](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/src/lib/auth/trustedPeer.js), [server stamping](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/custom-server.js) |
| Inference authentication | Defaults now set `requireApiKey=true` and `requireLogin=true`; nonlocal inference is also gated by middleware. The chat handler still conditionally enforces keys; key validation still has no route scope. Generated JWT secrets replace the earlier fixed fallback. Missing/invalid/disabled-key inference tests remain unrun. [defaults](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/src/lib/db/repos/settingsRepo.js), [key validation](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/src/lib/db/repos/apiKeysRepo.js), [session secrets](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/src/lib/auth/dashboardSession.js) |
| Status schema | Provider GET returns `{connections}` and retains arbitrary nested data; canonical `routingStatus/authState/quotaState/lastCheckedAt` semantics from the historical fork are absent from its routing path. Availability now reports model locks as `cooldown` or legacy `testStatus=unavailable`. Use the separate conservative 0.5.75 adapter profile below; never reinterpret `active` as readiness. [providers](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/src/app/api/providers/route.js), [availability](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/src/app/api/models/availability/route.js), [selection](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/src/sse/services/auth.js) |
| Endpoints / discovery | The three inference handlers still call `handleChat`; `/responses` adds another alias to deny at the Gaffer boundary. Model discovery can call live provider catalogs and persist refreshed credentials. Exclude `/v1/models` from passive status polling; a discovery GET is not necessarily read-only. [rewrites](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/next.config.mjs), [discovery and refresh callbacks](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/src/app/api/v1/models/route.js) |
| Hidden model paths | Capability adapters can prepend globally configured modality models even to a single-model request. An enabled empty pool defaults to `oc/mimo-v2.5-free`; fusion runs parallel panel models and a configurable judge. Combo members alone no longer enumerate the envelope. M0 rejects fusion and unapproved capability-adapter pools; inspect effective defaults as well as persisted overrides. [capacity adapter](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/open-sse/services/capacityAdapter.js), [fusion](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/open-sse/services/combo.js), [dispatch](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/src/sse/handlers/chat.js) |
| Streaming and cancellation | Responses passthrough now synthesizes `response.failed` on abort/stall; a watchdog measures upstream byte activity. Successful HTTP setup still precedes stream completion, flush still emits completion sentinels, and `handleChatCore` still creates its own signal rather than accepting the incoming request signal. Keep the existing partial-stream and cancellation gates; watchdog activity is not the semantic idle deadline. [stream response](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/open-sse/handlers/chatCore/streamingHandler.js), [watchdog](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/open-sse/utils/streamHandler.js), [flush](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/open-sse/utils/stream.js) |
| Payload and external effects | OpenAI-to-Claude JSON schema still becomes prompt instructions. The core can prefetch remote image URLs and run configured compression helpers. Review those destinations/settings as part of data policy, and exclude these features from the initial text/tool-only profile unless explicitly proven. [schema translation](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/open-sse/translator/request/openai-to-claude.js), [core](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/open-sse/handlers/chatCore.js) |
| Policy mutation / active reads | Combo updates reset rotation but still have no request-scoped atomic authority epoch. Usage GET still refreshes credentials/provider state. Router-owned SQLite and export/import replace the older storage layout; neither is the Gaffer integration API. [combo update](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/src/app/api/combos/%5Bid%5D/route.js), [usage GET](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/src/app/api/usage/%5BconnectionId%5D/route.js), [storage](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/src/lib/db/index.js) |

Read-only deployment inspection found 12 active configured connections across
seven providers and **zero named combos**. These are configuration counts, not
authenticated inference/capacity observations. No route was created or selected for
Gaffer, and no inference POST was made. A discovery GET was observed; its possible
provider-catalog/credential-refresh side effects were not measured. The listener is not bound exclusively to
a private interface; broader firewall exposure was not assessed. Therefore private
ingress, scoped status access, route authority, named-route setup and runtime
conformance remain explicit downstream gates despite a reconciled package pin.
Operators who choose to move an existing configuration can use the
[optional router-owned migration procedure](9router-migration.md); it is independent
of Gaffer installation and was not executed during this inspection.

## Named route and attempt identity

`router_id` identifies one operator-managed deployment. `route_id` is a stable
Gaffer policy identity scoped to that router, not a display label or account ID.
`router_model` is the exact, case-sensitive combo name or approved provider/model
string sent in the JSON `model` field. Prefer named combos. Renaming/deleting a
combo or reusing its name never silently retargets an existing grant.

A policy has an immutable `policy_revision`, a configuration `epoch`, exact router
build, protocol/harness/settings profile, limits profile and its explicit authority,
capability evidence, a complete permitted provider/model/billing envelope and
request bounds. Its fingerprint is SHA-256 over a deterministic canonical policy
document; retain that document as evidence. The epoch is an enforced admission and
mutation generation, not a hash. Reject unresolved aliases, nested combo cycles,
unknown endpoints, unclassified billing and any unenumerated fallback edge.

Resolve the whole reachable graph: combo members and strategy, model aliases,
provider-node endpoint/prefix, model remapping, relay destination, provider-level
model settings, capability-adapter pools and defaults, fusion panel/judge,
compression/helper destinations and billing-affecting connection class. An endpoint URL alone does
not establish provider identity. A subscription-only grant excludes paid API and
unclassified fallthrough. API-key/OAuth authentication type is not sufficient proof
of billing class. Use operator-reviewed policy evidence for that classification.

Multiple subscriptions remain separate router connections. Gaffer can keep opaque
connection references for status/evidence but never picks a connection on a request,
stores provider tokens, estimates account balances or runs a reset/refresh scheduler.
All eligible connections must fit the envelope; adding an account that introduces
a provider, endpoint, model or billing possibility is a policy change.

The boundary authenticates an opaque, revocable attempt token. Its server-side
binding contains attempt ID, lease/fence identity, grant ID, router/route IDs,
policy revision, epoch, expiry, capabilities, full graph fingerprint, exact router
build and protocol/harness/settings profile, limits profile, authorization policy
reference and reserved limits. It checks current lease/revocation state on admission
and cancellation; a token is never authority
for a later attempt. Coordinator/planner/reviewer calls use the same boundary with
their own bounded identity. Tokens and router keys are excluded from durable
events, fixtures, subprocess command lines and logs.

## Inference transport and bounds

The worker gets only its attempt credential and the scoped boundary endpoint.
The boundary owns the router inference credential and sends it as `Authorization:
Bearer …`; it strips worker authorization, cookies and forwarding headers first.
Set and verify router `requireApiKey=true`: missing, invalid and disabled keys must
fail on every selected inference path. Because the pin has no per-key model scope,
authentication by itself does not authorize direct worker-to-router access.

| Exposed worker method/path | Requirement |
| --- | --- |
| `POST /v1/chat/completions` | Chat Completions profile, only when granted |
| `POST /v1/messages` | Messages profile, only when granted |
| `POST /v1/responses` | Responses profile, only when granted |
| Everything else | Deny, including raw `/api/v1/*`, `/v1/v1/*`, `/codex/*`, `/responses`, query routing overrides, absolute-form URLs, redirects, CONNECT and management paths |

Select the upstream origin from trusted configuration, never from headers/body.
Parse and validate a single JSON object; reject duplicate keys, compressed bodies
unless explicitly bounded after decompression, unsupported content types and
noncanonical paths before forwarding. Reject a model unequal to `router_model`;
forward the exact authorized model. No arbitrary header or unknown top-level
request-field passthrough. Each pinned harness profile declares allowed fields and
values, including tool schemas, reasoning settings and protocol version headers.
Reject routing/account/provider hints, provider-hosted tools and file/remote resource
retrieval unless separately authorized in that profile. New harness fields require
profile review rather than silently widening the envelope.

An HTTP gateway is compatible with harnesses. OS isolation may expose it through a
private network allowlist or a Unix-domain socket relayed to loopback **inside** a
network-disabled worker. The transport must still enforce the identity above.
Workers must not reach the router origin, management plane, public inference,
provider URLs, host loopback/services, alternate proxy routes or credential mounts.
Containment is an OS/network policy with negative tests, not an environment variable
or Git worktree. The trusted boundary is outside the worker sandbox. See the
[execution boundary](../spec.md#42-execution-boundary) for supported profile
requirements; this contract does not claim they are implemented.

Every grant selects a versioned **limits profile** and explicitly sets enforced,
finite request bytes, response bytes, concurrency, request count, total deadline,
first-output deadline, semantic-idle deadline and attempt-wide retry/time budget.
Router/harness retry and physical inference-subattempt counts also have finite
ceilings; retries and fallback consume the shared attempt allowance. Heartbeats
do not reset semantic deadlines. Smaller applicable task, router and provider
limits win. The limits profile is distinct from the protocol/harness profile;
both must be compatible and bound into the policy revision and grant.

`strict-provider-output-v1` is the default and the interpretation of existing
grants, including legacy grants without a stored profile field. Those grants keep
their required cap; omission never selects native limits. New grants encode the
profile explicitly. Strict additionally requires a positive finite integer provider output-token
cap. Reject missing, invalid or excessive caps; map the approved cap to the tested
protocol field (`max_completion_tokens` or a profile's tested `max_tokens`, Messages
`max_tokens`, Responses `max_output_tokens`). Prove that the deployed path preserves
and enforces it across **every** approved fallback and retry. There is no automatic
downgrade or migration to another limits profile.

`native-subscription-local-v1` requires explicit operator authorization in the grant
or its standing policy. Every reachable connection and fallback needs reviewed
subscription billing classification and all other mandatory capability evidence.
Provider output-token and monetary bounds are explicitly **unavailable**. Reject
this profile for tasks or grants requiring either hard provider bound. Subscription
preference cannot override that exclusion. The approved profile is specified in
[native subscription local limits](native-subscription-limits.md); it is not an
implemented or live-certified route.

A boundary timeout, response-byte limit, visible-text count, prompt instruction or
reasoning/verbosity preference is not a provider-generation limit. A request with
a required output-token cap cannot be silently downgraded or have that requirement
stripped. An incompatible harness stays ineligible until a reviewed request profile
represents the selected limits honestly. Hard monetary caps require separately
verified maximum-cost evidence across hidden retries/fallback, reservations and
enforceable router/provider controls; otherwise hard-cap admission is denied.
Strict output limits alone do not prove a monetary cap. Subscription classification
means neither zero cost nor a known remaining allowance. Unknown usage/cost is not
measured zero. The boundary makes **one** upstream request per admitted request and
never retries a provider or selects an account. Residual remote work is tracked
separately and may remain unknown after local budgets expire.

The conservative [example policy](../../tests/fixtures/9router/examples.json)
remains a **strict** example: 1 MiB request, 4,096 provider output tokens, 8 MiB
response, one in-flight request, 32 requests/inference subattempts per attempt,
120 s per request, 30 s first output, 15 s idle and 10 minutes per attempt. These
are defaults to test, not verified provider or harness compatibility. The separate
[native example](../../tests/fixtures/9router/native-limits-example.json) declares
concrete local controls and unavailable provider bounds; it sets no native product
defaults. The operator's [bounded evaluation envelope](native-subscription-limits.md#approved-evaluation-envelope)
is separate from public installation defaults.

## Capability, completion and cancellation

Capability evidence is keyed by deployed build, full graph and policy revision,
limits profile, exact protocol/harness/settings versions and model/fallback path.
Test the full path for streaming UTF-8 and SSE fragmentation, multiple tool-call
IDs and fragmented JSON arguments,
tool-result continuation, reasoning/model settings, structured output, truncation,
refusal and cancellation. Every possible fallback must satisfy required
capabilities. Endpoint presence is not that proof.

Structured JSON has two distinct claims: validated JSON after generation versus
provider-enforced strict schema. Validate every completed output against its schema
before accepting it. Prompt-only JSON translation does not satisfy a strict-schema
requirement, even if one example happens to validate. Do not silently downgrade.

The response state is `awaiting_output` → `streaming` → `completed`,
`failed_before_output`, `partial_failure` or `cancelled_unknown`. HTTP 2xx opens a
stream; it is not completion. Complete schema-valid tool arguments and a trustworthy
successful original protocol terminal are required before dispatching a tool;
tool effects also require Gaffer's separate
execution authority and durable action identity. Invalid/incomplete arguments must
never execute. Preserve already acknowledged tool results across continuation.

For Chat Completions require each selected choice's explicit valid finish reason
and complete tool arguments; `[DONE]` alone is insufficient. For Messages require
closed content blocks, stop reason and `message_stop`. For Responses require
`response.completed` with completed status and completed output items; treat
`response.failed`, `response.incomplete`, refusal/length termination or malformed
events according to the profile, never as a successful structured result. A
terminal is trustworthy only if the selected translation path does not invent
success on truncated upstream input. The pin's flush behavior makes this a live
negative-test/upstream-fix gate, not something a downstream sentinel check proves.

| Observation | Boundary / harness consequence |
| --- | --- |
| Router internally switches connection before output, then completes | Same request and attempt; no Gaffer retry or duplicate task |
| Router returns terminal HTTP failure before body output | Classify auth/policy/protocol failures as nonretryable; 429/selected 5xx may park/back off within the task budget; no automatic boundary retry |
| Transport lost before first output | Outcome unknown: provider may have processed the request. Preserve correlation/reservation; reconcile before harness/task retry |
| Disconnect/error/EOF after any output, invalid terminal or synthetic success | `partial_failure`; retain output and tool journal, no transparent replay; reconcile continuation at the attempt boundary |
| Revocation, stop, fence/lease expiry or deadline | Reject new requests immediately, stop forwarding output, cancel upstream transport and retain terminal/cancellation evidence; no refund claim |
| Cancellation cannot prove upstream quiescence | `cancelled_unknown`; no replacement request under that reservation and no policy-change drain completion until quiescence is proved |
| Router unavailable | Park inference work visibly; capture, stored-artifact review and stop remain usable; no direct-provider bypass |

`Retry-After` can be HTTP seconds/date or the pinned router's body timestamp;
validate type/range and clamp the delay to remaining task budget. Never put raw
upstream error text/headers/body in a UI error. Use enumerated reason codes and an
opaque internal correlation ID. End-to-end cancellation requires fault tests before
headers, midstream and during fallback; local socket close alone is not proof.

Configurable provider output caps and evidence that an individual original request
ended are different capabilities. A trustworthy original provider terminal or
explicit remote cancellation acknowledgement may establish that request's
quiescence without establishing a configurable token or monetary cap. Verify that
evidence on the actual deployed path. A finite model maximum, local timeout,
process exit or EOF does not prove remote stop, release its reservation or refund
work. Quiescent failed/incomplete requests remain stopped failures, not successful
output. Preserve acknowledged tool results and provisional artifacts; durable
admission and successful completion decisions still bind scope, grant, lease/fence,
policy identity and request ID before router work or final/tool release.

## Atomic policy changes

Decision for both inspected pins: **freeze and drain at the trusted configuration
write boundary**, because immutable upstream route versions were not found. This
is a required integration capability, not an already available 9Router feature.
The admission boundary and every policy writer share one fenced epoch controller.

1. Admission holds the policy gate while checking active epoch/revision, lease and
   bounds, registering an in-flight reservation, and releasing to the fixed route.
   A write request must acquire the same gate. This removes the check/write race.
2. To edit policy, atomically mark the affected graph `draining` and stop admission.
   Existing requests continue under the frozen old graph; no writer can change it.
   Conservatively drain the whole router if graph dependencies cannot be isolated.
3. Wait for all router work to finish, including fallback and work whose clients
   disconnected. Track `cancelled_unknown` separately. A gateway count of zero,
   a deadline expiring or an unavailable management API is not proof of quiescence.
   Without sufficient evidence, keep the gate closed indefinitely. A verified
   router stop plus provider-side bound may help prove quiescence only where that
   capability exists; the native profile cannot assume such a bound. Original
   terminal or explicit remote cancellation evidence must itself be verified.
4. Once quiescent, make the reviewed configuration change, read back/validate the
   full graph, persist its document/fingerprint and increment epoch/revision.
   Issue fresh grants before reopening admission. Old grants fail even if a name
   or fingerprint has been reused. Narrowing changes use the same procedure.
5. On restart, controller crash, failed mutation/readback or unaccounted writers,
   recover closed and reconcile. A lock with only process-local state is insufficient
   across crash/restart. An out-of-band edit is an invariant breach: freeze affected
   work and record authority uncertainty; detection cannot undo widened exposure.

Enforcement must cover dashboard/API writes, imports/restores, CLI/database edits,
provider-node/alias/proxy changes and alternate management entry points. Normal
credential refresh, account rotation and cooldown updates stay in 9Router and may
continue only within the frozen provider/model/billing graph. No Gaffer database
coupling is introduced to implement this. If all policy writers cannot be fenced,
or a reachable fallback's identity/cost cannot be enumerated, the route cannot be
admitted for policies needing this guarantee. A preflight hash plus polling is not
an acceptable substitute. A future upstream immutable-version API needs separate
evidence and must not silently change this contract.

Changing limits profile requires explicit operator authority, a new policy/grant
identity and this same freeze/drain procedure, including changes from strict to
native local limits. An assessor/model cannot authorize the change. Neither local
timeout nor process exit permits replacement, route swap, refund or drain completion
while remote work remains `cancelled_unknown`/unreconciled.

The pinned 0.5.75 [executable authority experiment](../evidence/router-authority.md)
demonstrates counter expiry without completion, disconnect accounting before local
abort and combo mutation while a request remains pending. It records the required
live integration; it is not a working live authority adapter or provider test.

## Private management and status projection

Use a private authenticated ingress independent of dashboard login/Host headers.
The status adapter's identity has exact GET-only access to `/api/providers`,
`/api/combos` and `/api/models/availability`, bounded to configured router/route
scope. Disable redirects. A separate configuration authority owns writes; neither
workers nor the status adapter has it. Deny all other paths and methods, including
`GET /api/usage/{connectionId}`: that GET is an active operation. The adapter cannot
read router storage, provider files, key endpoints or usage aggregates.

Raw responses remain in backend memory only, with bounded read size/time; never
dump them into logs, caches, traces, exceptions, browser payloads or model context.
Project a newly constructed object using exact field allowlists. Redaction of a
copied object is insufficient. Discard all `providerSpecificData`, names/emails,
arbitrary error text, raw key data (including object keys), URLs, unknown nested
objects and fields. Match upstream connection/provider/model identifiers against
trusted configuration and emit local opaque references, never unchecked strings.

The canonical outgoing shape is
[status.schema.json](../../tests/fixtures/9router/status.schema.json), with
`additionalProperties: false` at every object boundary. Required fields are:

- Schema version, router/route references, policy revision and projection time.
- Route `state`: `available | degraded | exhausted | unknown`; and `readiness`:
  `ready | not_ready | unknown`, an enumerated reason, source observation time and
  expiry. The projection's creation time never refreshes an old observation.
- Per-connection local reference/provider reference and `state`:
  `eligible | blocked | exhausted | disabled | unknown`, source, observation time,
  expiry and optional sanitized `retry_after` timestamp (null when unknown).
- A capability observation with the same provenance/freshness fields; `unknown`
  until a route/protocol/harness conformance record exists.

For historical profile `rickicode-0.3.98-69724d8`, parse only known scalar fields
from matching `connections`: `id`,
`provider`, `isActive`, `routingStatus`, `authState`, `quotaState`, `healthStatus`,
`lastCheckedAt`, `nextRetryAt` and `resetAt`. Negative model observations may use
`models[].{connectionId,provider,model,status,until}` from the availability GET.
Never copy `lastError`, `reasonDetail` or `connectionName`. Invalid containers/types,
duplicate identity, missing provenance, future observation time, expired evidence
or unexpected enum in an inspected field invalidate that observation to `unknown`.
Unrecognized extra upstream fields are ignored, not passed through. Outgoing schema
violation fails the projection closed and serves a minimal schema-valid unknown.

For selected profile `decolua-0.5.75-17c4cc7`, initially permit only mapped `id`,
`provider`, boolean `isActive`, `testStatus` (`active | error | unavailable | unknown`)
and ISO `lastErrorAt` from provider GET. `active` and missing status produce
`unknown`, never `eligible`. `isActive=false` can produce `disabled` as a fresh
configuration observation at the adapter's authenticated retrieval time. For
`error`/`unavailable` with valid fresh `lastErrorAt`, project only `blocked` with that
source time and `retry_after=null`; the label does not establish quota exhaustion.
Without that timestamp, remain unknown. Unknown enum/type/identity invalidates the
observation. Do not consume incidental historical canonical fields in this profile.
The initial implementation may omit availability GET; consuming its model-specific
cooldowns needs separate fixture coverage and never creates positive readiness.
An implementation may explicitly declare only the disabled/unknown subset supported;
unsupported negative observations remain unknown until the matching corpus is implemented.
Its `until` cannot be used as the time an error happened. Profile selection is
explicitly keyed to source/artifact identity; unknown builds fail closed. The public
outgoing v1 shape stays the same, and fixtures name their input profile.

Use router-reported canonical states in the historical profile as observations, not a recreated account
selector. Preserve known negative states conservatively; conflicting eligible and
blocked/exhausted observations do not imply readiness. A model lock may make that
connection exhausted for the scoped route without implying its other models are
exhausted. Lack of an availability entry and an empty discovery list say nothing
positive. Model discovery is optional inventory outside the passive status poll and,
for the selected release, may actively refresh provider credentials.

Default freshness is at most 30 seconds for live route/connection observations;
clamp to any shorter source expiry. Capability conformance evidence may remain
valid for its exact deployment/policy/harness tuple until that tuple changes, but
does not extend live availability freshness. `ready` requires an accepted deployment,
enforced active policy epoch, fresh successful bounded inference evidence, required
capability proof and no newer contradicting status. Success proves observed route
availability, not remaining allowance. A known denial or terminal failure yields
`not_ready`; absent/stale/malformed/unattributable observations yield `unknown`.
Unknown availability can permit a separately authorized bounded probe after the
deployment/authority/capability gates; it never grants authority or unlimited quota.

Do not expose remaining quota/cash estimates in v1: the pin's passive paths do not
establish reliable route-wide remaining capacity. If a future adapter adds them,
version the schema with source, units and freshness. Preserve separate subscriptions
as two observations without adding their balances. Actual provider/model/connection,
usage and fallback may be attributed to an attempt only with a proven correlation
signal; otherwise keep them unattributed. Do not infer a selected connection from
timing, connection priority or the shared router key. Usage may be estimated by the
pin, so unclassified totals must not be reported as exact provider accounting.

## Examples, verification and handoff

The corpus includes strict/default and explicitly authorized native local-limits
logical examples, request/continuation examples, synthetic hostile management
payloads, status projection pairs and protocol/policy failure traces.
These are reproducible specification inputs, **not live probe recordings**.
Run `npm --prefix tests/fixtures/9router ci` then
`npm --prefix tests/fixtures/9router test` to validate the closed output schema,
negative schema mutations, expected projections and fixture references. Run the
repository docs build/check separately. The corpus checker is not a status adapter,
forwarder, network sandbox, authentication test or concurrency proof.

Issue #3 implements the attempt boundary and drain/fencing experiment; #4 the
allowlisted status adapter; #5 OS containment. Issues #6–#8 prove the first harness,
restricted coordinator flow and real two-subscription continuity. They must consume
the failure corpus and record actual deployment results. Required deployment
evidence includes authentication negatives, management/egress denial, bounds,
partial-stream/strict-schema/cancellation probes, concurrent policy-edit races and
same-provider connection fallback. Missing capabilities remain explicit blockers;
draft contract review can proceed without declaring those dependent gates passed.
