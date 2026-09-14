# Router authority extension candidate evidence

For [#77](https://github.com/korallis/letmecook/issues/77), 14 September 2026.
The [candidate and reproduction instructions](../../experiments/router-authority-extension/README.md)
implement an enforced extension at actual 9Router execution and SQLite seams.
The [recorded result](router-authority-extension-run.json) uses synthetic loopback
upstreams and credentials inside a disposable isolated container. This advances
[the #74 integration gap](router-authority.md); it does not certify an existing
deployment or complete live #6/#7/#8 acceptance.

## Identity and method

The public source is `decolua/9router` at
`17c4cc76877bd1755030a8414f8d0083f48dcccf`, package version 0.5.75. The loader
verifies 1,252 JavaScript, JSON and WASM files before evaluating upstream modules.
Exact single-anchor overlays and source digests are committed; a modified source
file is refused before evaluation.

The run used Node 24.21.0, Linux ARM64 and native `node:sqlite`. Dependencies use
the committed npm lock with lifecycle scripts disabled and optional packages
omitted. The [image identity](../../experiments/router-authority-extension/runtime-identity.json)
and Dockerfile describe this candidate runtime. Its resolved dependencies differ
from the installed npm artifact; this is not an installed-build attestation.

Real `handleChat`, chat core, account/combo fallback, Default/Base/Codex executors,
OAuth refresh, repository/import methods and SQLite execute without behavioral
mocks. Node import adaptation resolves bundler aliases and one actual CommonJS
export. The upstream custom server and background boot loops do not run.

Requested isolation: network disabled, nonroot UID/GID 1000, all capabilities
dropped, no new privileges, read-only root/source/experiment, private cgroup/IPC,
init, one CPU, 768 MiB memory/swap, 64 PIDs, 256 file descriptors, no core dump,
bounded tmpfs, and a 180-second outer timeout with one-second TERM-to-KILL allowance.
Only synthetic loopback HTTP runs inside the container. These requested controls
are not a new worker-isolation certification.

## Observed behavior

| Actual integration case | Observed result |
| --- | --- |
| Admission and every supported physical send | Durable receipts bind ID, boot, generation, revision, route, connection and each Base/Codex/refresh ordinal. |
| Stock account and model fallback | Compatible two-account fallback and native Astra/account → Sol fallback retain all operations and synthetic workspace attribution. |
| Repository/import/raw-SQL writers | Active policy changes roll back. A drained private writer advances generation and must match full readback before reopening. |
| Disabled maps and reserved database keys | All stored KV rows are rejected before map reconstruction. Repository and direct-adapter writes for four disabled maps and six ordinary/reserved keys roll back; unsupported privileged writes leave admission closed. |
| Admission/writer races | New admission is denied during mutation. Existing admitted work can finish during drain; late writers fail after their generation ends. |
| Delayed ingress and unknown IDs | Old-generation ingress is rejected before dispatch. Unknown IDs never become quiescent; presenting one to quiescence durably blocks replacement. |
| Mutation exception / incorrect readback | Both remain closed. Explicit trusted replacement and successful readback are required to recover. |
| Retry/cancel/fence | Every supported retry is recorded. Cancellation or fencing during retry wait, before headers or during refresh prevents further sends, retaining remote uncertainty when needed. |
| Router-owned native refresh | Proactive and 401 refresh execute stock code. Token persistence requires a corresponding terminal receipt and unchanged workspace/policy projection. |
| Crash/restart | A child with admitted upstream work is killed by SIGKILL. Restart is closed with a new generation and its unresolved receipt remains non-quiescent. |
| Original stream terminals | Evidence is observed before Codex peek and translation. Partial, malformed, contradictory and oversized streams remain unknown; native failed/incomplete remain distinct from completed. |
| Bounds and queued output | Oversized/stalled streaming ingress is denied before upstream. Oversized native output is physically stopped; cancellation discards queued output. |
| Original error and refresh cleanup | Oversized, malformed UTF-8/JSON, wrong content type and invalid refresh bodies abort the request and close the original response after one physical send. Neither later retries nor credential persistence occur. |
| Deadline and cancellation before original EOF | Stalled error JSON and Codex's first-event peek obey the real 30-second request deadline. Cancelling complete JSON without EOF retains unknown evidence; local disposal never supplies remote quiescence. |
| Native streamed tool consistency | Added tool identity, incremental arguments and complete snapshots must agree. The review's identity/delta drift and duplicate argument keys reject before successful output or terminal settlement. |
| Request envelope and ordinary continuation | Both profiles reject 38 unsupported body shapes and 47 header/alternate-metadata cases with zero sends and no receipt. Five positive requests per profile preserve ordinary tools, matched continuation, complete tool lists, HTTP metadata handling and router API authentication. |
| Unsupported graph | Enabled default adapters, unsupported native live profiles, MITM bypass and non-loopback/unclassified production endpoints fail closed. |

Fifty original-stream observer tests pass in the locked isolated runtime, covering
binary UTF-8/CRLF splits, two interleaved function calls, truncation, duplicate JSON
keys, changed response/model IDs, conflicting terminal/error fields, incomplete
tool arguments and resource limits. The native correction also reconciles tool
identity and text/function/custom/refusal/reasoning deltas against complete
snapshots. The root coordinator independently ran the contributor's 30 native and
20 Chat cases before integration; the integrated suites were then rerun.

An early independent audit drove named regressions for forged `kv.settings`
shadowing, upstream MITM branches with untracked sends, API-key changes outside
drain, protected-table casing, queued output after cancellation and weak Chat
terminal validation. These were material candidate defects, now covered by the
recorded rejection cases; they are not presented as previously passed behavior.

Fresh review of `c245c899c8d2562cb771f1f71258477fbaa88f77` found two further
blocking defects after its original 22 integration scenarios and 40 observer tests
passed. Oversized non-2xx responses triggered four stock retries and left all four
original responses open after handler completion; late cancellation closed none.
Native tool identity and streamed arguments could disagree with completed items,
yet the receipt incorrectly claimed `provider_completed` and quiescence. The
[original failed observations](router-authority-extension-review-c245c89.json)
retain the synthetic response/receipt records and reproduction artifact digests.

The correction owns each physical transport until original EOF or local disposal,
aborts and cancels every size/decode/validation failure, fences later retries, and
retains active cancellation/deadline bookkeeping until outstanding local cleanup
settles. Native item identities and per-index content channels are now reconciled
through incremental and completed events. The recorded run includes 40 scenarios,
including 12 actual HTTP cleanup/deadline cases and three native review regressions.
Closing the synthetic HTTP response is local cleanup evidence; affected remote
operations remain unknown and block replacement. A new independent final review
of the corrected commit is still required.

The next independent review of `4294e66649a981c9d227d8e1323ac9eaee4efe85`
found a P2 policy-projection bypass: assigning stored `__proto__` keys into plain
objects hid alias and pricing rows from the snapshot, while actual router lookups
resolved an inherited alias and changed effective pricing. No unapproved inference
was demonstrated. The [failed observations](router-authority-extension-review-4294e66.json)
retain that distinction. Since every KV-backed map is disabled in the supported
profile, the projection now rejects any stored row before constructing maps.
The added actual-repository regression covers four maps and six key forms through
both repository and direct-adapter writes (48 rollback checks), unchanged real
alias/pricing lookups, allowed health writes, and four privileged-writer readback
failures that leave admission closed. The full integration replay also retains
successful stock native refresh behavior. This correction again requires a new
independent final review.

The third fresh review at `461cb2f372d88689dd6aed6cd3e1fe3ed060bd7e` found
another P2: native requests accepted function `strict:true`, tool choices (`none`,
`required`, named function and unsupported hosted choice) and `temperature:0`,
then returned completed receipts although the actual upstream request omitted
those controls. The [original observations and exact reproduction/log hashes](router-authority-extension-review-461cb2f.json)
are retained. This demonstrated lost controls, not unapproved provider/account/model
selection, hosted-tool execution or false remote quiescence.

The common request envelope now rejects explicit temperature/tool choice and uses
closed tool/function/call shapes before durable admission. It rejects fields and
shapes the pinned path would rewrite, matches historical call IDs/results, and
uses the actual pinned schema helper to detect removed pattern constraints. It
does not implement general JSON Schema validation or #79's worker tool allowlists.
The new native and compatible HTTP regressions each reject 38 request shapes with
zero sends and unknown request IDs, then accept ordinary tools and matched two-call
continuation with exact relevant upstream wire assertions. Those positives include
preserved escaped literal patterns and a property actually named `pattern`. All
50 observer tests were rerun after the envelope correction. A new independent
final review is still required for this corrected commit.

The fourth fresh review at `88f82c421b6da88d81bc0a0a2ee6aca5bf51b53e` found
a grouped P2 header/metadata defect. Codex client headers selected native passthrough,
so the physical request lost system/user content and historical calls/results;
Claude client headers caused both profiles to remove a declared WebSearch function
when an Exa MCP search function was present. These requests still returned HTTP 200
and completed receipts. The [original observations and five reproduction/log hashes](router-authority-extension-review-88f82c4.json)
are retained unchanged. This demonstrated payload/control loss, not unapproved
provider/account/model selection, hosted-tool execution or false remote quiescence.

The correction validates a closed header envelope and reconstructs minimal stock
headers, preserving only router authentication, Gaffer correlation and canonical
content negotiation. Validated automatic HTTP metadata is discarded; other client,
session, provider and token-saver hints reject. Caller-provided `clientRawRequest`
is also refused before admission so separate metadata cannot bypass reconstruction.
The source audit covers `clientDetector`, `sessionManager`, chat raw-header/accept/
token-saver handling and the native executor's session/header paths. The exact
accepted transport forms are in the candidate README; no general CLI ingress is
claimed. Per profile, 47 negative header/metadata cases produce zero sends/no receipt,
early rejection cancels a stalled body, and five positive requests preserve the
tested system/user/call/result/tool fields, complete WebSearch/MCP declarations,
normal Node metadata and alternate API-key authentication. Existing actual HTTP
gateway tests retain their automatic headers. Full integration and 50 observer
tests were rerun; a new independent final review remains required.

## Remaining gates

Historical experiment status below is retained. The subsequent operator-approved
[#81 amendment](../contracts/native-subscription-limits.md) resolves the optional
native limits product choice and records bounded evaluation authority. It changes
no runtime, result or deployment: native implementation, reviewed deployment
controls and actual live conformance remain outstanding.

Both profiles report `liveAdmission:false`. Native registry injection changes only
response/token endpoint literals to loopback. Actual Codex removes all configured
output-token limits and the backend sees none; its policy records no provider
output bound. Local byte/time cancellation is not remote-generation/stop proof.

Production still requires the operator integration/build choice, reviewed endpoint
and billing enrollment, exclusive process/storage ownership, authenticated private
ingress, deployment attestation, credential/data authorization and live provider
conformance. At this run, native admission also required resolution of the mandatory
provider output-bound product choice. No existing deployment, active credentials, private configuration,
real provider requests, publication or human baseline were used.

The earlier boundary's `FrozenAuthority` remains deliberately fixture-schema-only.
The shared [#79 integration](https://github.com/korallis/letmecook/issues/79) must
explicitly version it, bind IDs/headers/request shape to this private interface,
consume correlated original receipts and require successful original completion
before accepting final/tool output. #6/#7 then consume that integration. Quiescence
alone also includes provider failure/incomplete outcomes. This candidate is not
silently substituted as a live adapter. Fresh final review and observed PR checks
precede any separately authorized merge; no merge or deployment is included here.
