# Isolated synthetic checks

`runCheck(job, store, signal)` executes the exact frozen synthetic registration's
Node check argv. It verifies the retained registration, input and executable
records, reconstructible snapshot bytes, and actual pinned image/runtime identity.
It executes no repository code on the host and never evaluates a shell string.
This module depends on the adjacent artifact store; it does not grant execution,
inference, acceptance or publication authority.

The only image is
`sha256:f95ae5b2218838d3da613273705b7914a149593c400a33087e9c383c2e12650a`
(`gaffer-router-extension-deps:0.5.75-locked`, Linux ARM64). `measureRuntime(store,
signal, absoluteDeadline)` runs a fixed probe, retains its evidence, and returns
actual Node version, binary digest, environment digest and image identity. These
values must be frozen in the registration before constructing a check job. The
required image runtime is Node 24.21.0; a host Node version is not that identity.
Probe failure throws `RuntimeFailure`, with retained `evidence` and `cleanup`.

Retained executable records have shape
`{schema:1,kind:'baseline-check-executable',file:SnapshotFile}`. Registered input
records have shape
`{schema:1,kind:'baseline-check-inputs',files:SnapshotFile[],executable:{path,sha256},imageDigest}`.
The executable must match its snapshot entry byte for byte. Additional input files
are supplied through `/inputs`; the snapshot is at `/repo`. Registered argv starts
with `['/usr/local/bin/node','/repo/<executable-path>']`. `cwd:'.'` maps to `/repo`;
another registered relative cwd must exist within that snapshot.
The candidate must preserve the complete base file inventory and modes, with
content changes confined to registered write paths. Caller job/store objects are
copied before asynchronous reads; checked bytes are staged under a private path.

Each probe/check receives its own container: 128 MiB memory and memory-swap limit,
32 PIDs, half a CPU, no network, read-only root and input mounts, a 16 MiB noexec
tmpfs, UID/GID 65532, all capabilities dropped and no new privileges. Actual Docker
inspection is checked before start. Creation uses `--pull=never`, an explicit
environment, private IPC/cgroup namespaces, no healthcheck and no Docker log file.
No inference, control, provider or Docker socket is mounted. The host invokes
`/opt/homebrew/bin/docker` with argument arrays and the fixed local socket
`unix:///Users/leebarry/.docker/run/docker.sock`, ignoring ambient Docker context.
Checks may
create descendants within the container; final removal stops the whole container.

Jobs require an absolute deadline within 60 seconds and a combined stdout/stderr
bound of 1–65536 bytes. The deadline covers admission, runtime probing and the
check, and is never refreshed. Cleanup gets a separate five-second bounded grace
period after a deadline or abort. Output flooding stops capture at the declared
byte bound and forces removal. Timed-out, interrupted or unconfirmed outcomes are
`unknown`; they cannot be passed. Nonzero observed exits remain `failed`.
Cleanup failure remains visible even after an otherwise successful check. Retained
observations contain bounded base64 logs, job/snapshot identity, actual inspection,
timestamps, exit/state and cleanup diagnostics. Container names remain recorded
even if create never acknowledges an ID; an uncertain create stays fenced even
after an immediate absence query. A persistence error throws rather
than returning an unretained acknowledgement.

The test-only `runCheckWithCleanupFailure` simulates a lost cleanup acknowledgement
after real removal. It can only turn a result into a failure; it does not relax
commands, bounds or isolation. It does not claim a real Docker daemon failure.
The retained execution explicitly identifies this injected fault.
The caller must treat every unconfirmed cleanup as a failure and fence later work.

Run with Node 24 and the locked baseline dependencies:

```sh
node experiments/baseline/node_modules/typescript/bin/tsc -p experiments/baseline/checks/tsconfig.json
node --test experiments/baseline/checks/checks.test.ts
node experiments/baseline/checks/proof.ts /private/new-synthetic-proof-directory
```

Tests use real containers for the two-file pin fixture, inherited frozen fake
audit failure, writes/egress/control isolation, timeout, flood, detached
descendants, abnormal exit, abort and cleanup uncertainty. Admission tests cover
tampered jobs, executable/input/snapshot/image/runtime identities. The proof command
retains actual synthetic base/candidate check evidence. These are small fixture
checks, not a real advisory audit, full workflow parser, upstream CI execution,
private baseline registration or live model call. They do not establish that the
private operator repository fits the same resource profile.

Container tests require access to the local Docker daemon and the already-loaded
pinned image. Docker access denial is a failing test, never a skip or a simulated
container success. Admission and native CLI output-bound tests can run separately:

```sh
node --test --test-name-pattern 'admission|tampered|native Docker CLI' experiments/baseline/checks/checks.test.ts
```
