# Docker-free Linux confinement: source only, proof NOT RUN

Issue **#89**, follow-up to #5. Authored **15 September 2026** against the locked
`astra89-proof-procedure-v2.md`, SHA256
`fbc0b408d2023a769a24a8b48e856b52a6744c5efc4e91869775be65a4ec6b59`.
Fresh satisfied procedure review: `opus89-v2-review.md`, SHA256
`9027887408ebb52e7067e600f813e3917484126caddb14ac74b5874d59c539bf`.
The lock authorizes no host selection, setup or execution.

**No Linux host selected or measured. Every native proof criterion is NOT RUN and
unproven.** Offline tests below validate source logic using synthetic objects and
task-owned temporary files on macOS. They establish no Linux enforcement, native
compatibility, successful confinement, installation or unattended support.

The separate [default profile](../../experiments/isolation-native/profile.json)
contains no invented host, kernel, systemd or toolchain pins. Its `unmeasured`
status and null measurement fields deny admission. Every result retains
`unattendedSupported: false`, including a future `case-passed` result.

## Bounded source

| File | Responsibility |
| --- | --- |
| [run.ts](../../experiments/isolation-native/run.ts) | Strict profile admission; reviewable preparation data; trusted controller; native service properties; external observations; durable intent/quarantine; positive-owned stop and cleanup |
| [probe.ts](../../experiments/isolation-native/probe.ts) | Trusted PID1 bootstrap; separate fixed synthetic mock; public adversarial fixture modes; bounded pressure and egress operations |
| [run.test.ts](../../experiments/isolation-native/run.test.ts) | Native Node test/assert checks for dangerous admission and journal failure paths, using synthetic data only |

Node native TypeScript and standard library only. No dependency, framework,
application package, durable application core, router implementation or adapter.
The selected Go runtime/TypeScript UI direction remains unchanged. Existing
Docker experiment and evidence remain untouched.

## Admission and frozen input

One candidate family: disposable Debian 13 amd64, Linux 6.12, systemd 257.
These are **constraints from the procedure**, not observations or support claims.
An operator must measure exact values and explicitly freeze the input file's SHA256.
`validateProfile` defines the complete schema and rejects missing/unknown fields,
wildcards, zero digests, changed mandatory limits and zero/conflicting identities.

Required measurements include:

- Exact OS release hash, kernel release/config hash, boot ID, machine-ID hash,
  systemd version output, Node version and architecture.
- VM image and hypervisor provenance hashes; full staged rootfs content/mode/link
  digest; hashes for every enumerated executable and both source programs.
- Host namespace identities; distinct nonzero worker/mock UIDs and primary GIDs,
  plus a dedicated supplementary socket GID, all below 65536.
- Evidence and rootfs device/inode identities; evidence filesystem type. Only
  disk-backed ext4 or XFS qualifies here. tmpfs, overlay and network filesystems
  cannot provide durable evidence.
- Controller/OS RAM budgets, storage and inode headroom, and the measured identity
  of the dedicated `gaffer89.slice`. Its memory ceiling must equal the frozen
  controller budget; swap must be zero. The controller service runs inside this
  slice, outside worker and mock cgroups. `memory.current/stat/events` include
  staging, file-cache, shared-page and kernel charges; RSS alone is insufficient.

Each image/hypervisor provenance file has exact `schema`, `kind`, `artifactSha256`,
`version`, `observedAt`, `dockerFree` and `disposable` fields. Its file hash is frozen
in `artifacts`; its measured artifact hash must match the corresponding identity
pin. Those outside-guest measurements rely on the trusted operator, not guest
self-attestation or an invented VM version.

No host paths, commands, shells, destinations or limits come from repository code.
The fixed layout is `/var/lib/gaffer-native/{controller,rootfs,evidence}` with
root-owned staging and an initially empty, root-owned mode0700 evidence directory.
Required staged mount targets must exist; the minimal root includes only frozen
tools/libraries, OS release data and public fixture source. It excludes homes,
credentials, resolver configuration, shared Git administration and service sockets.
`rootfsDigest` gives the deterministic inventory algorithm. Hashing uses bounded
buffers rather than loading toolchain binaries into controller memory.

Root setup, staging, the dedicated controller slice, console access and real
power-cycle authority require a later operator transaction. This source does not
install tools, provision a VM, open SSH, select Matilda, discover credentials,
mount an operator's filesystem implicitly or repair an unsupported runtime.

## Native path — not executed

`--prepare` validates a measured profile and prints bounded JSON containing
prerequisites, property lists and argument arrays. It performs no setup. The
default profile deliberately cannot produce an eligible transaction.

After host selection, measured setup and separate execution authority, freeze
each invocation's profile SHA256, case and unique 24-character lowercase hex run ID.
The controller service must already use the measured slice. The reviewed JSON
transaction supplies the controller service command and the inner arguments:

```text
/usr/bin/node /var/lib/gaffer-native/controller/run.ts
  --execute --profile <absolute-frozen-profile> --profile-sha256 <exact-sha256>
  --run-id <unique-24-hex-id> --case missing-pids-limit
```

No implicit execution mode exists. `run.ts --case missing-pids-limit` alone refuses.
Non-Linux execution refuses before profile reads or effects. A measured profile
must pass host/toolchain/control/ownership checks before any job allocation.
Initialization is a separate explicit `--initialize-journal` operation, allowed
only on a pristine evidence directory under the same measured controller boundary.
It cannot recreate a missing journal alongside existing run evidence.

### Trusted pre-code gate

Systemd starts a trusted bootstrap using `Type=exec`. The controller observes actual
cgroup limits and service identity before release. Bootstrap must be PID1 in a
distinct PID namespace; it verifies namespace-scoped `/proc`, identity mappings,
groups, empty capabilities, no-new-privileges, seccomp, descriptor/environment
inventory, read-only cgroup hierarchy and exact writable mounts. Trusted canaries
attempt privilege changes, namespace creation/entry, remount and IP socket use.
Unknown observations or degraded directives close the gate.
Pinned native `env -i` clears inherited environment before Node starts; loader and
Node injection variables are also explicitly unset by the service properties.

The mock has a separate cgroup and UID. Its directory is mode0750 with the shared
group. The controller verifies positive socket ownership before assigning that
group; the socket is mode0660. A trusted outsider helper drops supplementary groups
and its UID/GID before checking EACCES inside the mock mount namespace. Worker gets
only a read-only bind of that directory. Its real bootstrap must complete the
authorized UDS exchange and fail directory-write/socket-replacement canaries.

Gate and fixture HTTP servers are separate sequential instances, with independent
counters. The gate instance accepts one request and retires before repository
release. The fixture instance preserves the three exact POST paths, disposable
synthetic token, epoch7, `fixture/route`, 4096-byte request cap, output parameter
ceiling64, three accepted requests and four concurrent connections. Aliases,
management, methods, scope/epoch/route changes and extra routing fields are rejected.
No actual 9Router, provider, management service or model is contacted.

After both external and bootstrap checks, a root-owned release record permits
`execve` to replace trusted bootstrap with the public repository fixture while
preserving PID1. No repository callback, Git hook, build/plugin executable or
pressure operation runs before release.

### Fixed resources and evidence

| Role/resource | Frozen bound |
| --- | --- |
| Worker and mock, each | `cpu.max=50000 100000`; memory128MiB; swap0; tasks64; nofile256; core0 |
| Worker writable mounts | `/work`32MiB/4096 inodes; `/tmp`8MiB/1024; `/dev/shm`4MiB/1024; `/log`1MiB/64: total45MiB |
| Mock writable mounts | `/router`1MiB/64; `/log`1MiB/64: total2MiB |
| Tmpfs flags | noswap, nodev, nosuid; noexec except `/work` |
| Service allowance/reserve | Non-payload32MiB plus reserve16MiB; planned worker93MiB and mock50MiB within each 128MiB ceiling |
| Logs | Service writes its own bounded tmpfs log; stdout/stderr bypass journal; preallocated16KiB result record remains writable under log ENOSPC |
| Per-invocation evidence envelope | 16MiB/128 files, counting reserved journal1MiB/16 files; raw observations capped at10MiB, terminal receipt reserve512KiB |
| Service lifetime | Restart=no; NotifyAccess=none; RuntimeMaxSec=60s; random extension0; KillMode=control-group; stop timeout1s; SIGKILL enabled |

Existing evidence is retained on disk; fresh host free-space checks account for it
before each invocation. The append-only profile journal is shared across invocations
and separately capped at1MiB. Neither evidence nor journal exhaustion raises limits.

Byte and inode pressure are separate on every writable mount. Each records a
successful below-limit control, the failing syscall/error, statfs deltas and
memory/shmem/events. ENOSPC requires zero OOM deltas; byte pressure must leave free
inodes, and inode pressure must leave free blocks. The external controller checks
terminal statfs and OOM counters. Anonymous-memory pressure uses fresh mounts,
records a touched allocation control and requires actual `oom`/`oom_kill` counters;
ENOSPC cannot substitute. CPU, task, swap and actual log-append flood cases are separate.

### Cases and outcome semantics

| Cases | Native operation/evidence required | Status |
| --- | --- | --- |
| `baseline` | Synthetic host sentinels; symlink/proc escape; actual independent Git repository and hostile pre-commit; descendant and executable plugin; scoped inference negatives/positives | NOT RUN |
| `missing-pids-limit` | Owned worker service with `TasksMax=infinity`; actual `pids.max=max` rejects before repository-start; durable rejection and positive-owned cleanup receipt | NOT RUN |
| `runtime-drift` | Mutate copied expected kernel identity only; actual host comparison rejects before allocation | NOT RUN |
| `cpu`, `pids`, `swap`, `oom` | Throttling, EAGAIN/task events, zero swap under touched allocation, separate anonymous OOM counters | NOT RUN |
| `bytes-{work,tmp,shm,log,socket,mock-log}` | Independent byte ENOSPC per mount; zero OOM; external statfs | NOT RUN |
| `inodes-{work,tmp,shm,log,socket,mock-log}` | Independent inode ENOSPC per mount; free byte capacity; zero OOM | NOT RUN |
| `log-flood` | Append actual worker log to ENOSPC; preserve preallocated result and external observations | NOT RUN |
| `egress` | Identical hashed probe/endpoint argv outside and inside; TCP/UDP/DNS IPv4/IPv6 loopback stand-ins, forced raw ICMP; external listener and ICMP counters | NOT RUN |
| `stop`, `expiry`, `parent-exit` | Detached, TERM-resistant descendants; empty complete tree within5s of stop or65s of activation; observed parent exit | NOT RUN |
| `launch-interrupt-before-gate`, `launch-interrupt-after-gate` | Inject interruption at release boundary, stop both roles, preserve immutable failure/quarantine | NOT RUN |
| `controller-loss` | Intent and raw observations fsynced before deliberate controller SIGKILL; PID1 remains responsible for deadlines; later independent observation required | NOT RUN |
| `observation-loss` | Drop observation descriptor, refuse acceptance, stop/revoke both roles and retain failure receipt | NOT RUN |
| `reboot` | Durable fault-ready window for separately authorized console power-cycle; new boot, old-resource absence and next-launch rejection still require later observation | NOT RUN |

Egress destinations are local synthetic stand-ins, never public/provider/router
addresses. The source rejects timeout-only results and unavailable positive
controls. DNS uses a fixed local answer with no forwarding. These operations are
confinement evidence only; they make no complete #2 grant/streaming or live-model claim.

`case-passed` can describe only the exact measured tuple and that one case after
complete cleanup. It is not an aggregate native proof or product enablement.
`expected-rejection` describes the missing-limit/drift negative cases. Interrupted
and uncertain runs retain failed/quarantined state; successful cleanup does not
rewrite their failure into a pass. Unavailable observations fail closed.

## Durability, loss and cleanup

Before any unit/listener/mount allocation, controller appends blocked intent binding
run ID, profile hash, boot, deterministic unit names and resources. Every journal
append fsyncs file and parent directory. Exclusive append lock serializes writers;
torn records, bad hashes/transitions, absent journal, stale lock, exhausted reserve,
unresolved intent and replayed run IDs deny the next launch. One bounded append-only
file accommodates the case campaign without treating a sixteen-file cap as a
sixteen-record limit.

Raw observations are incrementally written and fsynced on disk outside worker
mounts. Final receipt is exclusive-create, mode0400, content-hash named and hash-bound
from the journal. Failure/quarantine must be durable before reporting failure.
Failure-writing errors leave unresolved intent rather than manufacturing acceptance.
Resolution appends evidence referring to the failed record; it cannot rewrite that
receipt, replay its run ID or turn failure into pass.

Before stop or `cgroup.kill`, controller checks unit invocation, cgroup path and
device/inode identity. Cleanup requires inactive/failed unit state, populated0 (or
verified absent cgroup), and no owned processes in an independent `/proc` scan.
Unknown/reused ownership prevents destructive cleanup. Systemd owns ephemeral mount
teardown; controller closes its observation references after complete-tree checks.
No recursive host deletion, prune, broad unmount or unrelated-service stop exists.
Task-owned disk evidence stays available for review.

After real controller loss or power-cycle, root must obtain independent observations
of the exact recorded resources. Missing logs are not termination evidence. Freeze
the new boot measurement after reboot and actually attempt the next launch before
any repository sentinel; old boot identity and unresolved intent must reject it.
No automatic resume, product recovery, permit implementation or live deployment
inspection is provided. Post-loss observation/reconciliation is a later root gate,
not fabricated by the offline journal tests or the fault injection entrypoint.

## Evidence separation and outstanding integration

| Reused #5 logic | Newly measured native controls | Outstanding product evidence |
| --- | --- | --- |
| Public synthetic sentinels, direct/symlink/proc escape attempts, actual Git hook, descendant and plugin attacks | None. Native UID/maps/namespaces/mount/seccomp/cgroup enforcement NOT RUN | #16 runner integration; #21 execution authority |
| Three-path fixed HTTP fixture, token/epoch/route/envelope negatives | None. Native scoped UDS ownership, outsider denial, gate-instance separation and egress positive controls NOT RUN | #24 inference/adapter integration; full accepted boundary contracts remain controlling |
| CPU/PID/OOM ideas, tightened to separate native byte/inode/OOM evidence | None. Native pressure, shmem/controller accounting and log bounds NOT RUN | #47/#57 integration and readiness evidence |
| TERM-resistant detached process tree | None. Native stop, expiry, interruption, controller loss, observation loss and real reboot NOT RUN | #60/#61 operational/installation evidence |
| Docker result retained solely as comparison | None. Disk-backed native journal/quarantine, cleanup and independent reproduction NOT RUN | #62 maintenance/support guide; #63 independent installation/recovery evidence |

#89 prerequisites #2/#5 remain controlling. #9 still requires #71/#1/#8 and its
foundation decision before durable application core work. Source implementation
does not complete #89 or any downstream issue. `initial_20260915` remains spent1,
unknown and halted; its original deployment, evidence and one-use state were not
accessed or changed.

## Safe local verification

From repository root, ordinary macOS user:

```sh
node --test experiments/isolation-native/run.test.ts
```

Nine behavioral checks cover absent/malformed/default/non-Linux admission,
missing effective limits before a simulated sentinel, exact cleanup ownership,
missing/corrupt/torn/symlinked journals, durable unresolved intent, competing stale
admission, immutable failed receipts and non-replayable resolution, journal bounds,
inert preparation/native CLI platform refusal, and replay-time rejection of correctly
hashed resolutions with missing or conflicting cleanup observations. Synthetic test values are not
profile measurements. Strict TypeScript checking uses a compiler and Node types; no install is needed for
the runnable check above. With the existing inference-boundary development tools:

```sh
experiments/inference-boundary/node_modules/.bin/tsc --noEmit --strict \
  --target es2023 --module esnext --moduleResolution bundler \
  --allowImportingTsExtensions --types node \
  --typeRoots experiments/inference-boundary/node_modules/@types \
  experiments/isolation-native/run.ts experiments/isolation-native/probe.ts \
  experiments/isolation-native/run.test.ts
```

The dedicated `Native confinement source checks` workflow runs only these offline
checks on Linux and macOS. Its Linux runner is not a selected confinement target;
passing CI provides no native runtime evidence.

Source refresh on **17 September 2026** preserves the reviewed procedure and the
unmeasured default profile. Journal resolution details are validated both when
appended and when replayed after restart; a valid hash alone cannot clear
quarantine without a matching failed record and empty, inactive resources.

This evidence document is not an input to `docs/build-reader.ts`; no reader source,
layout or generated `docs/index.html` changed. Fresh independent exact-head source
review, applicable CI, measured Linux execution, fault/reboot observations and
independent reproduction remain root's gates before any merge or proof claim.
