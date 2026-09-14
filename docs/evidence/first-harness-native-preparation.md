# First harness: native adapter preparation

Issue #6 / draft PR #75 remains incomplete. The native adapter is prepared
against frozen issue #83 contract `70bacc54cce839e5d0b6e168dceee10977a4eecb`.
That intermediate dependency was not merged into the PR branch for preparation.
No live requests, credentials, deployment or scope start were used here.

The separate profile preserves the actual OpenCode 1.18.30 native shape:
Responses through `openai/@ai-sdk/openai`, Astra `xhigh`, summary `auto`,
`store:false`, encrypted reasoning, exact captured `apply_patch`, enabled
builtins and no provider cap. PURE and repository admission exclude external
configuration/plugins; OAuth, native LLM and WebSockets are disabled. The worker
uses the accepted 768 MiB native resource variant; the shared gateway uses
768 MiB. Provider token/monetary limits remain unavailable.

The adapter consumes shared policy/receipt authority. It validates the complete
worker binding and settings digest before launch, preserves native events and
approval rejection, and bounds a fixed public `greeting.txt` task. Its worker
relay validates OpenCode metadata without turning it into authority, then sends
the unmodified body using the boundary's allowed transport headers. A trusted
candidate check joins the actual artifact, ordered continuation, durable receipt
and decision/output digests, and native call/results; the shared gateway must
acknowledge a packet containing those links before candidate qualification.

Observed local validation: legacy typecheck and 14 tests; native snapshot
typecheck and 9 tests. The native tests cover the retained real capture plus
constructed failure cases for changed scope/binding/settings/leases, old tool
authority, metadata identity, invalid events, unknown/corrupted receipts,
premature response release, changed encrypted continuation/call IDs, forged
worker artifacts and absent durability. Test-created clocks/base/diff data are
explicitly labelled; they are not measurements of this new adapter.

The retained native capture comes from the exact frozen shared source: all 75
source digests were checked against Git blobs. It contains two actual synthetic
original sends through the shared receipt gate, a completed patch/final and
the saved shared artifact acknowledgement. It predates this adapter and is
replay input, not proof that the new adapter ran. The separate disabled-builtin
control observed `max_output_tokens:128` and zero original sends. Historical
512 MiB direct-loopback and OOM control evidence remains distinct.

The implementation and staged file list are documented in
[the native adapter README](../../experiments/harness/native/README.md).
The machine-readable preparation identity is
[first-harness-native-preparation.json](first-harness-native-preparation.json).

Remaining criteria include accepted dependency integration, actual native
adapter/gateway execution and negative cases, builtin side-effect/ambient
configuration audit, containment, final independent review and CI, selected
deployment controls, and the approved live disposable task. All intended public
consumers and deployment must be prepared/reviewed before the single aggregate
10-attempt/10-minute scope starts. Baseline allowances are separate approved
cases and cannot reset it. Old synthetic reviews remain historical.
