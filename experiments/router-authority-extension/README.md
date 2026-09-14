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

## Exact profiles and limitations

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
graph and is not silently plugged into that validator. Follow-up #6/#7 integration
must version that trusted policy type, bind boundary IDs/headers and exact request
shape, authenticate exclusive ingress, consume correlated original receipts, and
withhold final/tool acceptance until **successful** original provider terminal
evidence. Quiescence alone also includes provider failure/incomplete outcomes.

No deployment was selected, configuration/credentials copied, provider inference
sent, package published, upstream repository changed, or human baseline invented.
The operator's integration/build choice and live credential/data authorization
remain separate. Live native admission additionally needs an accepted resolution
of the provider-output-bound contract gap. Keep the issue's PR draft until all
required review and integration criteria have been assessed honestly.
