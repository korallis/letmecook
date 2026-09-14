# Linux isolation experiment: containment passes, acceptance awaits review

Related to [issue #5](https://github.com/korallis/letmecook/issues/5).
Observed **14 September 2026**. This is an independent M0 experiment, with no
application runtime, harness adapter, account router or publication path.

The real Linux containers passed the containment probes below, including the
scoped inference endpoint's isolation and resource checks. **Acceptance of #5
awaits its accepted #2 prerequisite and independent review.** The linked
[9Router contract](../contracts/9router.md) defines the integration boundary.
This run deliberately uses a synthetic inference fixture; a synthetic HTTP 200
is not model output. Combined live harness/router/model proof belongs to #6/#7,
and is not an additional acceptance criterion for this isolation experiment.
The experiment does not enable a product unattended runner.

## Exact profile and prerequisites

Profile ID: `m0-linuxkit-arm64-docker29-uds-v1`.
This is one recorded experiment profile, not a required product topology or host.
Operators select their own infrastructure; no hostname is embedded in the launcher.
`GAFFER_ISOLATION_PROFILE=/absolute/path/profile.json` selects a trusted operator
profile with the same schema as the [recorded profile](../../experiments/isolation/profile.json),
including the exact engine, kernel, architecture and digest-pinned source image.
`GAFFER_ISOLATION_DOCKER_CONTEXT=<context>` selects the Docker context without
changing Docker's shared current context. The default records `docker context show`
and then passes that context explicitly on every command. The observed context was
`desktop-linux`. Profile selection never disables the security/resource checks or
enables the unattended gate. Other combinations require fresh proof and review.
The fixture bind directory must exist on the selected daemon host; execute the
tooling on that host for a remote operator-managed machine. A remote Docker context
alone does not copy the fixtures or establish compatibility.

| Component | Observed / selected value |
| --- | --- |
| Execution OS | Linux `6.12.76-linuxkit`, ARM64, cgroup v2 |
| Container engine | Docker `29.7.2`, API `1.55` |
| Desktop VM host | Docker Desktop `4.86.0 (236216)` on macOS `26.6.2 (25G83)`, ARM64 |
| OCI implementation | containerd `2.2.5`, runc `1.3.6`, docker-init `0.19.0` |
| Host experiment tooling | Node `26.8.2`; requires Node 24+ and Docker CLI access |
| Worker image | `node:24-bookworm@sha256:6dac556d980b7f0e5498d08f08cee0ca67798b4ad6c23964a9214920e67758d0` |
| Worker tools | Node `24.21.0`, Git and Debian release recorded in raw evidence |
| Required controls | memory and swap accounting, CFS CPU quota, PID limits, built-in seccomp |

The selected execution boundary is the **Linux container inside Docker's VM**.
This provides no evidence for executing repository code directly on macOS or
Windows, or for a different Linux kernel, CPU architecture or Docker version.
Runtime drift fails the preflight rather than silently broadening support. The
Docker daemon, VM, kernel and host supervisor are trusted; these probes are not a
proof against unknown kernel/runtime vulnerabilities.

Only a small, task-created directory containing the three reviewed fixture files
is mounted from the host, read-only at `/fixture`. There is no repository/home,
runner-policy, daemon-state, cache, SSH-agent, provider-secret or Docker-socket
mount. The worker creates its own empty Git repository in `/work`, so the Git hook
test uses actual Git administration inside the boundary, without shared worktree
metadata. Synthetic secret and policy sentinels remain outside the mounted fixture
directory. Their paths, rather than real secret values, are supplied to adversaries.

| Control | Enforced setting |
| --- | --- |
| Identity | UID/GID `1000:1000`, all capabilities dropped, no-new-privileges, Docker built-in seccomp |
| Namespaces | Private PID, IPC, cgroup and network namespaces; Docker init enabled |
| Root filesystem | Read-only; no additional devices, host namespaces or published ports |
| Workspace | `/work` tmpfs, executable, 32 MiB, 4,096 inodes, UID 1000, mode 0700, nosuid/nodev |
| Scratch | `/tmp` tmpfs, noexec, 8 MiB, 1,024 inodes; `/dev/shm` 4 MiB |
| CPU | 0.5 CPU (`cpu.max = 50000 100000`) per container |
| Memory | 128 MiB per container, no swap; tmpfs pages count toward memory |
| Processes / FDs | 64 tasks per container, nofile 256, core dumps disabled |
| Logs | Docker local driver, 1 MiB rotation target, one file, compression disabled |
| Egress | `--network none`; only loopback exists, with no inference listener on it |
| Inference socket | Task-owned 1 MiB tmpfs named volume at `/router`; read-only in worker, writable in trusted fixture |
| Lifecycle | No restart policy; trusted host supervisor inspects settings before `docker start` |

Writable scratch, standard device tmpfs and Docker log metadata are additional to
the workspace. Scratch remains memory-bounded; the log rotation target is not an
exact byte quota on Docker's metadata. Image cache and daemon-wide disk allocation
belong to the trusted host. This experiment does not provide production admission,
disk reservation or artifact durability. Two containers run concurrently during
most probes: the worker and trusted synthetic inference fixture.

## Reproduce and fail-closed cleanup

From the repository root:

```sh
# Resolve Docker Desktop's credential helper if it is absent from this shell's PATH.
export PATH="/Applications/Docker.app/Contents/Resources/bin:$PATH"
docker pull --platform linux/arm64 node:24-bookworm@sha256:6dac556d980b7f0e5498d08f08cee0ca67798b4ad6c23964a9214920e67758d0
npm ci --prefix experiments/isolation
npm --prefix experiments/isolation run check
npm --prefix experiments/isolation test
npm --prefix experiments/isolation run prove
```

`prove` writes [the redacted run record](linux-profile-run.json), including exact
Docker argument arrays, runtime identity, preflight settings, observations and
cleanup result. Pass a different output path with
`node experiments/isolation/run.ts /absolute/path/run.json` to retain another run.
The fixture token is randomly generated per run, inherited through the Docker CLI
environment and excluded from command arguments and the report. No real credentials
are needed. Container commands run TypeScript using the digest-pinned Node image;
there is no shell command evaluation of repository-provided arguments by the host.

The procedure persists a blocked run marker before any Docker mutation, assigns
unique task-owned container names and labels, creates the socket volume, creates
each stopped container, verifies its inspected configuration, and only then starts
it. A failed runtime check creates no container. Failure during a probe refuses
further launches. CLI operations have deadlines; cleanup runs even after failure.
SIGINT/SIGTERM are checked again immediately before each start. Pending observation
and container-wait CLIs and polling delays are interruptible, so stop proceeds to
cleanup without waiting for the probe deadline. In-flight mutations settle within
their existing CLI deadline before ownership reconciliation; cancelling the CLI
alone would not establish that the daemon had finished creating or starting a
container. Cleanup inspections, removals and final label queries remain active
after stop, including repeated signals. An interrupted run always remains blocked.
The result is accepted only after removing the run's containers/volume and checking
that none with its exact run label remain. The gate always records
`unattendedSupported: false`: this is an experiment, not a product execution
enablement switch. The report identifies required independent review and #2's
prerequisite without attempting to infer GitHub acceptance status at runtime.

If the supervisor is killed or the Docker daemon becomes unavailable, the initial
record remains blocked with cleanup unverified. **Quarantine that execution profile**:
do not treat timeout, missing logs or a disconnected CLI as proof of termination.
Recover Docker access, inspect the exact `runId` from the record, and check only
resources labelled `dev.gaffer.isolation-experiment=<runId>`. Remove the matching
containers with `docker rm -f <verified-owned-container>` and then their matching
socket volume with `docker volume rm <verified-owned-volume>`. Verify both exact
label queries are empty and rerun the proof. Do not prune or remove unrelated
Docker resources. This manual recovery rule is not a durable product supervisor.

## Observations and limits

The committed JSON is the authoritative raw result; timings and PID values vary.
The separate [supervisor-stop run](linux-profile-stop-run.json) sent SIGTERM after
Docker reported the probe container running. The launcher exited with code 1 in
178 ms, recorded a blocked result, and verified zero owned containers and volumes
remaining. This supplements the fixture's deliberate TERM-resistant process-tree
test with an actual signal to the host supervisor.

| Probe | Observed result |
| --- | --- |
| Kernel identity and restrictions | UID 1000; permitted/effective/bounding/ambient/inheritable capabilities all zero; `NoNewPrivs: 1`, `Seccomp: 2`; setuid(0) failed with EPERM |
| Root, fixture and socket mounts | Root/fixture writes and socket-directory write/unlink failed with EROFS |
| Host paths and secrets | Direct, symlink, `/proc/1/root`, real Git pre-commit hook and child-process attempts failed with ENOENT/EACCES; both host sentinels remained unchanged |
| Host services / general egress | Public IPv4/IPv6, gateway address, metadata address, localhost router port and external DNS failed; only `lo` existed |
| Scoped synthetic inference | Exactly three allowed POST requests succeeded over the Unix socket; a fourth returned 429 |
| Synthetic inference negatives | Management, alias, query, traversal and absolute-form paths / non-POST methods denied; invalid token 401; wrong epoch/model 403; unknown field 400; request bytes 413; excessive output limit 422 |
| CPU pressure | `nr_throttled` increased during a busy loop at the 0.5 CPU quota |
| Process pressure | `pids.current` reached 64; subsequent spawn failed EAGAIN and `pids.events` recorded the limit |
| Disk pressure | `/work`, `/tmp` and `/dev/shm` fills hit ENOSPC at or below their configured capacities |
| Memory pressure | Touched allocations exceeded the 128 MiB limit; Docker recorded OOMKilled, exit 137 and stopped state |
| Cancellation | Leader, detached child, grandchild and their sleeps existed before stop; they ignored TERM; Docker escalated after one second to exit 137, `Running=false`, init PID 0; subsequent exec was rejected |
| Cleanup | All task-owned containers and socket volume removed; exact label queries empty |
| Failure handling tests | Fourteen tests pass: runtime/missing controls rejected; cleanup failures propagate; real launcher subprocesses receiving SIGINT/SIGTERM during create, inspect, wait and readiness polling refuse further execution and promptly clean up; ownership mismatch still blocks removal |

Cancellation evidence uses Docker's container/task state and PID namespace, with
the actual process tree observed before stopping. It does not prove upstream model
or provider cancellation, because no model/provider was called. The fixture serves
fixed small JSON and contains no 9Router credentials, upstream transport, account
selection, retry logic, policy writer or lease controller. Its token/epoch/model,
request-byte, output-token and attempt-count checks demonstrate the exposed socket's
limited surface; they do not implement the complete #2 grant, JSON parsing,
revocation, streaming or admission contract. The worker cannot reach the actual
router management network because it has no IP network; the fixture separately
rejects management paths. Actual router configuration/authentication is outside
this isolation proof and is exercised by the combined harness tests in #6/#7.

For #5 acceptance, accept the #2 prerequisite and independently review this
containment evidence. Combined live harness/router/model tests remain in #6/#7;
they do not change the observations or add an acceptance criterion here. Product
execution enablement is outside this experiment. Other native Linux hosts require
their own observed runtime pin and proof; this run does not certify them.

Docker documents the [container execution and namespace controls](https://docs.docker.com/engine/containers/run/)
and [resource controls](https://docs.docker.com/engine/containers/resource_constraints/).
Those documents explain the configuration; the committed run establishes what was
actually observed on this runtime.
