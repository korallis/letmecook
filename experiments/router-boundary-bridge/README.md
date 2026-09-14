# Synthetic router boundary bridge

[Issue #79](https://github.com/korallis/letmecook/issues/79) joins the accepted scoped
boundary to [#77's pinned router authority](../router-authority-extension/README.md).
This experiment executes real 9Router modules, SQLite, account/model fallback and
OAuth refresh against synthetic original HTTP. It is not a deployed router,
provider-capability certificate, completed #6/#7 harness/planner, or application core.
Both profiles are synthetic and have `liveAdmission:false`.

## Reproduce

Use the accepted public router checkout at
`17c4cc76877bd1755030a8414f8d0083f48dcccf` and the locked image built by #77. The
runner verifies its image ID against `router-authority-extension/runtime-identity.json`
and the exact selected engine/kernel profile in `experiments/isolation/profile.json`.
It refuses a different host profile; hosted unit checks are not containment evidence.

```sh
npm ci --prefix experiments/inference-boundary --ignore-scripts
npm ci --prefix experiments/router-boundary-bridge --ignore-scripts
npm --prefix experiments/inference-boundary run check
npm --prefix experiments/inference-boundary test
npm --prefix experiments/router-boundary-bridge run check
npm --prefix experiments/router-boundary-bridge test
GAFFER_BRIDGE_DOCKER_CONTEXT=YOUR_CONTEXT \
GAFFER_BRIDGE_ROUTER_SOURCE=/ABSOLUTE/PUBLIC/PINNED/ROUTER \
GAFFER_BRIDGE_IMAGE=gaffer-router-extension-deps:0.5.75-locked \
node experiments/router-boundary-bridge/run.ts /ABSOLUTE/OUTPUT.json
```

`GAFFER_BRIDGE_CASES=compatible:tools,native:tools` selects a diagnostic subset and
reports `selected-cases-passed`, never a full proof. Run the original fixture proof
to another output path with `node experiments/inference-boundary/prove.ts OUTPUT`;
its committed earlier evidence remains unchanged.

The dependency image contains Node 24.21.0 and native SQLite. Public upstream code
executes only in bounded, disposable network-none gateway containers behind #77's
source-verifying loader. Host type/unit checks execute only first-party code. No
provider or publication credential, private repository or existing router is used.

## Versioned adapter contract

`types.ts` preserves `FixturePolicy` schema 1 / `chat-text-tools-v1` and adds the
schema-2 `RouterPolicy` family validated by `router-policy.ts`:

- `router-chat-text-tools-synthetic-v1`: compatible original Chat SSE with the
  pinned router's translated Chat output.
- `router-native-chat-translation-synthetic-v1`: original Codex Responses SSE,
  translated through the pinned Chat ingress. This narrow profile refuses mixed
  native/compatible graphs. It does not implement a worker Responses codec.

The complete sanitized projection retains its own schema 1. Validation binds the
synthetic deployment ID, router boot/generation/revision, full graph digest,
unique selected route ID/name and fixed operation envelope. Connections include
all projected candidates, but receipt operations require an active approved
connection. Local policy epoch and journal ownership generation remain separate
from router generation. No account selection, refresh or fallback logic moves
into Gaffer.

The trusted bootstrap supplies the approved graph, source/overlay/runtime
identities and synthetic router credential. `RouterAuthority` accepts only an
injected private control object; it exposes no writer callback. Its synchronous
`state/snapshot/receipt/state` reads occur before yielding in the exclusively
owned router process. A changed state or projection fails closed. Active admission
requires router phase active; already admitted completion may finish during
ordinary draining with unchanged identity. Closed/mutating/fenced state prevents
new output. Separate-process control transport is outside this profile.

The worker can call only `POST /v1/chat/completions` with its scoped token. The
schema-2 body is the existing bounded text/read_file contract, deliberately
mapped from `max_completion_tokens:N` to `max_tokens:N` with no widening. Every
explicit `tool_choice`, temperature, strict tool flag, stream option, effort or
unknown field is rejected. Tool-only assistant history uses null content;
historical call IDs have the router's whole-string 64-character bound. This
accepted request envelope does not cover OpenCode's current captured profile.

The private router hop reconstructs Host `127.0.0.1`, router authentication and
exactly `x-gaffer-request-id`, `x-gaffer-generation`, `x-gaffer-revision`, plus
bounded HTTP content metadata. Task/attempt/grant data stays in the private
journal. Worker-supplied authority/client/session headers cannot pass the boundary.

Native Codex demonstrably removes the requested output cap. Its projection keeps
`providerOutputTokens:null`, `providerOutputBound:false` and
`nativeLiveAdmission:false`. Byte/time/cancellation controls do not substitute for
provider-generation bounds. The accepted finite-output contract remains unchanged;
native live admission is still denied. Compatible wire preservation is also not
proof of a real provider's adherence to a cap or billing identity.

## Durable request and completion join

Schema-2 reservations save the exact policy, grant binding and outgoing request
digest before sending. A separately fsynced `send_possible` transition precedes
transport construction. Only a durable `reserved` state proves no send began.
Absent receipts after possible send remain unknown across recovery; local EOF,
process exit, cancellation and a caller's `neverForwarded` hint cannot erase them.
Existing fixture history cannot be silently reopened as router-backed history.

`RouterAuthority` polls nonmutating receipt reads. Calling #77's `quiescent(ids)`
with an absent ID permanently records missing evidence, so visibility waits never
do that. Known receipts validate exact ID/identity/route and contiguous operation
ordinals, supported terminals, lifecycle fields and approved provider/model/active
connection tuples. A known finished receipt with zero operations (for example an
API-key rejection) is quiescent failure. Refresh success alone is not inference
success. Any earlier unknown operation survives later success; otherwise the last
inference operation must be successful before local completion can be accepted.

The boundary keeps tools, finish frames and DONE buffered. It first validates
local EOF and the entire final output-byte budget, then waits for the matching
original success receipt. It fsyncs a decision containing the request join,
original receipt digest/operation summary and local completion digest before
release. Snapshot/audit decision references include only confirmed persistence or
validated disk recovery, not a success temporarily present in memory during a
failed write. Records are bounded and admission stops rather than evicting required
evidence.

The receipt-gated compatible codec permits exactly one additional DONE after a
valid finish because the pinned translator duplicates it. More duplicates or new
semantic data fail. The native translated codec prepares canonical completion
from finish plus clean EOF because that translator omits DONE. Both still require
original receipt success. The original fixture parser stays strict.

Immediately before terminal/tool writes, the boundary rechecks grant/lease/fence,
current authority and receipt cancellation state, including after persistence
waits. A cancellation can change a completed router receipt without changing its
generation. Its prior original success remains historical evidence but does not
permit new delivery. Backpressure interruption withholds subsequent terminal
output. Text already streamed is provisional. Consumers execute no tool or accept
no final result until canonical completion and clean EOF; the trusted supervisor
joins the returned request ID to its private durable decision.

Delivery is recorded separately from `validated_success`. A crash after a decision
but before release leaves delivery unobserved. Recovery never replays tools or
inference. Old receipts may retire old work while the new boot remains closed;
receipts alone cannot recreate lost local protocol validation. The proof supervisor
removes a stale boundary owner lock only after observing that exact child exit.
Automatic reopening under a new policy/boot and downstream effect acknowledgment
are not implemented here.

`replaceFrozenPolicy()` explicitly returns `replacement_read_only` and invokes no
mutation callback. `PolicyGate.drainAndReplace()` closes/refuses this profile;
router policy/phase/generation remain unchanged. Trusted synthetic bootstrap and
isolated mutation-failure scenarios use #77's private capability separately. This
is not bridge replacement support.

## Measured topology and lifecycle

`m0-router-boundary-gateway-768m-uds-v1` is an explicit resource/image variant of
the selected Linux profile: gateway 768 MiB/one CPU, worker 128 MiB/half CPU, no
swap above the memory limit, 64 PIDs, bounded tmpfs and logs. It retains network
none, nonroot, read-only root, separate namespaces, dropped capabilities, no new
privileges, default seccomp and effective file/core limits. The runner checks
actual inspect values and records cgroup peaks/OOM events and final process state.
It does not reuse the earlier fixture gateway's 128 MiB claim.

Only the boundary socket is shared with the worker. The worker receives its scoped
grant on stdin. Router source, boundary/control code, configuration, database,
private router socket, journal and synthetic credentials stay in the gateway. The
worker mounts only its standalone proof client; containment probes verify absent
trusted mounts/private paths, denied management and unavailable direct TCP.

A trusted supervisor runs router + boundary in a child process and preserves
private temporary state across deliberate SIGKILL/restart checkpoints. These are
combined-runtime crashes, not a claim of an independent boundary process. The
stock SQLite adapter's SIGTERM handler can exit before bridge cleanup; the
supervisor's cooperative stop therefore uses private SIGUSR2. Raw crashes remain
uncertain. Stop latches prevent subsequent worker/recovery starts. Deterministic
container/volume names are recorded before creation, Docker mutations settle
before ownership reconciliation, and every owned resource is verified removed.

The backpressure case deliberately injects a withheld drain notification on an
actual HTTP response; the receipt-delay case delays visibility of an actual stored
receipt. They do not claim kernel-buffer saturation or a delayed SQLite commit.
The persistence cases obstruct the actual journal temporary file and inject a
failure after rename but before parent-directory sync. Neither permits terminal
output or a confirmed durable audit reference. In the latter case, a subsequent
validated disk recovery can observe the decision but never replays its output.
Proof fields distinguish the in-process `gateSnapshot` from `journalOnDisk`;
they may differ after an unacknowledged write. Synchronous SQLite calls cannot be
preempted by JavaScript timers; the external bounded supervisor provides process
termination and conservative crash evidence.

See [the evidence record](../../docs/evidence/router-boundary-bridge.md) for observed
runs, failures and downstream #6/#7 work. Independent final-head review and any
merge remain separate from this draft's passing synthetic results.
