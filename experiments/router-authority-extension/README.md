# Pinned 9Router authority extension candidate

For [issue #77](https://github.com/korallis/letmecook/issues/77). This disposable
candidate wires authority into actual public 9Router 0.5.75 source at
`17c4cc76877bd1755030a8414f8d0083f48dcccf`. It executes the real chat handler,
translation core, account and combo fallback, Base/Default/Codex executors,
credential refresh, repository/import functions, and native SQLite adapter. The
upstreams and credentials are synthetic. No existing router or provider account
is used. This is **not** completed live acceptance for #6, #7 or #8.

## Reproduce in disposable isolation

Supply a separate public checkout at the commit above. Do not point this at an
installed router, private configuration, or a credential export. Source and
dependencies execute only inside the container. Building the image downloads
public dependencies from the committed lockfile with lifecycle scripts disabled.
The resulting dependency tree is the candidate's reproducible runtime, **not** a
rebuild/attestation of the operator's installed npm artifact. The recorded local
runtime identity is in `runtime-identity.json`.

```sh
extension_context=YOUR_DOCKER_CONTEXT
extension_source=/ABSOLUTE/PATH/TO/PINNED/PUBLIC/9ROUTER
extension_code=/ABSOLUTE/PATH/TO/GAFFER/experiments/router-authority-extension
docker --context "$extension_context" build \
  --tag gaffer-router-authority-proof "$extension_code"
docker --context "$extension_context" run --rm \
  --name gaffer-router-authority-proof \
  --label dev.gaffer.authority-extension=manual \
  --network none --user 1000:1000 --cap-drop ALL \
  --security-opt no-new-privileges=true --read-only --init \
  --cgroupns private --ipc private --cpus 1 \
  --memory 768m --memory-swap 768m --pids-limit 64 \
  --ulimit nofile=256:256 --ulimit core=0:0 \
  --mount "type=bind,source=$extension_code,target=/probe,readonly" \
  --mount "type=bind,source=$extension_source,target=/router-source,readonly" \
  --tmpfs /tmp:rw,nosuid,nodev,size=180m,uid=1000,gid=1000 \
  --env HOME=/nonexistent --env NODE_OPTIONS= \
  --pull never gaffer-router-authority-proof \
  timeout --signal=TERM --kill-after=1s 180s \
  node /probe/run-in-container.mjs > extension-result.json
```

The suite creates only loopback HTTP listeners inside a network-disabled
container. Its child processes use separate temporary SQLite databases; the
crash/restart pair shares one. The parent records an actual SIGKILL and verifies
that the next boot is closed and the unresolved operation remains unknown. The
runner emits only redacted JSON results on stdout and scenario progress on stderr.
It also verifies a modified source file is refused before any router evaluation.
Requested container controls do not establish a new worker-isolation certification.

Run the standalone original-stream observer tests inside the same image with
`node --test --test-concurrency=1 /probe/responses-terminal.test.mjs /probe/chat-terminal.test.mjs`. JavaScript syntax checks are
available with `npm --prefix experiments/router-authority-extension run check`;
they execute no upstream code and require no install for this experiment.

## Integration seams and durable protocol

`loader.mjs` verifies all 1,252 source JavaScript/JSON/WASM files in
`source-lock.json` before evaluating any upstream module, then applies exact
single-match overlays from `patches.mjs`. It resolves upstream's bundler aliases
and converts one `node-machine-id` named import to an equivalent real CommonJS
default import. No router dependency or database function is mocked.

| Pinned seam | Enforced extension behavior |
| --- | --- |
| `src/lib/db/driver.js` | Wrap the adapter before either async or sync getters expose it. Keep the raw SQLite object private; reject arbitrary SQL, protected tables and mutating read calls. |
| All repository/import writes through that adapter | Each write runs in a SQLite transaction and must retain the complete approved policy projection, or carry the current private writer generation while drained. Credential identity has a separate private comparison. |
| `src/sse/handlers/chat.js` | Bound/read the incoming body, require request ID/generation/revision, admit durably, then invoke the original handler. Preserve context through stock account/model selection. Deny new requests during drain. |
| `BaseExecutor.execute` and `CodexExecutor.execute` | Verify selected provider/model/connection against the frozen graph. Fence every Base URL attempt and both Base and Codex retry waits. |
| `proxyAwareFetch` before its actual transport path | Persist one operation before every supported physical send; enforce exact endpoint, no redirects/proxies/MITM bypass, and combine request cancellation with the original fetch signal. |
| Original response body before Codex peek or any translator | Persist protocol terminal evidence separately from local EOF/cancellation. Failed and incomplete native responses may establish remote terminality but never successful output. |
| Router OAuth refresh and credential update functions | Attribute the actual refresh HTTP operation. Permit token persistence only after a same-request, same-connection refresh terminal and unchanged workspace/policy projection. |

The authority tables share the router's SQLite database with WAL and
`synchronous=FULL`. Every boot advances the durable generation and starts closed.
Previous unresolved receipts survive; neither elapsed time nor stock active
counts retire them. A receipt is bound to request ID, boot, generation, revision,
route and every executor/refresh operation. Unknown IDs return `quiescent:false`.
An unknown ID presented to the trusted quiescence API is retained as durable
missing evidence and blocks replacement; this candidate provides no automatic
recovery that discards such uncertainty.
`quiescent(ids)` also checks the whole router. A failed/partial operation is never
settled merely because a later fallback succeeds.

All KV-backed policy maps are disabled in the current profiles. Readback checks
the raw stored row inventory and rejects every row before reconstructing maps;
reserved keys such as `__proto__` cannot disappear through plain-object assignment.
An active repository write rolls back, and a privileged replacement containing
such rows stays closed. Supporting any of these maps later requires an explicit
profile extension with complete key-safe decoding and effective-policy evidence.

Each original physical transport registers a disposer shared across executor and
refresh contexts. Size, UTF-8, JSON/schema, content-type or stream-validation failure
aborts the request and cancels the original reader/body before releasing ownership;
later retries cannot send. Handler completion aborts any remaining physical work
and retains its deadline/cancellation bookkeeping until local disposal settles.
An abort-induced reader completion is not original EOF evidence. Unknown operations
remain unknown even after the synthetic HTTP server observes connection closure.

The native observer retains immutable item/tool identity and accumulates content
by item and content/summary index. Explicit channel/part/item completion and final
snapshots must agree with all observed deltas. Repeated matching snapshots validate
existing content without appending it. Changed call IDs/names/namespaces, conflicting
tool arguments, duplicate JSON argument keys and dropped/moved content channels
fail closed. Consumers still withhold final/tool acceptance until original success.

The trusted in-process API is exported by `overlay/authority.mjs`:

```js
const control = authority();
const state = control.state(); // boot, generation, phase, revision, sanitized policy
control.receipt(requestId);    // durable original-operation and local-stop evidence
control.quiescent(requestIds); // unknown or any outstanding work => false
await control.replace(state.generation, nextRevision, reviewedFullPolicy,
  () => routerRepositories.importDb(reviewedRouterConfiguration));
```

Replacement first durably closes admission, then requires all known work to be
terminal. It advances the generation, executes only the trusted writer, and
requires exact sanitized readback before reopening. Mutation exceptions and
readback mismatch remain closed; stale or late writers cannot commit under a
new generation. `cancel(id)` and `fence()` stop later dispatch and output release.
Existing authorized work may finish during drain; fencing/cancellation revokes it.
The methods are private process capabilities, not HTTP endpoints for workers.
The tested ingress headers are `x-gaffer-request-id`, `x-gaffer-generation` and
`x-gaffer-revision`, alongside the router's own API-key authentication. Only a
trusted boundary may supply them through exclusive ingress; they are not grants
when supplied by an arbitrary worker. `digest(policy)` exports the deterministic
SHA-256 fingerprint of the retained full sanitized policy document.

Admission also closes the header envelope before reading the body. It accepts
only POST, at most 32 header entries totaling 8 KiB, and the forms below. It creates
a fresh Request containing canonical JSON/SSE content headers, the three Gaffer
correlation headers, and the supplied router API credential. No other incoming
header reaches stock client detection, request normalization or `credentials.rawHeaders`.
Caller-supplied `clientRawRequest` metadata is refused; stock reconstructs it from
the admitted Request. Rejection cancels the incoming reader without waiting for a
stalled body, and performs no inference admission or send.

| Incoming header group | Accepted forms |
| --- | --- |
| Router authentication | One of `authorization: Bearer <token>` or `x-api-key: <token>`; both together reject. The router still validates the credential. Missing/invalid credentials return its 401 without a physical send. |
| Authority correlation | `x-gaffer-request-id`, `x-gaffer-generation`, `x-gaffer-revision`, with the existing ID/generation/revision checks. |
| Content negotiation | Required `content-type: application/json` (optional UTF-8 charset); optional `accept` is `*/*` or `text/event-stream`. Stock receives canonical JSON/SSE values. |
| Discarded HTTP metadata | `host` is `127.0.0.1` with optional port 1–65535; `connection` is keep-alive/close; `content-length` is the actual bounded body length; `transfer-encoding` is chunked, without content-length. |
| Discarded fetch metadata | `user-agent` is `node`, `undici` or `gaffer-boundary/1`; `accept-language` is `*`; `accept-encoding` is a comma-separated subset of gzip/deflate/br/identity; `sec-fetch-mode` is cors/same-origin/no-cors. |

Every other header or value is unsupported, including CLI/client identities,
originator, session/cache hints, provider/account controls, cookies, forwarded
headers, and token-saver overrides. `localhost` is not an accepted Host value in
this disposable profile; #79 must deliberately construct its reviewed ingress.
The actual HTTP gateway regressions retain Node's automatic transport headers.

## Exact profiles and limitations

The limits amendment in [#81](../../docs/contracts/native-subscription-limits.md)
now approves an explicit optional native local-limits contract. The runtime
profiles and historical observations below are unchanged: their mandatory cap
field and `nativeLiveAdmission:false` do not implement the new contract. Reviewed
runtime changes and deployment/live conformance remain required.

Both profiles accept the same deliberately narrow Chat request envelope: `model`,
`messages`, `stream:true`, `max_tokens`, and optional function `tools`. Explicit
`temperature` and every `tool_choice` value are rejected before admission, including
`auto`, `none`, `required` and named-function choices. The pinned Chat-to-Responses
translator drops tool choice, and Codex deletes temperature; this candidate does
not claim to preserve them. Native removal of the mandatory `max_tokens` field
remains the separately declared provider-output-bound incompatibility below.

Function declarations and historical calls have closed outer/nested objects;
`strict` (including false), hosted tools, unknown controls, trimmed/overlong names,
missing or rewritten call IDs, and malformed argument strings reject. Tool names
and call IDs use 1–64 ASCII letters, digits, underscores or hyphens. A request has
1–64 text messages, up to eight unique function declarations, and up to eight
calls per assistant message. There may be one initial nonempty system message;
non-tool text messages must be nonblank. Every historical call must have one
matching tool result before subsequent messages or admission, and call IDs cannot
repeat. Tool-result strings may be empty; assistant tool-only content is null.
Ordinary tools and a matched two-call continuation are verified on both real
executor paths, including preserved IDs, arguments, results and schema fields.

Function parameters must supply an object schema with an explicit `properties`
object. The candidate uses the pinned Codex schema helper to reject any schema it
would transform; preserved literal escape patterns and a property named `pattern`
remain accepted. This is a check for known transformations, not a general JSON
Schema validator, provider strict-output guarantee, or worker tool allowlist.
Those consumer permissions and final/tool acceptance remain in #79.

The synthetic compatible profile supports only flat named fallback combos containing
literal `openai-compatible-*` node IDs and unambiguous model IDs. Nodes and every
connection must agree on a loopback HTTP Chat Completions endpoint and API type.
Non-loopback endpoints are refused; the snapshot explicitly records `synthetic-api`
billing and `liveAdmission:false`. The snapshot
enumerates every possible connection for each model; 9Router still chooses the
account, performs fallback and owns cooldowns. Static API-key replacement requires
drain. The candidate does not establish a provider's billing identity or adherence
to a requested token cap; production endpoint/billing enrollment and live
conformance remain separate work before a live profile can be accepted.

The native **synthetic authority** profile adds the actual `cx/gpt-6-astra`,
`cx/gpt-5.6-sol` and `cx/gpt-5.6-terra` paths, with router-owned OAuth refresh and
workspace identity frozen. `GAFFER_SYNTHETIC_NATIVE=1` changes exactly two pinned
registry literals to loopback `/responses` and `/token` on port 47771. This flag
requires the disposable container and is not a production admission override.
Native admission is serial, with no unresolved earlier operations, so concurrent
refresh dedup cannot hide cross-request dependencies. Both stock refresh dedup
layers still run. Native Codex removes all configured output-token limits; the
backend observations verify that behavior and the policy records
`providerOutputTokens:null`, `nativeLiveAdmission:false`. Time and byte limits
are local transport controls, not proof of remote generation limits or stop.

Both profiles reject aliases, nested/cyclic combos, fusion, all capability pools
(including enabled empty defaults), custom model maps, helper compression,
external resources/media, hosted tools, strict structured-output claims, proxy
pools, relays, MITM bypass hosts, proxy environment variables, unrelated inference
and unknown policy settings. The candidate runtime imports `handleChat` directly;
it does not start upstream custom-server/initializeApp background loops, quota
auto-ping, dashboard, OAuth login UI, updater, relay, Go wrapper or arbitrary APIs.
Those paths are outside the supported execution entrypoint, not automatically
covered by loading one module into an otherwise unrestricted stock deployment.

The source-pinned process and database require exclusive ownership and private
ingress. A worktree, SQLite facade, or Node loader is not a security boundary
against arbitrary code with the database credentials/path. The tested OS container
provides the process/network/mount isolation; there are no worker credentials or
provider/publication credentials in its mounts. Boot fencing retains uncertainty;
it does not prove a remote provider stopped after a router crash.

`FrozenAuthority` in the earlier boundary experiment intentionally accepts only
its original synthetic policy schema. This new authority has a richer real-router
graph and is not silently plugged into that validator. Shared follow-up
[#79](https://github.com/korallis/letmecook/issues/79) must version that trusted
policy type, bind boundary IDs/headers and exact request shape, authenticate
exclusive ingress, consume correlated original receipts, and withhold final/tool
acceptance until **successful** original provider terminal evidence. #6/#7 then
consume that integration. Quiescence alone also includes provider failure/incomplete
outcomes.

The [evidence record](../../docs/evidence/router-authority-extension.md) preserves
two blocking defects found in the first independent review, the second review's
P2 reserved-key policy-projection bypass, the third review's P2 silently dropped
native request controls, and the fourth review's P2 header-selected translation/tool
removal, alongside their corrected actual-router regressions. The latter reviews
demonstrated changed alias/pricing lookups, lost request controls and changed
request payloads, respectively; none demonstrated unapproved inference or
hosted-tool execution. Passing a previous suite did not establish conformance;
the corrected commit requires a fresh final review.

No deployment was selected, configuration/credentials copied, provider inference
sent, package published, upstream repository changed, or human baseline invented.
At the time of this experiment, the operator's integration/build choice, live
credential/data authorization and provider-output-bound product choice remained
separate gates. The subsequent [#81 amendment](../../docs/contracts/native-subscription-limits.md)
records the approved profile and bounded evaluation authority; it does not change
these results or enable live admission. Runtime implementation, reviewed deployment
controls and actual live conformance remain required.
