# Native subscription evaluation evidence

Shared M0 prerequisites for [#83](https://github.com/korallis/letmecook/issues/83),
implementing the [approved limits contract](../contracts/native-subscription-limits.md).
The [experiment and reproduction commands](../../experiments/native-evaluation/README.md)
use the actual pinned 9Router modules, SQLite and OpenCode binary with synthetic
credentials and responses. No provider was called. This does not close the live
harness, planner, continuity or foundation gates in #6, #7, #8 and #9.

## Explicit authority and wire identity

The new schema-3 policy selects `native-subscription-local-v1` and
`opencode-1.18.30-responses-apply-patch-v1`. It binds the resolved operator
approval, exact Astra/xhigh, classified Codex connections, finite expiry/skew,
source/overlay/runtime/isolation identities, full graph, deployment owner,
consumer settings, tools, allowed paths and scope. Grants bind the selected
profile, authorization and scope along with task, attempt, lease/fence, role,
route, policy revision and expiry. This worker protocol rejects planner and
reviewer roles at issuance, admission, saved-state reload and release.

Provider output-token and monetary caps are explicitly **unavailable**. A hard
requirement for either capability is rejected. No provider cap field is serialized
and no numeric local limit is presented as a provider bound. Paid/unclassified
connections, alternative models/efforts, helpers, proxies, adapters and refresh
are refused. Existing strict/synthetic schemas and historical journals retain
their earlier interpretation.

The OpenCode binary is version 1.18.30, SHA256
`01edb5839aa10d5b09133fedcb335a062ecad6e82552933bb14f71756f2b296b`, from source
`3104c1428ec91f809e5ab86631300de41eb6952e`. The measured configuration enables
its built-in OpenAI hook, disables native LLM/WebSocket transport and automatic
compaction, supplies a fixed session title and starts with fresh HOME/XDG/auth
state. The worker receives only a disposable boundary grant. Capture includes
every inference request; the accepted public-fixture run has exactly two.
Disabling the hook serializes `max_output_tokens:128`; the native boundary rejects
that request before any provider send.

Initial and tool-continuation requests preserve exact reasoning settings,
`store:false`, the encrypted reasoning continuation, ordered call/result pairing,
cache identity and apply_patch schema. SDK normalization omits provider output
item IDs in continuation; the validated ordered output digest and native call ID
remain bound. The boundary reconstructs its small authority header set instead of
forwarding or fabricating privileged Codex client headers. Every physical request
is checked after the pinned router's actual transformations: named route resolved
to Astra, strict-false tool marker omitted and pinned default instructions added.

All Responses frames remain buffered. Fragmented UTF-8, LF/CRLF/CR framing,
identities, deltas, complete snapshots, paths and terminal status are validated.
Created events and heartbeats do not satisfy first-output or semantic-idle timers.
A complete original response/output digest, trustworthy receipt, current scope
and persisted success decision are required before any tool or final candidate
is released. Altered translation and failed decision persistence release none.

## Aggregate scope and serial consumers

The existing router SQLite authority stores scope start/deadline, boot, approved
case identity, spent count and each physical operation's request/ordinal/body and
output digests. Its transaction commits a debit and send-possible record before
network. Failure to persist admission or debit prevents dispatch. A final clock
check immediately before the transport call handles synchronous commits that
outlast the remaining budget while JavaScript timers have not yet run. A committed
possible-send debit is retained even when that check prevents the actual send.

Initial checks share at most **10 physical inference attempts / 600000 ms**.
Registered baseline cases have at most **32 / 900000 ms**, serially. Counts span
new request IDs, tool follow-ups, discarded results, retries and account fallback.
A new scope name cannot renew the initial phase or an approved case. Elapsed time
includes preparation after the explicit start, waits and role changes; it is not
active-inference time. Monotonic and wall-clock checks close conservatively on
expiry or clock reversal. Restart closes scopes and preserves spent counts.

The private authority freezes a closed consumer registry before start. All entries
share the same deployment, authorization, scope and connections. Selection is
private and serial, requires verified quiescence, advances generation and requires
fresh grants. It keeps the same SQLite scope row/start/deadline/spent values.
Two predeclared worker variants exercise this seam. A separately versioned
read-only planner protocol/repair contract remains work in #7; this registry does
not enable planner tools or give the planner OpenCode/apply_patch authority.

A created-event plus overload comment with an open original response now produces
one unknown operation and zero subsequent physical sends. It retains its
reservation and prevents replacement/profile selection. Original success without
EOF likewise releases no output. Retry waits obey stop and deadlines. The initial
access-token-only profile refuses refresh, including proactive refresh and
401/403 paths, and bounds inference by verified token expiry minus skew.

The demonstrated successful fallback path uses a complete original JSON account
rejection followed by a classified-account fallback; later requests exhaust the
same ten-attempt scope. A separate retained limitation remains: stock Codex can
cancel its reader after a complete failed event and retry. Receipts then show
`provider_failed/original_cancel` followed by `provider_completed/original_eof`.
Both sends are charged and the authority is quiescent, but the strict boundary
requires EOF for every settled operation and withholds the candidate. This is
not evidence of successful SSE-retry continuity and does not weaken unknown-send
fencing.

## Measured deployment profile

`native-local-worker768-gateway768-uds-persistent-v1` uses the selected Docker
Desktop/Linux ARM64 runtime, with a 768 MiB worker and 768 MiB gateway. The actual
binary completed the patch round trip under this variant. The earlier 512 MiB
worker failed from OOM before any request; it is retained as a failed observation,
not passing evidence for that resource limit. These local measurements are
separate from CI's portable source/SQLite checks and reported architecture.

Worker and gateway use network-none, nonroot, read-only root/source, dropped
capabilities, no-new-privileges, private namespaces, bounded CPU/PIDs/files/logs
and tmpfs. The worker has only its public fixture/binary and inference socket;
no provider TCP, host home, credentials, router state/code, private control or
Docker socket. A second gateway fails on the deployment owner lock before opening
or changing router state. The lock precedes upstream DB import and persists when
work is unknown.

The gateway owns separate persistent state, private control, inference and egress
socket volumes. The credential-free relay gets only a numeric destination and
limits; the short-lived nftables helper gets only filter rules. An exact kernel
readback and removal of setup processes precede relay activation. The filter runs
after connection tracking and before Docker's DNS NAT: default-deny input/output/
forward, selected IPv4 TCP443 only. Reachable synthetic port/destination controls
become blocked; DNS, IPv6, host gateway, metadata, loopback and other destinations
remain blocked. The positive selected route still works. No provider IP was used
in these tests.

TLS stays in the authority's pinned dispatcher over its private egress socket,
with fixed hostname/path, normal certificate validation, no trust/transport
overrides and one request per connection. A local untrusted certificate is refused
before HTTP bytes. Redirects cannot select a new destination. The opaque relay
owns no credentials, HTTP account choices, retries or original-response receipts.

## Deployment packet, stop and recovery

`deployment.ts prepare` verifies the selected runtime, builds source hashes,
installs/readbacks namespace rules, removes helpers and starts the gateway without
starting the scope clock. The gateway verifies its source/runtime manifest before
DB evaluation and writes the concrete boot/generation/full-graph/profile-registry
packet. The private `start` command requires that packet's reviewed digest. Prepare
all intended consumers and review their controls before issuing it.

Host records require a private directory and use exclusive random temporary
files, fsync and rename. The gateway stores budgets, receipts, journal decisions
and acknowledged artifacts outside disposable tmpfs. Normal stop persists state;
container/process-tree checks verify exit, no OOM and denied post-stop execution.
Injected persistent host-record open and fsync failures after start still fence
the owned containers independently of record writing, retaining the unchanged
SQLite scope and deployment owner lock.

Consumer supervisors stop their own trees before the shared gateway/relay. State
volumes are retained by deployment controls. Recovery requires verified old-process
death and reconciliation of original evidence before any manual stale-lock action;
there is no automatic unlock, budget reset, output replay or replacement of unknown
work. Sole ownership applies to the selected copied local state. No optional source
host needs to be changed merely because its authorized configuration was copied.

The final concrete deployment packet still requires operator review before live
admission. Live provider results, two-subscription continuity, baseline scores and
the build/adopt/extend decision remain separate deliverables. No new durable
application core is introduced by this experiment.
