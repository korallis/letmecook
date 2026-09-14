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
| Admission/writer races | New admission is denied during mutation. Existing admitted work can finish during drain; late writers fail after their generation ends. |
| Delayed ingress and unknown IDs | Old-generation ingress is rejected before dispatch. Unknown IDs never become quiescent; presenting one to quiescence durably blocks replacement. |
| Mutation exception / incorrect readback | Both remain closed. Explicit trusted replacement and successful readback are required to recover. |
| Retry/cancel/fence | Every supported retry is recorded. Cancellation or fencing during retry wait, before headers or during refresh prevents further sends, retaining remote uncertainty when needed. |
| Router-owned native refresh | Proactive and 401 refresh execute stock code. Token persistence requires a corresponding terminal receipt and unchanged workspace/policy projection. |
| Crash/restart | A child with admitted upstream work is killed by SIGKILL. Restart is closed with a new generation and its unresolved receipt remains non-quiescent. |
| Original stream terminals | Evidence is observed before Codex peek and translation. Partial, malformed, contradictory and oversized streams remain unknown; native failed/incomplete remain distinct from completed. |
| Bounds and queued output | Oversized/stalled streaming ingress is denied before upstream. Oversized native output is physically stopped; cancellation discards queued output. |
| Unsupported graph | Enabled default adapters, unsupported native live profiles, MITM bypass and non-loopback/unclassified production endpoints fail closed. |

Forty original-stream observer tests pass in the locked isolated runtime, covering
binary UTF-8/CRLF splits, two interleaved function calls, truncation, duplicate JSON
keys, changed response/model IDs, conflicting terminal/error fields, incomplete
tool arguments and resource limits. The root coordinator independently repeated
the same 40 tests before final integration review.

An early independent audit drove named regressions for forged `kv.settings`
shadowing, upstream MITM branches with untracked sends, API-key changes outside
drain, protected-table casing, queued output after cancellation and weak Chat
terminal validation. These were material candidate defects, now covered by the
recorded rejection cases; they are not presented as previously passed behavior.

## Remaining gates

Both profiles report `liveAdmission:false`. Native registry injection changes only
response/token endpoint literals to loopback. Actual Codex removes all configured
output-token limits and the backend sees none; its policy records no provider
output bound. Local byte/time cancellation is not remote-generation/stop proof.

Production still requires the operator integration/build choice, reviewed endpoint
and billing enrollment, exclusive process/storage ownership, authenticated private
ingress, deployment attestation, credential/data authorization and live provider
conformance. Native admission also requires resolution of the mandatory provider
output-bound gap. No existing deployment, active credentials, private configuration,
real provider requests, publication or human baseline were used.

The earlier boundary's `FrozenAuthority` remains deliberately fixture-schema-only.
#6/#7 must explicitly version it, bind IDs/headers/request shape to this private
interface, consume correlated original receipts and require successful original
completion before accepting final/tool output. Quiescence alone also includes
provider failure/incomplete outcomes. This candidate is not silently substituted
as a live adapter. Fresh final review and observed PR checks precede any separately
authorized merge; no merge or deployment is included here.
