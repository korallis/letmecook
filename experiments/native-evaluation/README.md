# Native subscription evaluation experiment

Issue #83 implements shared prerequisites for the initial local-limits trial.
This directory remains an offline implementation milestone until the complete
review packet and issue acceptance matrix have been reviewed. It is not evidence
of a deployed provider request or of the durable application foundation (#9).

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
send. An earlier 512 MiB worker failed from OOM before any request; that profile
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

The live deployment supervisor and its complete source/egress/ownership packet
still require integration and review. Changing a Boolean or endpoint does not
turn synthetic evidence into deployed evidence. This milestone never imports
real credentials or calls a provider.

The apply_patch profile is a worker consumer profile. A future read-only planner
must add its own explicitly versioned codec/tool/profile identity through the
same receipt authority and aggregate scope machinery. It must not impersonate
OpenCode, gain apply_patch authority or create a second routing authority.
