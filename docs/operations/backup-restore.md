# Backup and paused restore

This is the provisional schema-10 backup/restore slice for issue #23. It copies
acknowledged daemon state and immutable custody evidence, not live execution. It
does not qualify an execution runtime, stop surviving runners, publish code or
satisfy the complete M1 system acceptance gate. The integration contract is
[decision 0002, section 8](../decisions/0002-m1-end-to-end-integration.md#8-reconcile-20-and-backuprestore-23).

## Consistency and completion boundary

1. Pause the daemon and reconcile **every non-terminal attempt**, including
   `unknown`. Pause alone does not stop work. Backup refuses `active_execution`
   while any attempt is `assigned`, `starting`, `running`, `result_pending`,
   `stopping` or `unknown`, and refuses `not_paused` otherwise.
2. `store.PinArtifacts` holds the store's writer/collection mutex until backup
   publication. Collection, resume and other metadata mutations cannot cross this
   boundary. The only permitted store call while holding the pin is
   `SnapshotDatabase`; release is idempotent. This intentionally favors correctness
   over read/write availability during the copy. Use the jobs worker for large
   backups, and expect other store calls to wait. This whole-copy mutex is the
   accepted M1 trade-off. A follow-up should introduce an explicit collection-pin
   flag, hold the mutex only around `VACUUM INTO`, and preserve the pause/attempt
   fence while releasing the mutex for file copying.
3. `SnapshotDatabase` uses modernc SQLite's **`VACUUM INTO`**, under the same mutex,
   to capture committed WAL pages into a new private `state.db`. It explicitly
   fsyncs that file and its parent. It never copies just the live main file.
4. The snapshot itself supplies the artifact inventory. Every retained candidate
   manifest and its referenced blobs, including quarantined evidence and empty
   files, is copied and hashed. Known attempt stream journals are copied from
   `streams/<attempt_id>.sink/journal.jsonl`; unrelated files, upload staging,
   runner workspaces and recovery directories are not copied. A present sink
   directory with a missing journal is an error, not an empty stream.
5. The copied database omits **gateway configuration values and credential
   responses**. Its `gateway_profiles` rows are removed. An explicit sanitization
   allowlist also rewrites `owner_commands.status/response` for enrollment routes
   or any response JSON containing a non-empty string under a `token` key at any
   depth. Receipt identity and request hashes remain; status **410** and a
   `credential_response_omitted` tombstone replace the secret response. This
   recognizes path-based and `identity.enrollments` route names, but does not
   depend on future route naming. Immutability triggers are restored exactly and
   a second `VACUUM` removes freed-page secret bytes. **The source is unchanged.**
   Reconfigure the gateway and create new enrollment invitations after restore.
6. All files are copied with exclusive creation, expected SHA-256 and length
   checks, file fsyncs and directory fsyncs. Before completion they are rehashed.
   `backup-manifest.json` is atomically renamed into place **last**, and its parent
   is fsynced before success. An interrupted/failed directory is not reusable.

`backup-manifest.json` has version `gaffer-backup-v1`, a random `backup_id`, source
`generation` and `daemon_boot`, `schema_version: 10`, `created_ms`, the database's
SHA-256/byte length, an inventory of kind/path/SHA-256/byte length, and every stored
attempt's state. `created_ms` records snapshot creation, not the newest possible
runner work. Inventory paths are closed to the exact blob, manifest and sink
layouts; symlink inputs, path escapes, unknown fields, duplicate keys, nulls,
missing fields and unsupported schema versions are rejected.

`backup.Verify(dir)` checks the marker, every hash/length, `PRAGMA integrity_check`
and `PRAGMA foreign_key_check`, database/manifest identity and attempt-state
agreement, and completeness against both manifest-body references and their
SQLite links. Retained runtime stream watermarks make a sink mandatory even if
its entire directory/root is missing. Copied journals receive bounded read-only
replay of the `runnerjournal` envelope and hash chain plus `runstream.Validate`
record checks, binding the attempt identity, acknowledged watermark and
generation lineage across repeated restores. Verification never opens a live
writable sink; read-only backup media can be verified and restored without
mutation. Extra files (including SQLite sidecars) are refused. Corrupt or missing
content produces an error naming the failed content where available; a
database-only directory is never reported as a complete backup. Hashes detect
accidental corruption, not an attacker who can rewrite both the backup and its
manifest. Store backups privately.

**Residual stream-evidence limit:** a terminal attempt can have acknowledged sink
records but no retained exit observation or other positive stream watermark. If
that entire sink is then lost, the current database cannot distinguish it from
an attempt that emitted no output; a missing sink remains optional in this case.
A durable per-sink acknowledgement watermark independent of exit observations is
future integration work. Existing positive watermarks and present-but-damaged
sink directories still fail closed.

## Owner API and composition

The daemon's `--backup-dir` is an explicit private destination root. Compose
`backup.NewService(store, artifactsDir, stateDir+"/streams", backupDir)` into
`httpapi.Deps.Backup`. The service accepts only a direct child name, or its absolute
path under that root; it refuses existing non-empty destinations and symlink
roots. An unconfigured dependency returns `503 backup_unavailable`.

The HTTP destination must be a single name of at most 128 bytes: no separators,
`.`/`..`, NUL or line breaks. Absolute paths are rejected at submission even
though the internal service can resolve equivalent direct-child absolute paths.
The handler briefly acquires/releases the backup pin before queuing, refusing
`not_paused` or `active_execution` with 409 without submitting a job. The worker
rechecks this boundary when it executes; preflight is not a reservation.

`POST /api/v1/backup` uses owner mTLS and closed JSON:

```json
{"version":"workflow-provisional-v1","message_id":"7c2c10cc-f7e6-4e64-a13b-57b889b21f61","destination":"nightly-01"}
```

The route requires `Deps.Jobs` and submits a `backup` job with the stable message
ID and destination as `subject_id`, returning `202 {"job_id": "…"}`. The worker
must register a handler calling the configured service's `Create`; job submission
is not a completed backup. The worker owns durable message-ID replay and changed
body conflicts. Without a worker, the route refuses **503 `store_unavailable`**
with detail **`jobs_unavailable`**. There is no success-capable synchronous
fallback: long copying must not bypass durable owner intent handling.

The `backup create|verify` CLI family, pause/resume commands, jobs implementation
and daemon composition are S4/S0 integration work, not implementations in this
slice. The offline `restore` command below is implemented here. Execution-route
`paused` refusal is S1, and the confirmed-source-fenced resume gate is S4. Until
those pieces are composed and tested, do not treat this lane as a running backup
service or a complete safe-resume workflow.

## Offline restore drill

Keep the original backup intact. Restore into **distinct, empty, private** targets
on the local filesystem; parents must exist. Neither target may overlap the
backup or the other target. Existing data is refused, never overwritten.

```sh
gaffer --json restore \
  --backup /absolute/backups/nightly-01 \
  --state-dir /absolute/recovery/state \
  --artifacts-dir /absolute/recovery/artifacts
```

Offline `restore` **ignores the global `--timeout` request deadline**, including
its 30-second default and `GAFFER_TIMEOUT`; a valid `--timeout 1ms` does not bound
filesystem recovery. SIGINT/SIGTERM still cancel the operation. An interruption
may leave partial targets that must not be resumed in place. Failure JSON keeps
`code: restore_refused` and includes a bounded error class in `detail`, such as
`digest_mismatch`, `target_not_empty`, `invalid_destination`, `no_space` or
`interrupted`, without echoing paths or arbitrary database/payload text.

No endpoint, network connection, certificate or key is used by `restore`. It first
verifies the entire backup, then holds the same directory-inode ownership lock
as `store.Open` on both final targets for the entire restore. Emptiness is checked
after acquiring these locks, excluding a racing daemon or second restore. It then
copies into the new targets. State remains under
`.restore-pending` until `store.OpenRestored` commits, in **one FULL-synchronous
SQLite transaction**:

- a fresh execution generation and daemon boot;
- `daemon_state.paused=1`, `reason=restored`;
- an immutable `restore_history` entry identifying the source/new generations,
  backup ID, backup creation time, restore time and exact manifest SHA-256;
- defensive recovery of active attempts to `unknown` and tasks to `reconciling`.

The last SQLite connection closes/checkpoints before the main database is
published at the target root. A non-empty remaining WAL prevents publication.
Stream files precede `state.db`; that database is published last. Success removes
`.restore-incomplete`. A failed restore leaves inspectable partial data and may
leave this marker. **Do not start a daemon on a failed restore.** Use new empty
targets after diagnosing the failure; there is no in-place continuation or cleanup
command. The source backup and original files are never removed on an error.

Successful JSON output contains `paused: true` and the restore-history entry.
Inspect its old/new generation and manifest digest before starting the replacement
with its explicit state/artifact paths. Normal subsequent store startup preserves
that paused state and the newly allocated generation.

### Reconcile before resume

The integrated contract requires `Dispatch`, `Delivery`, `IssueLease`,
`ProposeTransition` and `FinalizeAttempt` to refuse `paused`. Before running
`gaffer daemon resume --confirm-source-fenced`, the operator must:

1. Stop/fence the **source daemon**, and confirm it cannot dispatch more work.
2. Stop surviving work and restart **every runner** so it gets a new boot.
3. Re-establish the separately held credentials/gateway configuration and import
   fresh runner eligibility facts. Review enrollment/identity trust too.
4. Read `gaffer reconcile status`; resolve every unknown execution/remote-work
   outcome and retained reservation, and verify the intended repository base.
5. Explicitly acknowledge source fencing with `--confirm-source-fenced`. An
   ordinary resume without it must be refused by the composed S4 workflow.

A new generation is a message fence, **not a process kill**. Old-generation
assignment acknowledgements and lease/result control messages are refused
`stale_generation`; newly arriving old-generation custody is retained in quarantine,
never a current head. `TestRestoreIssueLeaseFencesOldGeneration` additionally
invokes the actual lease issuance entry point: a retained pre-restore session
must fail `session_stale`, then an old-generation request over a fresh session
must fail `stale_generation` with no lease row created. Both sessions are created
through `RunnerSession` against validated persisted eligibility facts, and this
regression runs against the real execution-channel implementation without a skip.
Historical acknowledged-receipt replay is quarantined by the integrated store's
retained-receipt generation check, covered by
`TestRestoreQuarantinesAlreadyAcknowledgedReceiptReplay`. Normal store startup
refuses `.restore-incomplete` before opening or initializing its database, covered
by `TestDaemonRefusesIncompleteRestoreMarker`. Historical events retain their
original generation in the immutable log; the bounded snapshot projects only
current-generation events, so reads succeed after restore without presenting
retired events as current. This is covered by the active
`TestRestoredSnapshotPreservesHistoricalEventGenerations` regression. No successful
response here grants execution, review or publication.

## Recovery point, retention and limits

- Recovery is limited to the committed database point and copied daemon custody
  and sink bytes. Unsent artifacts, output, inference receipts or work still only
  on a destroyed runner are **not recoverable** from this backup. A surviving
  runner's old history is reconciliation evidence, never authority to rerun.
- **Base/context commits are not bundled by this slice.** Registered remote URLs
  and pinned commit identities remain in the database. Independently retain those
  Git objects/remotes; recreated verification needs them. A locally available
  checkout is not an included context bundle. This is a documented shortfall from
  spec 9.4/issue #23's broader context-commit criterion.
- Private keys, raw enrollment tokens, credential files, gateway config files and
  router internals are excluded; identity fingerprints/token hashes and workflow
  evidence remain. Provider credentials and 9Router/other model-gateway state
  require a **separate backup and recovery procedure** under their owner's policy.
  Native output and candidate content must already be redacted upstream: this is
  an allowlisted backup, not a general-purpose secret scrubber of arbitrary text.
  Legacy identity routes do not yet populate `owner_commands`, but future receipt
  wrappers could retain raw invite tokens there. The route/content-based receipt
  sanitization above closes that path and `Verify` rejects unsanitized matches;
  other future secret-bearing columns or differently named credential keys must
  be explicitly added to the sanitization contract.
- There is no automatic backup schedule, rotation or backup GC. Retain at least
  the last independently verified backup and the Git/context/credential recovery
  material it needs. Never delete the only durable copy to make room for a new
  one. Prefer a separate failure domain and test its filesystem durability.
- Interrupted or ENOSPC copies leave no acknowledged completed backup. Budget
  space for the SQLite snapshot, its transient sanitizing VACUUM, all referenced
  content and the previous good backup. Delete incomplete destinations only after
  positively identifying them; never infer completion from `state.db` alone.
- This lane has no disaster-recovery RPO/RTO guarantee. Record actual schedule,
  source snapshot `created_ms`, restore wall time and validation outcome in each
  drill. The scheduler's 60-second restart target is not a full-restore target.

## Reproduction checks

```sh
go test -race -count=1 ./internal/backup
```

The tests use real local SQLite and journal files, exercise WAL snapshot integrity
and foreign keys, compare byte-identical copies, corrupt/remove inventory content,
reject incomplete and non-empty targets, verify collection pinning, restore a new
paused generation/history, replay old runner messages, and invoke the built CLI.
A child process is blocked **after a real partial copy write and fsync**, then
killed with SIGKILL; the leftover directory has no manifest and is refused. An
instance-local writer injects ENOSPC during real backup/restore copying and proves
the original recovery data and prior verified backup survive. This is separate
from host/disk-loss testing; no privileged tmpfs is claimed. Owner-route tests use
real mutual TLS, not a mocked identity header. The built restore CLI succeeds
with `--timeout 1ms` and exposes distinct safe failure classes. Receipt tests
cover nested/escaped token keys, future route names, unchanged source/ordinary
receipts, immutable triggers and rejection despite a recomputed database hash.
