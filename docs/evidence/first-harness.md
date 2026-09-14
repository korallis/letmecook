# First coding harness: bounded synthetic evidence

[Issue #6](https://github.com/korallis/letmecook/issues/6) is **not complete**.
The real OpenCode 1.18.30 binary returns a bounded disposable-repository change
through the implemented scoped boundary and a synthetic router. The live
harness → boundary → 9Router/provider criterion remains blocked by the live-authority gap documented in
[#74](https://github.com/korallis/letmecook/issues/74), exact route/settings evidence,
and a reviewed live gateway profile. No live inference, private code, account
configuration change, provider credential access or publication was performed.

The [reproduction instructions](../../experiments/harness/README.md),
[pins](../../experiments/harness/pins.json),
[full machine-readable run](first-harness-run.json), and
[preparatory probe summary](first-harness-preparatory.json) retain the boundaries
between independent preparation, combined synthetic proof and unproved live use.

## Actual pinned router integration

The new [actual-router proof](first-harness-router-run.json) uses the accepted
#77/#79 shared authority and durable receipt gates, real pinned router modules and
SQLite, and synthetic original HTTP. It is a separate proof from the historical
active-set fixture below. The mandatory authorized live task, deployed conformance,
exact selected live tuple/fallback envelope and live egress evidence remain unmet;
issue #6 and PR #75 must stay incomplete/draft.

The [usage-disabled binary capture](opencode-usage-disabled-capture.json) was run
before choosing the compatible extension. Both real OpenCode requests omitted
`stream_options` with explicit `includeUsage:false`; both preserved
`max_tokens:128`, `tool_choice:auto` and exact edit/write schemas. The second
preserved assistant `content:""` plus the correlated tool call/result. The new
router profile admits only those deliberate differences from the existing common
profile. Original Chat observation still rejects usage-only frames, and usage is
unknown. Historical usage-enabled evidence remains unchanged.

The new `m0-opencode512-router768-uds-v1` profile checks the effective 768 MiB
locked gateway and 512 MiB pinned OpenCode worker, separate network/PID namespaces,
network none, nonroot/no capabilities/no-new-privileges/seccomp, read-only mounts,
bounded CPU/PIDs/tmpfs/logs and complete owned-resource cleanup. Only the boundary
socket is shared; the scoped grant arrives on worker stdin. Router source,
control/SQLite/journal/credentials are inaccessible to the worker.

Successful edit and write runs preserve base/head
`e0c05a1793f5d3719e608bef690e28531a38c33c`, change only `greeting.txt`, retain the
exact diff and artifact digest, and join native completed tool/final stop events
with both request IDs and durably persisted `validated_success` decisions before
tool/terminal release. The approved route is `harness_coding`, model name
`gaffer-harness-edit`, through the compatible synthetic provider graph. The
request cap, choice, tools and message history are checked for preservation at
each actual hop; model substitution is the approved router combo resolution.

The proof covers original EOF and receipt-visibility delays, approval rejection,
unknown/mismatched receipts, actual journal temporary-file obstruction and
post-rename directory-sync failure, cancellation during receipt wait and after
persistence, authority changes, router authentication failure, partial/forbidden
tools, router errors, process cancellation/crash/descendant teardown, OOM,
ambient canaries and worker containment. Every failed worker observation is
saved before assertions and cleanup. Source identities and [failed development
observations](first-harness-router-development.json) are retained separately from
the final successful matrix.

The final source-matched matrix passed **24/24** cases, including **17** actual
router ingress refusals before original dispatch. The
[regression replay](first-harness-router-regressions.json) retains the old
13-case harness proof, 34-case shared bridge proof and 40-case original router
suite, all with verified cleanup. Portable checks passed: boundary **54/54**,
harness **13/13**, bridge lifecycle **4/4**, original observers **50/50**, isolation
**14/14**, all relevant type/syntax checks, docs build/check and whitespace checks.
The final harness record's 71 source digests and bridge record's 52 source digests
were compared with the working files. The new envelope's overlay identity covers
all four overlay modules, loader and patches. No historical run file was replaced.

No native-output-bound waiver, paid substitute, live provider, real account,
private repository or core application is used. OpenCode JSONL is native harness
output, not a Responses request/continuation implementation. Existing native
terminal/fallback/refresh failures are rechecked through the shared bridge.

## Historical fixture evidence

The following sections and linked original run describe the earlier 128 MiB
synthetic gateway with its separate 512 MiB worker. They remain part of the record
and are not relabelled as actual-router execution.

## Selected candidate and topology

Observed 14 September 2026 at 13:56 UTC: OpenCode **1.18.30**, binary SHA-256
`01edb5839aa10d5b09133fedcb335a062ecad6e82552933bb14f71756f2b296b`, public
source commit `3104c1428ec91f809e5ab86631300de41eb6952e`. Capability claims below
come from the actual binary, not model names or source declarations alone.

The worker profile is the explicit `docker-linux-arm64-opencode512-v1` variant:
Docker Engine 29.7.2, LinuxKit kernel 6.12.76, arm64, cgroup v2 and the image digest
already pinned by #5. Worker memory is **512 MiB**, no swap; trusted gateway memory
remains **128 MiB**. Both have network `none` and separate PID/network namespaces.
A read-only worker volume exposes only `inference.sock` and a disposable scoped
grant. The real `Boundary` and `PolicyGate`, journal, synthetic router socket and
synthetic router key live in the separate trusted gateway. The worker loopback
relay forwards bytes; it has no policy or provider authority.

The predecessor's 128 MiB combined probe OOM-killed before a POST. An independently
labelled 512 MiB compatibility mock then made a toy edit, but neither proved this
combined topology. The new run revalidates CPU throttling, PIDs (`EAGAIN`), memory
OOM, tmpfs limits (`ENOSPC`), read-only mounts/root, capabilities, nonroot/no-new-privs,
seccomp, host paths/sockets, symlink/proc attempts, IPv4/IPv6/DNS/metadata egress,
scoped path/model/auth/header negatives, teardown and ownership cleanup. It does
not alter #5's default profile or enable general unattended support.

## Bounded change and failure matrix

The selected synthetic route is `coding`, router model `gaffer-coding`, revision
`policy_1`, epoch 1. All accepted adapter cases start at disposable base/head
`e0c05a1793f5d3719e608bef690e28531a38c33c`; the immutable brief changes
`greeting.txt` from `hello\n` to `hello from harness\n`. Only that file changes:
one deletion, one addition, no commit/head movement. Native events, exact forwarded
request bodies, tool result continuation, final bytes/diff/status, usage observations,
container state and scoped boundary audits are in the run record.

| Case | Forwarded requests | Observed outcome |
| --- | ---: | --- |
| Bounded edit | 2 | `completed_candidate`; valid native tool result, stop event and exact artifact |
| Approval ask | 1 | `approval_blocked`; native rejection, unchanged file, **CLI exit 0** |
| Missing usage | 2 | Candidate change; usage remains unknown despite native zero counters |
| Partial tool stream | 1 | `harness_failed`; `partial_failure/invalid_stream` at boundary, no tool/file effect |
| Forbidden tool path | 1 | `harness_failed`; invalid tool withheld, no file effect |
| Router 503 | 3 | Fourth harness request denied by attempt cap; 25 s outer deadline gives `cancelled_unknown` |
| Local cancel during request | 1 | SIGTERM/process exit, boundary transport cancelled; upstream remains unknown in adapter |
| Harness SIGKILL crash | 1 | `harness_failed`; unchanged artifact, upstream cancellation remains unknown |
| Detached/TERM-ignoring/setsid tree | 1 | Adapter cancels harness; outer cgroup teardown removes remaining descendants in 1,065 ms |
| Memory exhaustion | 0 | 512 MiB limit OOM-kills fixture, exit 137, stopped PID 0 |
| Containment/resource matrix | 0 | All selected controls and bypass negatives pass |
| Raw-binary ambient negative | 0 | Canary observed, then bounded process termination; adapter admission had refused this input |
| Forced ambient timeout | 1 | Worker exits 1 on missing canary; stdout/stderr, signal, artifact and census retained before failed assertion |

All **13** cases reached their specified observations, including the intentionally
failed forced-timeout discovery criterion and its retained diagnostics. Worker/gateway stop states,
failed exec after stop, owned resource removal, empty final label queries and the
unchanged host sentinel were checked. No container or volume remains. `exit` in the
outer record is the supervisor/container status; use the nested `observation` record
for native `exitCode` and `signal` for the actual harness. The tree case intentionally needs SIGKILL of
the container and records exit 137. HTTP disconnect and synthetic active-set
quiescence are not provider cancellation receipts.

The fixture supplies 17 input/9 output/26 total tokens for each normal stream.
The successful edit exposes two such native usage events. These are invented test
numbers, not tokenizer/billable usage, account attribution, a pricing guarantee or
an accepted token-limit capability of a real serving model. The missing-usage
case confirms native zero values must not become known-zero usage.

## Adapter and protocol decisions

The adapter has minimal descriptor/probe/start/events/cancel surfaces, preserved
`opencode-run-json/1.18.30` native lines with sequence cursors, immutable attempt and
base identity, explicit finite output/wall bounds, and artifact reconciliation.
Binary integrity is hashed in 1 MiB chunks; an early whole-file hash version caused
a worker-side SIGKILL during the combined proof and was replaced. That failed run
did not establish OOM cause because its final worker state was not retained. Failed
runs now retain the pending case/state/logs in their blocked report.

CLI JSON is a structured step projection, not text-token streaming, structured
provider output, interactive permission lifecycle or resume support. `ask` safely
blocks; no auto/yolo/skip-permission flags are used. Cancel revokes no extra
publication authority and asserts only local process exit; trusted gateway teardown
revokes the attempt and closes admission. A completion candidate requires fresh
independent review and actual local acceptance outside this experiment.

The new `opencode-1.18.30-chat-edit-v1` profile separately admits the observed
`max_tokens`/`stream_options`, exact edit/write schemas, bounded fixture-path
arguments and narrow session/encoding headers. Tools are withheld until complete,
valid terminal stream/EOF. Known token usage is reconstructed from strict numeric
fields; unrelated metadata is discarded. The old `chat-text-tools-v1` remains
strict: OpenCode requests are still rejected there. No model/account router,
provider fallback loop, management endpoint, remote resource or arbitrary effort
enum is introduced.

## Ambient discovery and unsupported inputs

A harmless `opencode.json` plugin canary executed in the actual binary despite
`OPENCODE_DISABLE_PROJECT_CONFIG=1`, `OPENCODE_PURE=1` and a trusted explicit config.
The final `ambient-probe` deliberately reproduces that failure inside isolation
and terminates after observing the canary, with a 20 s discovery deadline and
1 s TERM-to-KILL escalation. It does not wait for an unrelated edit to complete.
It is a raw-binary negative fixture, **not** an admitted adapter task. Do not infer
that these flags enforce an exclusive configuration boundary.

The adapter refuses repository harness config/plugin directories, unexpected
entries and active Git hooks before launch. It supports only the generated tiny
fixture (`greeting.txt`, optional bounded `AGENTS.md`, trusted `.git`), created by
the launcher without another writer. This inventory does not provide a generic
TOCTOU defense against concurrent repository mutation or an arbitrary Git-config
security guarantee. The OS boundary provides containment. General repositories
need a reviewed preparation/admission design before adoption.

Fresh home/XDG directories and a complete explicit environment exclude ambient
host auth, parent provider/proxy/telemetry canaries, plugins and account discovery
state. Repository instruction injection remains task data: it cannot widen the
scoped model/tool/path/endpoint policy. Negative socket requests cannot reach
management, models, other protocols or unapproved models; the worker has no network
route to any direct router or provider and no access to the trusted journal/key.
These are concrete selected-profile negatives, not a universal sandbox proof.

## Review replay and diagnostic retention

The fresh review of `994281d` found a real evidence-loss failure. Its
[first replay record](first-harness-review-failure.json), retained with presentation-only host-path redaction,
pins the reviewed source separately from the final implementation and shows an
ambient worker exit after 21.218 s with one forwarded request: the 20 s
native timeout fired, then an exit-zero assertion ran before native output,
signal and canary state were emitted. Those missing fields cannot be reconstructed
from the remaining logs. Cleanup was verified. A second review replay passed the
original 12 cases; that pass does not erase the first failure. Both historical
failure records replace personal checkout paths in error stacks with
`<host-checkout>/`; original unredacted files remain local. Outcomes, native
observations and source digests are unchanged.

Workers now flush a structured `observation` record before validating native,
canary or artifact outcomes. Each artifact/process read records an error if it
fails, so one bad read cannot discard the already captured native output. The
separate `finished` marker is emitted only after assertions pass. The launcher
retains these observations before removing containers, including failed workers.

The new `ambient-timeout` regression uses the real pinned binary and a harmless
plugin that emits stdout/stderr diagnostics without creating the canary. A held
synthetic request triggers forced termination. The worker emits its native status,
stop reason, missing-canary state, artifact and process census, then **exits 1**
on the unmet discovery criterion. The launcher requires those diagnostics, one
request, unchanged artifact, no remaining OpenCode process and no `finished`
marker. Passing this regression means failure evidence survived cleanup; it is
not a successful discovery or task. It does not establish provider cancellation.

An [intermediate correction replay](first-harness-diagnostic-replay.json) then
retained a previously hidden ordinary-case variation: after the forbidden tool
stream was rejected, OpenCode retried once and the fixture returned final text.
The adapter correctly refused the unchanged artifact as `artifact_mismatch`; the
old test expected only `harness_failed` and therefore failed. Native events,
artifact, process census and boundary logs survived that assertion and cleanup.
Negative cases now explicitly admit only `harness_failed` or `artifact_mismatch`,
require no native tool event, unchanged file/head, an `invalid_stream` boundary
audit and the three-request cap. This changes the fixture assertion, not adapter
behavior or tool authority.

## Acceptance coverage and live blockers

| Issue criterion | Status |
| --- | --- |
| Pin one harness/settings; probe capability | Synthetic candidate proved; live model/settings readiness blocked |
| Minimal interfaces, native events, approval behavior | Implemented for bounded CLI; unsupported surfaces declared |
| Real disposable task through live 9Router | **Not met**; synthetic router only |
| Streaming/tools/approval/cancel/crash/router failure | Actual harness + synthetic boundary matrix proved; provider cancellation unknown |
| Ambient discovery and containment | Negative canary retained; tiny-fixture admission and OS controls proved; arbitrary repositories unsupported |
| Direct router/management/provider bypass negatives | Proved for selected network-none synthetic topology; live egress profile unproved |
| Exact selected settings/full fallback envelope | **Not met**; desired #71 labels are not capability proof |
| Independent acceptance/review | Required; this document does not grant it |

[Issue #74](https://github.com/korallis/letmecook/issues/74) records why 9Router
0.5.75 dashboard pending counts cannot implement `FrozenAuthority`: source-module
probes show expiry to zero without completion, zero before the disconnect abort
signal, and a combo writer that updates while a request is active. This is pinned
public source-layer evidence, not full HTTP/auth/provider proof. A live adapter
needs exclusive gated writers and trustworthy per-request quiescence. Unknown
remote work must remain reserved; no callback or elapsed timer can invent stop.

The [task-routing contract](../contracts/task-routing.md) is also binding. The
known-shape Astra → Terra → Opus `xhigh` preference is not an approved runnable
fallback graph. This profile accepts **no effort settings**. Resolve exact settings
semantics across every fallback with accepted capability evidence or an explicit
operator revision before live admission; never silently lower or relabel effort.
The live gateway must keep real inference/provider/publication credentials outside
the worker and be revalidated under its actual resource/egress topology.

## Observed checks

- `npm --prefix experiments/harness run check`: passed.
- `npm --prefix experiments/harness test`: 9/9 passed.
- `npm --prefix experiments/inference-boundary run check`: passed.
- `npm --prefix experiments/inference-boundary test`: 33/33 passed, including the unchanged legacy OpenCode rejection.
- Pinned `GAFFER_HARNESS_BINARY=… npm --prefix experiments/harness run prove`: 13/13 specified observations, cleanup verified.

CI runs the portable type/unit checks only; the exact arm64 Docker runtime proof
is the separately recorded local evidence. No live gate, merge, deployment or
production readiness is claimed.

Additional regression checks passed: isolation type check and 14/14 tests; docs
build/check (5 reader documents, 127 unique anchors, internal links valid). The
recorded runtime source digests were verified against the final implementation.

Final correction replay: ambient discovery observed the canary and stopped in 3,409 ms (SIGTERM); forced timeout retained both diagnostics and native SIGTERM after 4,320 ms. All 26 runtime source digests match this implementation.
