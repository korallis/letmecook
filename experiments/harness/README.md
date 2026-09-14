# First structured harness experiment

This is the bounded M0 adapter for [#6](https://github.com/korallis/letmecook/issues/6).
It executes OpenCode 1.18.30 in a disposable Linux container through the existing
attempt-scoped boundary and a **synthetic** router authority. It cannot launch a
live route, accept arbitrary repositories, resume sessions or publish changes.
See [the evidence and remaining criteria](../../docs/evidence/first-harness.md).

## Run the offline checks

```sh
npm ci --prefix experiments/harness --ignore-scripts
npm --prefix experiments/harness run check
npm --prefix experiments/harness test
npm ci --prefix experiments/inference-boundary --ignore-scripts
npm --prefix experiments/inference-boundary run check
npm --prefix experiments/inference-boundary test
```

## Run the pinned container proof

Use the image and exact Docker engine/kernel/architecture from
[the predecessor profile](../isolation/profile.json). Unsupported runtime versions
fail before a worker starts. The launcher never runs the harness on the host.
`pins.json` pins the public Linux arm64 release archive and extracted binary.
Download and extract only that public archive into a disposable directory, verify
both SHA-256 values, and provide the binary path. Do not run package lifecycle
scripts, repository code, or the Linux binary on the host.

```sh
GAFFER_ISOLATION_DOCKER_CONTEXT=desktop-linux \
GAFFER_HARNESS_BINARY=/absolute/path/to/pinned/opencode \
npm --prefix experiments/harness run prove -- /absolute/path/to/evidence.json
```

The proof creates task-labelled containers and a private tmpfs socket volume. It
verifies ownership before removal, checks that no labelled resources remain, and
writes a blocked marker before mutation. Signal interruption refuses new work and
reconciles cleanup. SIGKILL/host loss leaves the blocked marker and requires offline
ownership reconciliation. Output includes only public synthetic prompts/events;
never point this fixture at private code or supply real credentials.

The 512 MiB worker is a separately named resource variant of #5; gateway memory
remains 128 MiB. Both use `network=none`, distinct PID/network namespaces, nonroot
UID 1000, read-only image/runtime, dropped capabilities, no-new-privileges, seccomp,
bounded tmpfs/PIDs/CPU/files/logs, and no host home, agents or Docker socket. The
worker sees only the scoped socket and its disposable grant on a read-only volume.
The trusted boundary's journal, synthetic router socket and key stay in the
separate gateway's `/work`. The worker's loopback TCP-to-Unix relay is untrusted
transport; all request authority is checked in the gateway.

## Adapter and protocol boundaries

`adapter.ts` exports `describe`, read-only binary `probe`, `start`, replayable
sequence-based `events`, and idempotent process termination via `cancel`. The
public start request binds base SHA, attempt/lease/fence, route identity, settings
and finite limits. The experiment admits only its synthetic route, empty model
settings and the tiny fixture repository. It preserves native JSONL lines and
parsed versioned events. CLI events report completed step/tool parts, not token
streaming or a full permission lifecycle. Native errors, missing terminal events,
crashes, cancellation, and artifact mismatch cannot produce a completion candidate.
Candidates still require independent acceptance/review; no exit code grants it.

Approval `ask` is auto-rejected by this CLI. The adapter records `approval_blocked`
from the pinned native tool error even when CLI exit is zero; it never adds auto,
yolo or skip-permission flags. Interactive approval, server session abort and
resume are explicitly unsupported. `cancel` proves only local process exit. The
outer trusted launcher tears down the whole container/cgroup, including detached,
TERM-ignoring and `setsid` descendants. Remote work remains `unknown` unless a
separate trustworthy authority proves it; synthetic fixture quiescence is labelled.

`opencode-1.18.30-chat-edit-v1` is a separate policy identity. The original
`chat-text-tools-v1` still rejects OpenCode's fields and tools. The new profile
admits only `max_tokens`, `stream_options.include_usage=true`, the exact captured
edit/write tool schemas, bounded string arguments for `/work/repo/greeting.txt`,
and the observed session/encoding headers. Headers are validated and reconstructed
before upstream forwarding. Tools are withheld until complete valid SSE/EOF; a
partial or forbidden call never reaches execution. No request normalization,
provider fallback, account choice, remote resources or management endpoint is added.
No reasoning effort is admitted. Unknown usage stays unknown even if native events
show zero; nonzero fixture numbers are synthetic observations, not billing evidence.

The pinned binary executed a harmless project plugin despite the documented
disable flags. The `ambient-probe` case deliberately bypasses **adapter admission**
to reproduce that public negative observation inside the same OS boundary; it is
not an eligible run. The adapter refuses unexpected repository entries and active
Git hooks before spawn. This inventory is a narrow compatibility check, not an OS
security boundary or a general TOCTOU guarantee. Only the trusted launcher creates
the fixture, with no external writer; arbitrary working trees remain unsupported.
Repository instructions remain untrusted task data. Fresh home/XDG state and an
explicit subprocess environment exclude host auth/config, parent credential,
proxy and telemetry canaries. Unattended support is still disabled.

## Live preparation

There is deliberately no live command or boolean that upgrades this synthetic
profile. `probe(...).liveBlockers` provides the required missing evidence:

1. An accepted live `FrozenAuthority` with exclusive gated policy writers and
   trustworthy request/provider quiescence; see [#74](https://github.com/korallis/letmecook/issues/74).
2. A verified named route with exact model/settings and **every** fallback within
   the approved data/billing/tool/protocol/effort envelope. The #71 known-shape
   Astra → Terra → Opus `xhigh` preference is not a capability attestation; this
   adapter rejects effort settings rather than silently translating them.
3. A reviewed endpoint-only gateway topology that keeps real router/provider keys
   outside the worker, followed by a new live disposable task and negative/stop
   evidence. The current `network=none` gateway cannot reach any live router.

Prepare the public task (`greeting.txt`, `hello\n` → `hello from harness\n`), base
SHA, exact named route revision/graph, approved request/time/output limits and
independent review packet. Actual live execution needs the separate implemented
profile and accepted authority. Never use UI pending counts as quiescence, clone
active credentials into a second router, or remove isolation to make a smoke pass.
