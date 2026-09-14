# Native OpenCode adapter preparation

This separately named `opencode-native-m0-v1` adapter prepares issue #6 for the
shared `router-native-responses-local-v1` policy from issue #83. It remains an
offline preparation in draft PR #75. Its dependency is not merged into this
branch yet. The compatible Chat adapter, its 128-token requests, edit/write
tools, 512 MiB worker and historical evidence remain unchanged.

The public native task is fixed: a disposable Git repository containing
`greeting.txt`, `hello\n` → `hello from probe\n`. It is not an arbitrary repository
runner. The worker runs pinned OpenCode 1.18.30 with the exact shared settings:
`openai/@ai-sdk/openai`, Astra `xhigh`, summary `auto`, `store:false`, encrypted
reasoning continuation, builtins enabled, PURE, no external plugins, no OAuth,
native LLM false and WebSockets false. The captured builtin hook omits the
provider output cap despite the configured 128 default. Provider token and
monetary caps remain unavailable; local limits are not provider generation bounds.

## Integration interfaces

- `admission.ts` validates the shared schema 3 policy and `Binding.native`, exact
  settings digest, worker role, route, lease and finite limits. It returns a small
  worker request containing digests and the scoped grant, with no provider
  credentials, account selection or control access. It creates no scope or grant.
- `client.ts` describes/probes the pinned profile and preserves native JSONL.
  Only `apply_patch` tool outcomes are valid. Error/approval/cancellation and
  missing artifact/final records cannot become completed candidates. The CLI's
  known exit-zero permission rejection is still classified as blocked.
- `run.ts` provides start/events/cancel. It checks the exact public base, inventory,
  hooks and artifact; uses fresh explicit environment/config; bounds output and
  wall time; and terminates the local binary on failure or cancellation. Container
  teardown remains the outer supervisor's responsibility. Remote quiescence is
  always unknown in the worker result.
- `relay.ts` validates the exact captured OpenCode user agent, originator and
  session metadata, including its match to `prompt_cache_key`. It preserves the
  body and retains metadata without auth. It reconstructs the existing boundary's
  allowed HTTP transport headers. This is an untrusted worker transport check,
  not boundary-side metadata validation and never native Codex impersonation.
  Duplicate/extra headers and alternate paths/hosts are refused. The authority
  remains the shared boundary grant and original receipt.
- `staging.ts` enumerates only the client files, settings, JSON parser and binary.
  Admission, receipt verifier, router source, authority, SQLite and deployment
  controls are excluded. `isolation.ts` enforces the separately measured 768 MiB
  worker variant over the existing namespace/mount/resource checks. The shared
  gateway owns its separately measured 768 MiB topology.
- `evidence.ts` joins the exact worker binding/settings, base/head, independently
  observed artifact/diff, raw native events, ordered initial/continuation requests,
  durable original receipts, decisions, output digests and call/results. It uses
  the shared continuation codec; encrypted reasoning/order/call identity remain
  exact while the SDK's omitted provider item IDs stay omitted. The first public
  task requires one patch and one final response. It produces an artifact packet
  containing these links. The shared artifact control must persist that complete
  packet; `acknowledgeCandidate` requires its digest in both the acknowledgement
  and durable gateway artifact record. A candidate does not grant independent
  acceptance, publication or merge.

The supervisor must obtain `observedArtifact`, the complete attempt decisions and
receipts, pending reservations and decision times from its trusted artifact read
and shared gateway records. Pending reservations block qualification. Feeding the
worker's own report back as independent evidence does not satisfy that contract.
Failed or missing observations must be retained before cleanup. The gateway's
control protocol is deliberately not copied here; the prepared multi-consumer
registry/start/grant/stop handshake must use the accepted issue #83 version.

## Checks and remaining execution

After the shared dependency is present:

```sh
npm --prefix experiments/harness run check:native
npm --prefix experiments/harness run test:native
```

The preparation was checked in an archive of exact shared commit
`70bacc54cce839e5d0b6e168dceee10977a4eecb` with these adapter files overlaid.
The native fixture retains a real pinned-binary/shared-router synthetic capture
whose 75 recorded source digests match that commit. Its clocks/base/diff additions
inside the tests are explicitly constructed fixtures. They are not a new adapter
runtime or live result. Existing native source tests pass only in that combined
snapshot until #83 is integrated; ordinary Chat checks still run on this branch.

Required next work is actual new-adapter/shared-gateway integration, fresh
independent review and final CI, including native ask, cancellation/descendants,
crash, receipt/persistence failures, ambient builtin/plugin/auth discovery and
ingress/egress negatives. The prior direct-loopback 512 MiB capture and its
OOM-killed concurrent control are historical; 512 MiB is not the accepted
integrated native memory profile. The completed disabled-hook shared control
observed cap 128 and zero original sends.

Prepare and review every intended public consumer and the concrete deployment
before starting the one initial scope: at most 10 physical inference attempts
and 600,000 ms elapsed across roles and retries. This adapter cannot renew or
start that scope. Separately approved baseline scopes have at most 32 attempts
and 900,000 ms; they cannot replenish initial checks. No live command, provider
credential, deployment, source-host operation, publication or merge is performed
by this preparation.
