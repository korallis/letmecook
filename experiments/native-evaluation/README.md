# Native subscription evaluation experiment

Issue #83 implements shared prerequisites for the initial local-limits trial.
All retained measurements are offline. They do not establish a deployed provider
request or the durable application foundation (#9).

The native profile admits the captured OpenCode 1.18.30 Responses apply_patch
subset, exact Astra/xhigh, existing Codex subscriptions, access-token-only state,
and explicit unavailable provider output-token and monetary caps. Its initial
SQLite scope is shared across boundary requests and physical retries. Neither a
new request ID nor a new scope name resets that allowance. An uncertain original
send retains its debit and prevents replacement.

## Reproduce offline evidence

Use the pinned public 9Router source and Linux ARM64 OpenCode binary described in
`fixtures.mjs`. Docker runtime identity must match `../isolation/profile.json`.
The binary and helper evidence are ARM64-specific; an x64 CI run is different
evidence. Supply absolute paths, then run sequentially:

```sh
GAFFER_ROUTER_SOURCE=/absolute/public/router node experiments/native-evaluation/run-router.ts /tmp/native-router-evidence
GAFFER_ROUTER_SOURCE=/absolute/public/router node experiments/native-evaluation/run-router.ts /tmp/native-tls-evidence transport
GAFFER_ROUTER_SOURCE=/absolute/public/router GAFFER_OPENCODE_BINARY=/absolute/public/opencode node experiments/native-evaluation/run-binary.ts /tmp/native-binary-evidence.json
GAFFER_ROUTER_SOURCE=/absolute/public/router GAFFER_OPENCODE_BINARY=/absolute/public/opencode node experiments/native-evaluation/run-binary.ts /tmp/native-disabled-hook.json disabled-hook
node experiments/native-evaluation/run-egress.ts /tmp/native-egress-evidence.json
```

`run-router.ts` executes the actual pinned router under a test-only loader. Faults
are injected at the SQLite adapter boundary, not through deployment controls.
It checks synchronous commit expiry, admission/debit/original-receipt persistence
failures, altered translated output, and even false/null TLS overrides. Failed
original or translated validation must never leave a success decision.

`run-binary.ts` uses separate network-none 768 MiB worker and 768 MiB gateway
containers, a private inference socket and separate control/state volumes. A
second gateway must fail before changing the first gateway's database identity.
The default run captures two Responses requests and the resulting file; disabling
built-in hooks serializes max_output_tokens=128 and is rejected before a provider
send. The `artifact-restart` mode exercises two clean gateway boots without
inference: a new approved baseline starts without incidental profile selection,
acknowledgement retries remain idempotent and both earlier/later artifacts survive.
An earlier 512 MiB worker failed from OOM before any request; that profile
is not a passing binary result. Host evidence is fsynced before state cleanup.

`run-egress.ts` uses an internal Docker network and synthetic destinations only.
The credential-free TCP relay starts inactive. A short-lived pinned nftables
helper installs and reads back an exact deny-default table in the relay network
namespace; reachable local controls become blocked except for the selected
IPv4:443. DNS, IPv6 and other destinations remain blocked. All setup helpers are
removed before relay activation. The gateway's pinned dispatcher keeps TLS and
original-response observation inside the authority process, fixes origin/path,
uses one request per connection and rejects trust overrides. The local TLS proof
checks that an untrusted peer receives no HTTP bytes.

## Scope and deployment seams

`gateway.mjs` acquires the persistent owner lock before importing the upstream
DB, and exposes controls only on a separate supervisor socket. It writes a
reviewable policy packet first. The initial elapsed clock starts only with an
explicit private `start` command bound to that packet's digest; preparing the
consumers must happen before that command. A private `grant` binds each request
series to its profile, graph, task/lease and shared scope. Stop saves receipts,
reservations and artifact acknowledgements; unknown work retains ownership.
Native `PrivateControl.assertNativeCurrent(record, admission?)` remains required
after transport drain, including post-persistence and per-frame release checks.
It retains the request clock under the same boot and checks scope/token/lease
limits; the boundary separately checks its earlier request start synchronously.

`deployment.ts` provides the trusted prepare/inspect/select/start/grant/stop
supervisor. `bootstrap.mjs` verifies its source/runtime manifest before gateway
or database evaluation. A private configuration volume supplies only the gateway;
the relay and nftables helper receive only their destination/rules files. Host
records require a private directory and use exclusive random temporary files.
An exclusive command lock serializes record reads and updates. Duplicate prepare
and concurrent commands fail without changing an incumbent record or processes.
Persistent record open/fsync failure fences owned containers independently of
record saving and retains SQLite plus the owner lock for supervised recovery.
Changing a Boolean or endpoint does not turn synthetic evidence into deployed
evidence. This implementation has been exercised only with synthetic credentials.

Prepare a mode-0700 directory containing `profile.json`, `config.json` and
`relay.json`. The exact profile schema is in `native-profile.mjs`; the private
router config contains only the selected access tokens with verified expiry and
no refresh or ID tokens. Optional `profiles.json` predeclares up to eight closed
consumer variants, with `profile.json` equal to its first entry. Every variant
shares one authorization, scope, deployment and classified connection set. The
supervisor fills source/runtime/isolation hashes and the gateway supplies its
actual owner fence. A live profile selects a reviewed public provider IPv4 and
the fixed HTTPS Codex endpoint; DNS and refresh are denied.

```sh
mkdir -m 700 /absolute/private/deployment-records
node experiments/native-evaluation/deployment.ts prepare /absolute/private/deployment-records/run.json /absolute/private/inputs /absolute/public/router
node experiments/native-evaluation/deployment.ts inspect /absolute/private/deployment-records/run.json
# Review the concrete packet and finish all consumer preparation before start.
node experiments/native-evaluation/deployment.ts start /absolute/private/deployment-records/run.json REVIEWED_PACKET_DIGEST
node experiments/native-evaluation/deployment.ts select /absolute/private/deployment-records/run.json PREDECLARED_PROFILE_DIGEST
node experiments/native-evaluation/deployment.ts grant /absolute/private/deployment-records/run.json /absolute/private/binding.json
node experiments/native-evaluation/deployment.ts stop /absolute/private/deployment-records/run.json
```

Selection stays in the same running authority. It requires quiescence, advances
its generation and creates a fresh boundary policy; old grants cannot be reused.
It preserves the existing scope row, start/deadline and spent count. Worker,
planner and other consumers must not restart the deployment or create separate
stores to split the initial allowance. A worker protocol requires a worker role
at issuance, admission, reload and release. A read-only planner descriptor still
requires a separately reviewed implementation; it is not enabled by this registry.

Stop consumer trees through their owning supervisor, then stop the shared gateway
and relay. The gateway fsyncs receipts, budgets and acknowledged artifacts before
its normal exit. `persistImmutable(directory, kind, value)` in
`durable-records.mjs` returns `{digest, file, created}` after a no-replace atomic
link and directory fsync; consumers can use the same primitive for acknowledged
candidate envelopes. Existing same-digest records are verified and re-synced,
and malformed/symlink collisions are refused. Bare artifact acknowledgements
add an immutable `artifact-ack` manifest with packet/policy/authority/profile and
started scope/authorization identity, returned as `acknowledgement:{digest,file}`.
The blob can be deduplicated across baselines while each scoped acknowledgement
and its immutable deployment packet remain independently retrievable. Gateway revisions derive from the
fenced boot generation so a separately approved later baseline can reuse the
preserved store without reopening an earlier scope. The host supervisor preserves all state volumes and records;
there is no automatic deletion or stale-lock recovery. After a crash or unknown
send, verify every old process is dead and reconcile original receipts before
any manual owner-lock recovery. Restart closes the elapsed scope and cannot renew
an initial allowance or the same baseline case. A copied local state volume has
one owner; these controls do not require changing an optional source host.

`run-deployment.ts` exercises prepare/start/selection/stop without inference. Its
`open` and `sync` variants inject host-record persistence failure after scope
start and verify physical fencing, unchanged SQLite scope and retained ownership.
The `gateway-stop` variant prevents gateway result persistence and requires a
failed stop plus the same retained ownership and physical fencing. Every variant
also verifies duplicate prepare and overlapping-command denial.
`portable-run.mjs` executes source regressions on CI's reported runtime without
claiming that it matches the measured Docker Desktop/Linux ARM64 profile.

The apply_patch profile is a worker consumer profile. A future read-only planner
must add its own explicitly versioned codec/tool/profile identity through the
same receipt authority and aggregate scope machinery. It must not impersonate
OpenCode, gain apply_patch authority or create a second routing authority.

## Demonstrated fallback limit

The aggregate test includes an original-JSON account rejection, classified-account
fallback and later requests, stopping at ten physical sends. A complete
response.failed event can make stock Codex retry after cancelling its reader;
the first receipt then says provider_failed/original_cancel. Both physical sends
are charged, but strict boundary EOF classification withholds the later candidate.
That charged, quiescent-but-unreleased case is retained explicitly. It does not
relax the separate rule that unknown prior work prevents any second send.
