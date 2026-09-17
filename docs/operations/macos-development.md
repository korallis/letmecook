# macOS development runner

This is an operator-authorized **development** path, not a supported unattended
execution runtime. All facts and launch records say `qualification: development`
and `supported: false`; none accepts #9/#10 or proves the native isolation work
in #89. The default `unqualified` profile refuses launch.

## Setup and facts import

Build `cmd/gaffer-runner` and provision private canonical directories outside any
working/bare repository. Use absolute `/private/tmp/...` paths rather than the
`/tmp` symlink. Supply a full independently reviewed local eligibility policy and
repository profile; see [runner commands](../../cmd/gaffer-runner/README.md).
Start the daemon's separate execution listener with its explicit development
profile flag, enroll the runner certificate, then:

```sh
gaffer-runner serve \
  --state-dir /private/tmp/gaffer-runner-state \
  --repository-root /private/tmp/gaffer-checkouts \
  --daemon-state-dir /private/tmp/gafferd-state \
  --daemon https://127.0.0.1:7444 --daemon-fingerprint "$DAEMON_SHA256" \
  --cert /private/tmp/runner.crt --key /private/tmp/runner.key \
  --isolation-profile macos-sandbox-exec-dev --harness fake \
  --gateway-config /private/tmp/gateway.json
```

Use `--daemon-state-dir` for a colocated daemon (omit it for a remote daemon);
it denies the configured private state path, including identity/database files.
Every start mints a fresh `boot.json`. In another terminal, generate/import facts
for **that** boot before dispatching anything:

```sh
gaffer-runner facts --state-dir /private/tmp/gaffer-runner-state \
  --gateway-config /private/tmp/gateway.json > /private/tmp/facts.json
gaffer runner facts import --file /private/tmp/facts.json --expected-revision 0
```

Replace `0` with the previous eligibility revision on later imports. Owner
approval also needs `--allow-development-isolation`; runner, daemon and owner
choices must agree. The runner stays recovery-only until the owner imports its
new boot. Restarting never resumes an old journal or silently frees a reservation.
The S2 standalone binary registers fake; OpenCode registration is an integration
step using the separately implemented adapter and its config-isolation probe.

Authentication facts are real observations, even for fake jobs: without a gateway
probe they stay false and admission refuses. For tests use the loopback-only
`mock-gateway` command with a synthetic mode-0600 token. It prints its HTTPS URL
and public CA file; use `facts --gateway-ca <file>`. Do not copy an operator gateway
credential into fixtures, repository content or worker configuration.

## What the profile does

The profile uses Apple's `/usr/bin/sandbox-exec` with the **allow-default** shape
measured to let the pinned OpenCode binary start on this Mac. A deny-default
profile previously prevented startup and is not silently substituted. The narrower
per-attempt write and signal rules now pass real native canaries; the pinned
OpenCode startup/config-isolation probe must be rerun in the integrated system
proof before claiming compatibility with those stricter rules.

* Deny network access except the attempt boundary's one loopback port; the fake
  no-boundary case denies all network. Another loopback port is not an exception.
* Deny signals to outside processes; permit signals only to same-sandbox children.
* Deny file writes to the repository root and everywhere else except the attempt's
  candidate/private HOME/TMP/runtime directories and the necessary null/TTY devices.
  Neither `/private/var/folders` nor `/private/tmp` is a general write exception.
* Deny read-data and writes to HOME `.config` (including gh/gcloud), `.claude`,
  `.claude.json`, `.codex`, `.ssh`, `.aws`, `.azure`, `.netrc`, `.authinfo`, `.gnupg`,
  `Library/Keychains`, `.gitconfig`, `.git-credentials`, `.npmrc`, `.pypirc`,
  `.docker`, `.kube`, OpenCode local data/state and Gaffer local state/data;
  also deny canonical aliases, the gateway credential/configuration, runner mTLS
  key/state, and the configured colocated daemon state directory.
* Use a clean private HOME and XDG configuration/data/state/cache. Runtime files
  live outside the candidate, never in a packed artifact.
* Run a separate-session trusted guardian with file-size, descriptor and CPU-time
  rlimits. Its job uses a distinct process group. TERM escalates to KILL at two
  seconds and disappearance is bounded/observed, never inferred from a signal.

The profile is emitted with escaped paths for every attempt. Native tests really
run sandbox-exec: they deny a synthetic HOME-secret canary, credential/state
canaries, outside signals, unrelated temporary/repository writes and a second
loopback listener, while allowing same-sandbox signals, a HOME-rooted candidate
write and the selected boundary listener.

## What it does not prove

Allow-default is a compatibility profile, not comprehensive least privilege.
The contract identifier `secret-separation` under this development profile means
only that these enumerated secret paths are denied to the job. It is **not full
host-secret isolation**: shell rc files, preferences, `/etc` and other unlisted
host data remain readable. `mach-lookup` remains open, including
securityd/keychain IPC; path canaries do not prove isolation from those services. This is not a VM/container, a complete secret
inventory, a provider-cost/token guarantee, or measured adversarial confinement.
Resource rlimits do not establish a hard memory or aggregate-process reservation.
A worktree alone remains no security boundary.

The guardian samples process ancestry every 100 ms to notice setsid escapes; this cannot prove
that an adversary never created a short-lived, unobserved escape. Known escapes
or inconclusive EPERM observations stay unknown and cannot release reservations.
On supervisor SIGKILL its pipe closes; the guardian kills the group and syncs a
private receipt. `Open` retains that evidence, fences old launch authority, and
replays old-boot evidence for daemon reconciliation. Suspend uses the earlier of
monotonic and conservative wall cutoffs, but suspend/reboot latency has not been
qualified for unattended service.

Native events are spooled before acting on them; full spools retain acknowledged
content and add a terminal `spool_full` status before stopping. Approval-required,
crash/nonzero exit and grant-path violations produce failed manifests, not local
acceptance. Custody commit still is not finalization: the candidate workspace is
deleted only after a positive finalization reply with release. Interrupted upload
phases resume from the immutable custody plan and synced blob snapshots without
cancelling a finished attempt. Terminal transport refusals retain bytes and a
separate refused outcome, never a false acknowledgment. Legacy incomplete plans
remain preserved for reconciliation.

A daemon restart can invalidate the execution session (`session_stale`). The
runner stops locally and exits fail-closed; the guardian receipt remains durable.
The operator must restart serve for a new authenticated session and evidence
replay/reconciliation, then import the runner's new boot facts before new work.
Execution, local acceptance, publication and merge are separate; this runner has
no publication credentials or merge route.
