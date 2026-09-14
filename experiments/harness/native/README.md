# Native OpenCode adapter preparation

This separately named `opencode-native-m0-v1` adapter prepares issue #6 for the
shared `router-native-responses-local-v1` policy from issue #83. It is integrated locally with the frozen shared runtime and remains an
offline experiment in draft PR #75. Dependency acceptance and publication are
coordinated separately. The compatible Chat adapter, its 128-token requests, edit/write
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
Failed or missing observations must be retained before cleanup. The multi-consumer registry/start/grant/stop handshake uses the shared issue #83
implementation; the adapter does not maintain a separate protocol or counter.

## Actual synthetic integration

`router-run.ts` stages only the enumerated client files into a 768 MiB worker,
starts a separately measured 768 MiB shared gateway, and uses its private
inspect/start/select/grant/evidence/candidate/stop controls. Its profiles and
credentials are synthetic. The pinned real OpenCode binary sends through the
worker relay, actual boundary, patched public 9Router and durable SQLite original
receipts. A separately invoked, read-only artifact inspector observes the parked
worker's tmpfs after the binary exits. No worker mount exposes private controls.

The gateway evidence query returns the complete attempt's original receipts,
durable decisions, pending reservations, decision times, selected policy and
scope. The closed candidate envelope binds packet, scope, policy, grant, ordered
requests, receipts, decisions and independently observed artifact. The private
candidate control validates those links again and uses shared `persistImmutable`
to retain the entire envelope as `candidate-<digest>.json`. Retries verify and
sync that record; they cannot overwrite it. After gateway shutdown the supervisor
reads the actual immutable file and verifies its identity/content before cleanup.
The proposal callback defaults to denial; the planner PR owns its codec and
callback wiring. Proposal acknowledgement cannot grant repository-write authority.

The synthetic failure matrix covers edit/final, delayed original EOF, CLI ask
rejection, same-scope profile selection with old-token/old-binding refusal,
cancellation, binary crash, detached/TERM-ignoring descendants, receipt and
boundary journal failures, immutable candidate write failure, incomplete/forbidden
output, router retries, disabled builtin hooks, ambient plugins/auth/Git hooks,
network/filesystem/process containment, native transport bypass and cgroup OOM.
The default worker reports only after relay closure and holds its tmpfs for at
most ten seconds; a requested active stop skips that hold. The delayed-start and
active-container-stop regressions exercise these lifecycle boundaries. Every
retry is an actual counted original attempt. Failures are saved before
cleanup, including a failed gateway stop: read-only retention saves the journal
and immutable records when the final result file is absent, and records unknown
quiescence. An OOM child is measured through kernel cgroup memory.events and
SIGKILL; Docker's top-level OOMKilled flag alone is not the evidence.

```sh
npm --prefix experiments/harness run check:native
npm --prefix experiments/harness run test:native
GAFFER_ROUTER_SOURCE=/absolute/pinned/public/router \
GAFFER_OPENCODE_BINARY=/absolute/pinned/opencode \
npm --prefix experiments/harness run prove:native -- /tmp/native-harness.json
```

This launcher is synthetic-only. Its fault loader is test staging, never a
deployment launcher or a way to alter a reviewed production response. It checks
all recorded source hashes again after execution. It is intentionally a fixed
public toy task, not a production supervisor for arbitrary repositories.

The retained `captured.json` is earlier proof from shared commit `70bacc54…`,
with all 75 source identities checked. Constructed clocks/base/diff additions in
unit tests remain labelled fixtures. Historical evidence is not reclassified as
new adapter execution. See [the integration record](../../../docs/evidence/first-harness-native-integration.md)
for actual current runs and [the builtin audit](../../../docs/evidence/first-harness-native-builtins.md)
for source observations and their limits.

Prepare and review every intended public consumer and the concrete deployment
before starting the one initial scope: at most 10 physical inference attempts
and 600,000 ms elapsed across roles and retries. This adapter cannot renew or
start that scope. Separately approved baseline scopes have at most 32 attempts
and 900,000 ms; they cannot replenish initial checks. No live command, provider
credential, deployment, source-host operation, publication or merge is performed
by this preparation.

The current worker binary wall ceiling is 30,000 ms. Synthetic gateway fixtures
use request/first-output/idle limits of 6,000/4,000/2,000 ms; these are deterministic
fault-test timings, not measured live Astra xhigh thresholds. The exact reviewed
live suite must declare suitable finite request and consumer bounds within its
shared 600,000 ms scope before admission. No limits were widened or calibrated
through model calls in this integration.
