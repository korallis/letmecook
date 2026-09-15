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
It emits its observation after closing the relay and becoming ready for artifact
inspection. Active cancellation skips that hold and preserves unknown upstream
work; a stopped process does not acknowledge upstream quiescence. The supervisor
waits through Docker's asynchronous `created` state before expecting observation.
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
npm --prefix experiments/planner-probe ci --ignore-scripts
npm --prefix experiments/router-continuity run check
npm --prefix experiments/router-continuity test
GAFFER_ROUTER_SOURCE=/absolute/pinned/public/router \
GAFFER_OPENCODE_BINARY=/absolute/pinned/linux-arm64/opencode \
npm --prefix experiments/router-continuity run prove -- /tmp/continuity.json
```

An optional comma-separated third argument selects `pair`, `partial`, `outage`,
`tool`, `intent-write`, `result-write`, `active-stop` or `start-delay`. Each offline fault case uses its own fake
scope; this never renews a real initial allowance. The pure control matrix uses
constructed captured receipts, not provider observations. The integrated pair
executes the actual stock helper, router selection, authority and pinned binary.
Partial output remains unknown, has no successful release, and an attempted
replacement must produce no second original send. Router outage produces zero
original sends. Intent/result persistence faults fence the gateway and retain
evidence before synthetic cleanup.

The production packaging check uses the shared deployment CLI with synthetic-only
inputs, then the actual attach-only consumer. It uses the normal gateway loader,
an isolated internal network and measured egress relay, and no fault loader:

```sh
GAFFER_ROUTER_SOURCE=/absolute/pinned/public/router \
GAFFER_OPENCODE_BINARY=/absolute/pinned/linux-arm64/opencode \
node experiments/router-continuity/deployment-proof.ts /tmp/private-continuity-proof
```

It checks prepared packet/source hashes, an initially unstarted clock, both
receipts, the unchanged initial scope/start/deadline, retained transition state and
cleanup. Its synthetic credentials and provider responses are never live evidence.

Detailed output files are private synthetic troubleshooting records. The pair's
`public` field is an explicit projection with opaque A/B references and no policy,
account/workspace metadata, credentials or private paths. Retention is immutable
before cleanup; missing/failing observations are kept as failures.

## Shared deployment consumer

The shared `experiments/native-evaluation/deployment.ts prepare` command packages
these modules only when its private inputs contain the exact `continuity.json`
declaration. It binds the reviewed source manifest and resolved fixture to the
deployment packet. `evidence RECORD ATTEMPT_ID` and
`continuity-transition RECORD continuity_model_lock_once` reuse its serialized
private control channel; neither command creates a scope. Without the declaration,
the gateway has no continuity handler and packaging adds no continuity code.

After the coordinator has prepared/reviewed the complete suite and explicitly
started its single initial scope, the trusted host runs:

```sh
GAFFER_OPENCODE_BINARY=/absolute/pinned/linux-arm64/opencode \
node experiments/router-continuity/consume.ts \
  /private/deployment.json /private/continuity-result.json
```

The consumer requires private owned directories/record, an already started initial
scope, the exact packet, no unresolved reservations, two remaining sends and at
least 120 seconds remaining. It checks every staged client source against the
prepared manifest, selects only the packet's declared profile, grants the two fixed
attempts, and retains immutable private observations. Worker staging, 30-second
worker walls, 45-second grants and control/artifact overhead count against the
existing deadline. Each new member needs 45 seconds remaining. It never prepares,
starts, resets or restarts a scope. On failure it physically stops workers and the shared deployment. It removes its
workers only after durable evidence retention; storage uncertainty retains stopped
container logs and staging references. No configured evidence label automatically establishes live acceptance.

Optional packaging and this consumer have passed an actual synthetic deployment
exercise. The integrated worker/planner stack, prepared suite and finite timing variant are
implemented below. Fresh independent controls review and actual bounded live
evidence remain prerequisites to live acceptance. Source-host,
credential and billing mutations remain outside this fixture; routine fixture
implementation does not create an additional operator approval flow.

## Prepared initial suite

`suite-prepare.ts INPUT_DIRECTORY` prepares the worker, planner and continuity
closures before the clock starts. The directory must be private and initially
contain the reviewed schema-1 common `profile.json`, `config.json` and `relay.json`.
The pinned binary is supplied through `GAFFER_OPENCODE_BINARY`. Preparation derives
three closed schema-2 profiles and writes their registry, suite/continuity
declarations and staged public fixtures. It imports no credentials and makes no
provider calls. The staging manifest includes the binary, fixture/schema, and the
actual copied bytes of the five locked Ajv runtime packages. Symlinks are refused.

The coordinator then uses the existing deployment preparation and one explicit
start-bound command:

```sh
node experiments/router-continuity/suite-prepare.ts /private/inputs
node experiments/native-evaluation/deployment.ts prepare \
  /private/deployment.json /private/inputs /absolute/pinned/public/router
# Review the concrete packet, source, isolation and egress before this command.
node experiments/router-continuity/suite-run.ts \
  /private/deployment.json REVIEWED_PACKET_DIGEST /private/suite-result.json
```

The suite coordinator checks the complete packet and staged bytes before its only
start, then attaches the default worker, read-only planner and two continuity
members serially. A one-use local marker prevents rerunning it. Consumers never
receive private controls or create a scope. Worker artifact and planner proposal
acknowledgements use `candidate RECORD FILE` through the existing command lock and
gateway validator. Proposal acknowledgement grants no execution authority.

The schema-2 timing tag is exactly `initial-suite-v1`; schema-1 worker/continuity
walls remain at most 30 seconds and the old planner budget remains 5 seconds.
These new finite ceilings are engineering selections, not measured live latency:

| Consumer | Session wall | Request total | First semantic output | Semantic idle |
| --- | ---: | ---: | ---: | ---: |
| Worker | 180 s | 120 s | 90 s | 45 s |
| Planner | 150 s | 120 s | 90 s | 45 s |
| Each continuity member | 90 s | 75 s | 60 s | 30 s |

The four walls total 510 seconds. Scheduling reserves 15 seconds of acknowledgement
time per phase and 15 seconds for stop, requiring 585 seconds at worker admission;
the remaining 15 seconds cover startup. It rechecks the original remaining scope
and physical allowance before each phase. Fixed session and grant deadlines never
renew during a continuation or repair. The selected planner budget participates
in its settings and input-revision identities. Provider caps, tool schemas, byte
limits, roles and classifiers are unchanged.

The ordinary synthetic path uses six physical sends. The general coordinator
instead joins all qualified original operations to the actual scope debit and
enforces the aggregate ceiling of ten, allowing the already-supported direct or
repaired planner proposal and router-owned charged fallback. It creates no replay
entitlement. An operation marked `unknown/running` remains in flight; the watcher
stops on an ended unknown original, or qualification, budget or deadline failure.
Even a successful phase sequence is reported failed when required stop or cleanup
fails. Shared deployment stop runs independently of report persistence.

If observation storage fails, the coordinator physically stops consumers but
retains their container logs and staging references. Stopping destroys `/work`
tmpfs; a retained container does **not** preserve the worktree. Independent artifact
durability remains unavailable and the run remains unaccepted. Logs cannot replace
the trusted artifact observation. Ordinary failure logs are durably saved before
test resources are removed.

The production-source synthetic suite can be reproduced with:

```sh
GAFFER_ROUTER_SOURCE=/absolute/pinned/public/router \
GAFFER_OPENCODE_BINARY=/absolute/pinned/linux-arm64/opencode \
node experiments/router-continuity/suite-proof.ts /tmp/private-suite-proof
```

Its fake planner responses wait six seconds, exercising the new variant beyond the
old five-second fixture limit. `stale` and `dependency` proof cases refuse before
start. The explicit offline host/gateway fault loaders exercise partial originals,
storage failure, stop failure, direct proposals and repair; production preparation
never selects them. Reports identify fault injection separately. Synthetic test
cleanup happens only after diagnostic retention, and is distinct from a consumer's
decision to preserve resources following failed storage.

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
