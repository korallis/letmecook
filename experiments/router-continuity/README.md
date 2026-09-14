# Same-provider continuity fixture

Issue #8 preparation only. This experiment runs a pinned OpenCode 1.18.30 worker
through the shared native boundary and actual 9Router 0.5.75 source. Provider
responses and credentials in the launcher are synthetic. Two fixture rows do not
establish two distinct real subscriptions. #6/#7 live acceptance and #9's foundation
decision remain separate gates.

The fixed request asks for `GAFFER_CONTINUITY_OK` without inspecting/changing files
or using tools. Both independently planned invocations use the same existing
OpenCode `ask` settings, Astra/xhigh, enabled builtins and Responses/apply_patch
profile. The pin serializes the user prompt as a quoted string; the receipt check
requires that exact captured form. A tool call fails this fixture and CLI asks are
rejected. No edit classifier was changed: the separate result is
`continuity_transport_completed`. The supervisor independently reads the parked
worker's unchanged file/base/diff and joins its observation to the original
receipt, durable decision and exact request/output/settings identity.

The worker uses default client-only staging, a network-none 768 MiB container,
finite 30-second binary wall/output bounds and a ten-second artifact-inspection
window. It has no private controls, router DB, provider credentials or fault loader.
The gateway is a separate measured 768 MiB container. Synthetic gateway request
timers remain 6/4/2 seconds. These short fixture timings are not a live readiness
claim or revised product defaults.

## Private transition

By default the shared gateway has no continuity control. An optional
`/config/continuity.json` must match the closed fixture declaration and an admitted
initial worker profile using the exact ask settings and two connections. Its
resolved profile digest becomes part of the immutable deployment packet. The
gateway verifies the continuity source entries before importing this optional code.

The exact declaration is:

```json
{
  "schema": 1,
  "fixture": "same-provider-between-requests-v1",
  "firstAttemptId": "continuity_a",
  "secondAttemptId": "continuity_b",
  "transitionId": "continuity_model_lock_once",
  "settingsDigest": "<settingsDigest('ask')>",
  "scopeId": "<existing initial scope id>"
}
```

The supervisor socket accepts only
`{command:"continuity-transition",packetDigest,transitionId}`. It obtains A from
the first attempt's validated successful original receipt. It never accepts a B
selector, arbitrary account ID, SQL, token, status, error text or duration.
Quiescence, current owner/policy, original EOF, scope/token/lease validity and no
second attempt are checked before durable intent. Admission and mutable controls
are refused during the transition, including already queued admission races.

The gateway calls stock `markAccountUnavailable` on A with the declared controlled
health reason and the existing scope deadline. The guarded router repository
rejects graph or credential changes. The controller additionally checks unchanged
graph/profile/credential identity and scope/debits, records its result durably, then
acknowledges it. Repeating the identical command returns that result without
another mutation or extended lock. A recovered intent or persistence uncertainty
fences subsequent admission and replay. This is explicitly injected local health
state, never evidence of an original provider 429/503 or natural quota exhaustion.

The second normal request reaches the unchanged router route/profile. The router's
fill-first strategy excludes A's active model lock and selects B. The fixture
requires one physical original operation per member and distinct receipt connection
identities. Every original/fallback remains in the shared initial scope.

## Reproduce offline

Use the pinned public source and Linux ARM64 OpenCode binary; Docker must match
the repository's measured runtime. The launcher selects a test-only loader and
network-none containers. It cannot launch a live endpoint or import live secrets.

```sh
npm --prefix experiments/router-continuity ci --ignore-scripts
npm --prefix experiments/router-continuity run check
npm --prefix experiments/router-continuity test
GAFFER_ROUTER_SOURCE=/absolute/pinned/public/router \
GAFFER_OPENCODE_BINARY=/absolute/pinned/linux-arm64/opencode \
npm --prefix experiments/router-continuity run prove -- /tmp/continuity.json
```

An optional comma-separated third argument selects `pair`, `partial`, `outage`,
`tool`, `intent-write` or `result-write`. Each offline fault case uses its own fake
scope; this never renews a real initial allowance. The pure control matrix uses
constructed captured receipts, not provider observations. The integrated pair
executes the actual stock helper, router selection, authority and pinned binary.
Partial output remains unknown, has no successful release, and an attempted
replacement must produce no second original send. Router outage produces zero
original sends. Intent/result persistence faults fence the gateway and retain
evidence before synthetic cleanup.

Detailed output files are private synthetic troubleshooting records. The pair's
`public` field is an explicit projection with opaque A/B references and no policy,
account/workspace metadata, credentials or private paths. Retention is immutable
before cleanup; missing/failing observations are kept as failures.

## Live integration remains pending

There is no live launcher in this directory. The generic deployment CLI currently
does not package `continuity.json` or these extra optional modules; that concrete
packaging and full-suite supervisor must be integrated and independently reviewed
before the live initial clock starts. No new operator approval is required merely
because a routine fixture implementation was absent: the coordinator assesses the
existing assigned scope against the concrete reviewed controls. Source-host,
credential and billing mutations remain outside this fixture.

Prepare every consumer and its failure evidence first. The intended initial suite
is worker edit/final (two expected sends), planner read/proposal (two), then this
continuity pair (two), sharing one gateway/store/owner and the same aggregate
10-physical-attempt/600,000-ms initial allowance. Four unused sends are contingency,
not a replay entitlement. Stop on any unknown original work or either ceiling;
do not warm up routes or replenish allowance with a new scope/store/baseline.
Freeze exact per-consumer timers and control/retention overhead in the reviewed
packet. Current synthetic timers do not guarantee a live Astra response.

Live acceptance still requires private proof of two distinct operator-configured
Codex subscriptions, their opaque mapping, valid access-only expiry, the concrete
deployment and isolation/egress review, and actual bounded live original receipts.
Natural quota exhaustion, remaining usage/headroom and live passive status remain
unproved. Route readiness is not `gateway_ready`; the #4 passive adapter needs its
own authenticated read-only ingress. Paid fallback and refresh stay denied.
