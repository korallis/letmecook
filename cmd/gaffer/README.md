# Gaffer owner CLI

Build with `go build ./cmd/gaffer`. The CLI uses the Go standard library and typed,
bounded requests. These are **provisional owner workflows**, not evidence of a
supported execution runtime or acceptance of #9/#10. Execution, local acceptance,
publication and merge are separate authorities. There is no publish/merge command.

## Connection and output

Globals work before or after the subcommand: `--endpoint`, `--cert`, `--key`,
`--daemon-fingerprint`, `--json`, `--message-id`, `--timeout` (30 seconds by default).
Environment defaults are `GAFFER_ENDPOINT`, `GAFFER_CERT`, `GAFFER_KEY`,
`GAFFER_DAEMON_FINGERPRINT`, `GAFFER_JSON`, `GAFFER_MESSAGE_ID`, `GAFFER_TIMEOUT`.
Explicit flags override environment defaults. Keep credentials outside repositories
and worker sandboxes. The daemon pin is explicit trust, not trust-on-first-use.
TLS 1.3 verifies the exact leaf, validity, serverAuth and endpoint hostname; no
redirects, environment proxies or unverified-TLS fallback are allowed.

```sh
gaffer identity keygen --cert owner.crt --key owner.key
gaffer identity keygen --server --host 127.0.0.1 --cert daemon.crt --key daemon.key
gaffer identity self --endpoint https://127.0.0.1:7443 \
  --cert owner.crt --key owner.key --daemon-fingerprint "$daemon_pin" --json
```

Keygen creates an Ed25519 PKCS#8 key (0600) and self-signed leaf (0644), never
replacing either file. Default validity is 30 days (`--days 1..365`). Client leaves
have CA:false, digitalSignature and clientAuth. `--server` selects serverAuth and
requires `--host` SANs. [Bootstrap/recovery](../gafferd/IDENTITY.md) is offline.

Success is one stdout envelope; errors go only to stderr:

```json
{"version":"gaffer-cli-v1","command":"task.create","message_id":"00000000-0000-4000-8000-000000000001","result":{}}
{"version":"gaffer-cli-v1","error":{"status":409,"code":"identity_conflict","detail":"message_id"}}
```

| Exit | Meaning |
| --- | --- |
| 0 | Success (a queued job or stop intent is not completed execution) |
| 1 | Refusal/conflict |
| 2 | Usage or malformed request |
| 3 | Unavailable/timeout |
| 4 | Reconciliation required; no second valid attempt is implied |

Local configuration/transport failures use status 0; only received HTTP refusals
carry an HTTP status. An omitted UUID is generated once per invocation. Retrying a mutation requires the
**same `--message-id` and body**. Receipt replay carries `Idempotent-Replay: true`;
reusing a retained UUID for another request is `409 identity_conflict`. Domain work
commits before its response receipt. Never use a fresh UUID to bypass a refusal.

## Commands

| Family | Commands and important inputs |
| --- | --- |
| Identity | `invite --fingerprint SHA256`, `enroll --token TOKEN`, `update --id UUID --revision N --action enable\|disable\|revoke\|rotate [--fingerprint SHA256]` |
| Repository | `register\|validate --profile FILE --expected-revision N [--wait]`; `show ID` |
| Runner | `facts import --file FILE --expected-revision N`; `facts show [ELIGIBILITY]` |
| Task | `create`, `approve UUID --eligibility ID`, `dispatch UUID --grant-id UUID --grant-revision N --attempt-ms N`, `inspect\|watch\|stop\|retry UUID`, `list [--after UUID --limit N]` |
| Attempt | `inspect\|cancel\|stream\|artifacts UUID`; stream takes `--after N --limit N` |
| Verification | `run UUID [--expected-selection UUID --wait]`, `show REPORT_UUID` |
| Review | `accept\|reject UUID [--verification-id UUID --coverage-file FILE --notes TEXT]`; `show UUID` |
| Daemon | `status`, `pause\|resume [--reason TEXT]`, `resume --confirm-source-fenced`, `stop-all` |
| Reconcile | `status`, `release ATTEMPT_UUID --proof FILE` |
| Backup | `create --destination DIR`, `verify --backup DIR` (S6 service); offline `restore` is S6-owned |

Repository work and verification are durable jobs. `--wait` polls; a timeout does
not cancel the job. `GET /api/v1/jobs/{id}` exposes queued/running/succeeded/failed,
result JSON and an error. Interrupted running jobs fail `daemon_restart`; they are
not silently rerun. Task watches poll every 500 ms and surface unknown attempts as
reconciliation required. Reads do not call a model gateway.

`task stop`, `attempt cancel` and `daemon stop-all` return **recorded stop intent**,
not a claim that a process or remote inference has terminated. Inspect the attempt,
`/api/v1/stops/{id}?attempt_id=...`, and reconciliation evidence. A stopped or
unresolved attempt cannot be bypassed with a retry. After `attempt cancel`, retry
is admitted only once `reconcile status` shows the attempt released and its cancel
latch cleared. Task stop is a sticky task pause; attempt cancel and global stop
leave assigned-to-stopping transitions to the runner/reconcile path.
Pause is an admission gate, not
process termination. Restored stores require explicit source-fenced confirmation.

## Exact task input and owner approval

```sh
gaffer task create --repo fixture --base "$base_commit" --brief-file brief.txt \
  --criterion c1='The trusted checks pass' --path file \
  --harness fake --settings-file fake.json --message-id "$task_id" --json
gaffer task approve "$task_id" --eligibility fixture-pair \
  --allow-development-isolation --message-id "$grant_id" --json
gaffer task dispatch "$task_id" --grant-id "$grant_id" --grant-revision 1 \
  --attempt-ms 60000 --message-id "$dispatch_id" --json
```

Repeat `--criterion id=text`, `--path PATH`, and `--operation read|verify|write`.
The CLI explicitly defaults operations to read/verify/write; the API invents no
scope. Paths are exact, portable, sorted paths, never globs or `.git` access.
Tasks pin the registered repository's base. Protected paths are refused.

`harness` is `fake` or `opencode`. `settings` is the direct harness-specific JSON
object, **not a `fake_spec` wrapper**. Fake example:

```json
{"attempts":[{"mode":"edit","edits":[{"path":"file","content":"changed\n"}]}]}
```

The runner selects `attempts[min(epoch-1,last)]`. Edits use exactly one of `content`,
`content_base64`, or `delete:true`; timing/output controls are bounded `delay_ms` and
`stream_bytes`. The default fake script is one noop attempt. OpenCode uses
`{"model":"gateway-model-id"}` with optional `variant`. Unknown/duplicate/null
settings are refused. Settings object keys are recursively sorted, preserving
optional-key presence; criteria/paths/operations are sorted before persistence.
`store.BriefDigest` is SHA-256 of ordered canonical JSON
`{brief,criteria,paths,operations,harness,settings}`. The grant's brief revision/hash
binds the exact persisted input exposed to the runner, not just prose.

Approval fetches the persisted server proposal and submits its `proposal_digest`.
Human mode prints it to stderr; JSON mode keeps the stdout envelope clean. A stale
proposal is refused. Replacement approvals use `--expected-grant-id`; optional
`--budgets-file` and `--expires-ms` cannot enlarge runner-local permission.

`macos-sandbox-exec-dev` is **qualification: development, supported: false**.
Both daemon `--allow-development-profile macos-sandbox-exec-dev` and approval
`--allow-development-isolation` are required. No flag qualifies it for unattended
production execution. Verification defaults to `unqualified` refusal evidence;
only explicit daemon profile selection enables the measured development verifier.

`review accept --override-with-reason TEXT` records a requested reason in the
submitted limitations. It **does not bypass** the service's verified-evidence gate:
an unverified report still returns `verification_required`. This deliberately
preserves `review.ValidateDecision` rather than treating a CLI flag as authority.
A new report or candidate selection invalidates earlier acceptance. Rejection is
an explicit local decision, not publication or merge.

## Scripted flow

```sh
gaffer --timeout 10m flow run --repo fixture --base "$base_commit" \
  --brief-file brief.txt --criterion c1='The trusted checks pass' --path file \
  --runner "$runner_id" --harness fake --fake-spec fake.json \
  --flow-id disposable-example --allow-development-isolation \
  --transcript ./flow.ndjson
```

`flow run` drives create → approve → dispatch → watch → verify → accept. OpenCode
uses `--harness opencode --model MODEL` or `--settings-file`. It requires exactly
one matching imported eligibility tuple and never invents a route. Stable UUIDs
are derived separately for create/approve/dispatch/verify/accept from `--flow-id`.
The same flow ID and exact input replay; changing intent requires a new flow ID.
Each NDJSON record has `step`, `request`, `response`, `elapsed_ms`. A file transcript
must be a private 0600 regular file; records append and fsync. It contains task and
review data, not certificates, keys, invitation tokens or gateway credentials.

`--until assigned` stops after admission and exists for seams-only smoke testing;
it is **not** execution/verification/acceptance evidence. The test
`TestFlowRealHTTPSDaemonAssignedAndBlockedRoute` builds and launches real `gafferd`
over pinned mTLS, reaches assigned, replays without a second attempt, and refuses
an unconfigured route. Full accepted flow requires integrated S1/S2 execution and
S5 reconciliation; this branch alone cannot supply those implementations.

The daemon must compose `httpapi.WorkflowJobHandlers` and `jobs.NewWorker`, and
integrate the owner transaction/decision and paused-dispatch hooks. The tagged
`ownercommit` store tests intentionally fail until those out-of-lane patches land.
See [owner workflow operations](../../docs/operations/owner-workflow.md).
