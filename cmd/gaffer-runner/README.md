# Gaffer development runner

`gaffer-runner` is the trusted supervisor for the provisional execution channel.
The job is an untrusted subprocess. This lane does **not** qualify unattended
execution or accept #9/#10: `macos-sandbox-exec-dev` always records
`qualification: development`, `supported: false`. The default `unqualified`
profile refuses launch.

## Commands and local authority

Build with `go build -o /private/tmp/gaffer-runner ./cmd/gaffer-runner`.

```sh
/private/tmp/gaffer-runner serve \
  --state-dir /private/tmp/gaffer-runner-state \
  --daemon https://127.0.0.1:7444 --daemon-fingerprint "$DAEMON_SHA256" \
  --cert /private/tmp/runner.crt --key /private/tmp/runner.key \
  --repository-root /private/tmp/gaffer-checkouts \
  --isolation-profile macos-sandbox-exec-dev --harness fake \
  --policy /private/tmp/gaffer-runner-state/policy.json \
  --repository-profile /private/tmp/gaffer-runner-state/repository.json
```

Provision canonical, private mode-0700 state and checkout roots first. The checkout
root must be outside an existing working or bare repository. An exclusive
`serve.lock` prevents two supervisors from owning one state directory. Every
process start atomically replaces `boot.json`; it never reuses the old boot.

The operator supplies two independent local inputs; neither is downloaded policy:

* `--policy` defaults to `<state-dir>/policy.json`, a full `scheduler.Eligibility`
  including the runner-local envelope. It must be a regular file not writable by
  group/others. `Accept` reloads it independently and compares it with the daemon's
  exact eligibility mirror. Changed or expired policy stops the accepted attempt.
* `--repository-profile` defaults to `<state-dir>/repository.json`, a full
  `repositories.Profile`. Its digest, pinned remote/base, repository revision and
  runner-root selection must match the dispatch. `repositories.Prepare` makes an
  independent clone with disabled hooks, filters and ambient Git configuration.

Task content comes from authenticated `GET /x/v1/input?dispatch_id=...`.
The runner verifies the task/dispatch/repository/base/harness and the exact grant
paths/operations. `brief_sha256` must equal the grant's brief digest and SHA-256
of canonical `{brief,criteria,paths,operations,harness,settings}`, in that order,
with recursively sorted settings keys. The input is journaled before acceptance.
`--brief-file` and `--fake-spec` are reserved test overrides and **refused when
authenticated daemon input is present**; they cannot replace approved input.

Other flags are `--gateway-config`, `--opencode-bin`, and `--spool-bytes`
(default 4 MiB, minimum 64 KiB). The standalone S2 build registers only `fake`.
The `harnessFactories` seam accepts the separately implemented OpenCode adapter;
adding its registration at integration is necessary before `--harness opencode`
works. A non-fake adapter must pass a startup config-isolation probe. The cache is
bound to the current boot, binary digest, profile runtime digest, route and clean
environment revision; a changed key reprobes. Probe launchers allow only their
own dynamically allocated loopback endpoint, not arbitrary loopback traffic.

## Facts lifecycle and the gateway

After **every** supervisor restart:

```sh
/private/tmp/gaffer-runner facts --state-dir /private/tmp/gaffer-runner-state \
  --gateway-config /private/tmp/gateway.json > /private/tmp/facts.json
# With the separate owner CLI implementation:
gaffer runner facts import --file /private/tmp/facts.json --expected-revision 0
```

Use the actual prior eligibility revision instead of `0` for later imports.
`facts` reads `boot.json`, measures that the selected profile can start a clean
process, updates the local policy snapshot atomically, and prints the same
eligibility JSON for owner import. Until current-boot facts are imported, the
session remains `recovery_only`; old journals can be reconciled but not relaunched.

Facts use route `worker-dev`, the gateway ID as profile/provider, billing
`gateway-managed`, and limits `gateway-local-bounds-v1`. Provider token/cost hard
bounds are **not** claimed. `RouterAuthenticated` becomes true only after a real
authenticated HTTPS `GET /v1/models` succeeds without a proxy or redirects.
`--gateway-ca FILE` trusts an explicit test CA for that probe. Without a gateway,
fake facts annotate `fake-no-inference` but leave authentication false, so scheduler
admission refuses. Fake jobs do not make inference requests.

For synthetic tests, start a real local authenticated HTTPS gateway:

```sh
# Supply a synthetic, mode-0600 bearer key; never a provider credential.
/private/tmp/gaffer-runner mock-gateway \
  --listen 127.0.0.1:0 --key-file /private/tmp/mock-token --models gpt-6-astra
```

This **test-only** command refuses non-loopback binds and prints JSON containing
`url` and `ca_file`. Its ephemeral certificate's public CA is written beside the
key as `<key-file>.ca.crt`; the TLS private key stays in memory. A
`gateway-config-v1` referencing that URL and synthetic key can be probed with
`facts --gateway-ca <ca_file>`. Only authenticated models and canned
`/v1/chat/completions` routes exist. It is not a replacement model gateway.

## Durable sequence and failure behavior

1. Hello with retained journals; inbox; validate task input; durable `Accept`.
2. Journal lease nonce/send time, obtain lease, install conservative cutoff.
3. Prepare the boundary and workspace; journal/propose `launch_intent` with
   `guardian_pid: 0` (not spawned yet); wait for starting acknowledgment.
4. Launch a separate-session guardian. It launches the sandboxed job, reports
   PID/PGID/start time, then the supervisor journals `launched` before proposing
   running. The job starts immediately after the guardian starts it; a crash in
   the running-ack gap is recovery, not permission to retry.
5. Spool native and normalized events before upload/acting on approval. The final
   8 KiB of usable spool capacity is reserved for terminal `spool_full`; previously
   acknowledged native bytes are never discarded. Approval is never bypassed.
6. Observe process-group disappearance; close/drain the boundary; check every
   changed/untracked/deleted path against the grant and repository protections;
   pack a succeeded or failed manifest. Custody is not finalization.
7. Replayable begin/blob/commit; persist receipt; post usage; finalize. Only a
   positive released finalization permits workspace deletion. Optional adapter
   `Release(ctx, handle, through)` runs once afterwards, with the last fully
   recorded harness event sequence (not the chunk sequence). Cancellation drains
   and acknowledges all remaining output before reporting terminated/releasing.
   Spool exhaustion or incomplete custody retains the adapter handle and can
   eventually refuse new admission; it never drops unspooled bytes to release it.

Cancel is journaled before stopping. `TerminateProcessGroup` records request,
acknowledgment and observation separately. TERM escalates at two seconds, with a
five-second observation bound. Lease expiry uses `ExpiryStopID(last_nonce)`.
The guardian independently stops on its deadline or supervisor stdin EOF (even
SIGKILL), and writes a synced private receipt. EPERM is inconclusive, not ESRCH.
A detected setsid escape remains unknown; no terminated message or release is
claimed, even when best-effort cleanup killed the observed escaped PID.

`Open` consumes guardian recovery evidence, only re-signals a still-matching
PID/start token if needed, then writes a fresh restart boot and sticky quarantine.
Old messages retain their IDs; generation/boot refusals become durable `fenced`
events. Old-boot termination and receipt accounting are replayed for daemon
reconciliation; no old lease is installed. Uploads interrupted before a complete
outbox/finalization record stay preserved and may still need operator recovery.

## Verification

`go test -race -count=1 ./cmd/gaffer-runner ./internal/runner` runs real TLS against
an in-test daemon, a real built runner executable, real guardian/job processes,
real SIGKILL recovery and macOS sandbox canaries. It tests lost commit replies,
lease expiry, TERM-ignore escalation, setsid escape, stream exhaustion, approval,
crash/nonzero exit, envelope violation and refusal paths. This is lane evidence,
not a test of another lane's daemon route/store transaction implementation.
