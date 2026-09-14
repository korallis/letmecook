# Native subscription evaluation evidence

Shared M0 prerequisites for [#83](https://github.com/korallis/letmecook/issues/83),
implementing the [approved limits contract](../contracts/native-subscription-limits.md).
The [experiment and reproduction commands](../../experiments/native-evaluation/README.md)
use the actual pinned 9Router modules, SQLite and OpenCode binary with synthetic
credentials and responses. No provider was called. This does not close the live
harness, planner, continuity or foundation gates in #6, #7, #8 and #9.

## Retained packet and reproduction

The [first measurement packet](native-evaluation-run.json) retains the concrete
synthetic deployment policy, all 86 runtime file hashes, original receipt and
budget projections, actual wire identities, containment settings, kernel probes,
control failure results and the failed 512 MiB observation. That historical packet binds runtime commit
`ab27fc2e181fefee4687540e022d058a65fda540`. Review subsequently found and fixed
release-time expiry and restart/artifact defects; its passing cases do not prove
those previously missing failures. The [review-fix packet](native-evaluation-review-fixes.json)
records the corrected runtime and new regressions. Input hashes identify the retained raw records. The
[collector](collect-native-evaluation.ts) checks committed/current source bytes,
paired binary manifests, exits, budgets and cleanup before projecting them.
It excludes host paths, configuration credentials and complete logs.

| Measurement | Observed result |
| --- | --- |
| Actual pinned router/SQLite native matrix | 33 cases, all exited 0 without OOM; owned containers removed |
| Existing router authority integration | All 41 cases pass |
| Portable source runner | Same 33 cases; explicitly no local-containment claim |
| Actual OpenCode, enabled / disabled hook | Two admitted physical sends / zero provider sends |
| Kernel egress and TLS | Selected synthetic route works; forbidden routes and untrusted certificate refused |
| Deployment controls | Normal, host open/fsync failure and gateway stop failure; collision/overlap regressions pass |
| Worker process tree | Signal-ignoring parent/child/grandchild terminated; no OOM or post-stop execution |

The [process-tree producer](native-process-stop.ts) uses the same native worker
resource and namespace settings with a deliberately uncooperative synthetic
process tree. Its forced stop is separate from the successful actual OpenCode
exit. Run it with `node docs/evidence/native-process-stop.ts /tmp/tree.json`.
The collector accepts a local JSON manifest naming the outputs from the documented
runners (`binary`, `disabledHook`, `router`, `portable`, `egress`, `processTree`,
`oom512`, `legacy`, optional `artifactRestart` and four `deployment` modes) plus the exact `sourceCommit`. No retained
private path is required by a public installation.

## Review-fix verification

The corrected runtime adds five actual-router failure cases for scope, token,
request and lease expiry during delayed decision durability, plus expiry immediately
before frame release. Each retains one charged original send and the persisted
success decision while delivering no response frame. The private request clock
remains available after the original handler drains. The two-boot artifact case
preserves both scoped acknowledgements and the first packet/blob after a later
approved baseline, including an idempotent retry and the unchanged closed first
scope. The final native matrix has 38 cases; 127 unit tests and the three experiment
typechecks pass. The packet names source commits separately for retained unchanged
legacy, kernel and process-tree proofs.

The imported, hashed `egress-helper/metadata/base-installed.tsv` retains 413 lines
with trailing whitespace. A full base-to-head whitespace check reports those lines;
the check excluding that unchanged imported inventory passes. Its bytes were not
rewritten to hide that observation.

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
is released. The same private authority checks scope, token and retained request
clocks after decision persistence and before each output frame. The boundary also
checks absolute and monotonic request elapsed time synchronously, so a blocked
fsync cannot race an overdue timer. Expiry withholds delivery without refunding
the send or erasing the durable decision. Altered translation and failed decision
persistence release none.

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
files, fsync and rename. An exclusive command lock is acquired before reading
the record; duplicate preparation preserves the incumbent bytes and processes,
and overlapping commands cannot overwrite state with an older snapshot. The gateway stores budgets, receipts, journal decisions
and acknowledged artifacts outside disposable tmpfs. Artifacts use immutable
content-addressed files: exclusive temporary write, file fsync, atomic no-replace
link, content verification and directory fsync precede acknowledgement. Retrying
the same payload after restart returns its original identity; later content never
overwrites it. A separate immutable acknowledgement binds each blob to its
packet, selected policy/authority/profile, started scope and authorization. The
packet itself remains immutable, so both old acknowledgement identity and content
survive later baselines. A fresh fenced boot uses a new startup revision, allowing a new
approved baseline on the same preserved store while earlier scopes stay closed.
Normal stop persists state;
container/process-tree checks verify exit, no OOM and denied post-stop execution.
Injected persistent host-record open and fsync failures after start still fence
the owned containers independently of record writing, retaining the unchanged
SQLite scope and deployment owner lock. Failed gateway result persistence also
returns a failed stop, retains ownership and fences both containers; a zero exit
from the stop supervisor requires successful gateway and relay exits.

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
