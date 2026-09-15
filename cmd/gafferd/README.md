# Local workflow store scaffold (#11, provisional)

Runnable Go/SQLite metadata foundation on the decided-but-unlocked narrow Go core.
This store is an **operator-authorized reversible provisional deliverable**,
reconciling the merged #94 store toward the decided (not-yet-locked) foundation.
**Full #11 acceptance reconciliation is deliberately deferred until #9 is accepted
and locked and #10 O1–O8 close**, including execution/runtime qualification.
This extension adds persistent empty installation/reopen and fail-closed restart
handling. It supplies no formal #11 acceptance, execution authority, supported worker
runtime, execution readiness or product readiness.
The [decision](../../docs/decisions/0001-execution-foundation.md) and
[execution contract](../../docs/contracts/execution.md) retain their acceptance gates.

## Bounded local startup

Toolchain: Go 1.26.5; optional embedded fixture UI build uses Node 24+ and pinned
React/TypeScript/StyleX dependencies. Runtime: single local process on Linux
ext4/XFS/Btrfs or macOS APFS. No Docker, router, account, repository, maintainer
hostname, private network or Node server required for this daemon. No supported
**execution** runtime follows from these storage platforms.

Build the existing [fixture shell](../../web/README.md#build-and-serve) before Go
if it is needed; the persistent read API deliberately exposes no web UI.

```sh
npm --prefix web ci --ignore-scripts
npm --prefix web run build
mkdir -p .local
CGO_ENABLED=0 go build -trimpath -buildvcs=false -o .local/gafferd ./cmd/gafferd

# Disposable example install; retain this path to reopen the same state.
install_dir=$(mktemp -d /tmp/gaffer-install.XXXXXX)
.local/gafferd --state-dir "$install_dir/state" \
  --artifacts-dir "$install_dir/artifacts" --listen 127.0.0.1:0
```

Stop with Ctrl-C/SIGTERM. Re-run the same command to reopen. Shutdown never deletes
persistent state. Startup prints `store-only http://127.0.0.1:<port>/api/v1/status`
only after ownership, migration, boot/recovery transaction and directory sync.
`GET` that URL or replace `status` with `snapshot`. Fresh installation is empty.
No task creation, execution, grant, enrollment, mutation or import endpoint exists.
Store write primitives remain private; tests exercise them with synthetic v2 inputs.

Installation configuration is **flags only**:

| Flag | Required meaning |
| --- | --- |
| `--state-dir` | Absolute clean path on local disk. Creates final directory only, under an existing parent; private owned mode 0700. Holds `state.db`, WAL/SHM and directory-inode ownership lock. |
| `--artifacts-dir` | Separate, non-nested private local directory; also exclusively locked. Canonical path persists with installation and must match on reopen. Reserved location only: no uploads, artifact writes, custody or acknowledgements. Relocation requires future migration, not silently changing a flag. |
| `--listen` | Exact `127.0.0.1:<port>`, 0..65535; 0 requests an ephemeral port. All three install flags required. No DNS lookup, wildcard, remote binding or proxy exception. |
| `--fixture` | Exclusive alternative to install flags: creates/seeds fresh disposable public #94 data and serves the existing shell. Graceful exit removes only this owned fixture directory. |

Choose any operator-controlled host meeting the local storage requirements. No
configuration discovery from HOME, environment, files, repository plugins, accounts,
proxy/router variables or a private network. Unknown/positional flags reject.
Runner and 9Router infrastructure configuration belongs to their future owners;
this process has neither client and stores no provider credentials.

**The listener is not authentication.** This provisional daemon has no sessions or
owner identity. Local programs can read its metadata. Use disposable synthetic data
only; do not expose it through a proxy or use it for private product data. Remote
installation access remains blocked until authenticated HTTPS/session work lands.
A private network alone will not provide authorization.

For historical fixture UI/tests use `.local/gafferd --fixture`. Abrupt fixture death
may leave `/tmp/gaffer-fixture-*`; no persistent startup imports it. Never bulk-delete
other processes' stores. Persistent and fixture databases have different SQLite
application identities, checked before migration; no version relabelling/import.

## Read contract for #95

`schemas/readapi/types.go` / `types.ts` retain `read-provisional-v1`, extending its
closed mode/schema combinations: `fixture-only` / schema 1, `store-only` / schema 2.
Old strict clients refuse the new mode rather than misreading it as fixture data.
Go validates projections before JSON; TypeScript `decode(bytes, 'status' | 'snapshot')`
validates bounded input at runtime. The fixture shell remains fixture-only and is
not served in persistent mode; no UI work or product exposure is introduced.

| Request | Response |
| --- | --- |
| `GET /api/v1/status` | Metadata, `task_count`, `event_count` |
| `GET /api/v1/snapshot` | Metadata, `tasks`, `events` |
| `GET /api/v1/snapshot?task_id=<UUIDv4>&limit=1` | Filtered snapshot |

Metadata includes version, mode, missing capabilities, generation, daemon boot and
schema version. Missing capabilities: execution, inference, artifact custody,
result ack, acceptance, publication, merge, state import and sessions. Error JSON
returns version/mode/missing capabilities plus a fixed error code, never SQL/path
or reflected input. Persistent observations are always desired stop, process unknown,
remote unknown and quarantined; no process death or remote completion is inferred.
Historical fixtures retain synthetic not_started observations and v1 events;
persistent events require `execution-provisional-v2`.

Tasks sorted by ID; events by ascending sequence. Each array independently caps at
`limit` (default/max 50, minimum 1). Unknown canonical task returns 404. Bounded
snapshot is not full history/export, pagination, streaming or result custody.

Existing boundary remains: URI 512 bytes, headers 4 KiB plus Go parser overhead,
responses 1 MiB, 16 active DB requests, 2-second query/header and 3-second read/write
budgets. Reject unknown/duplicate query keys, noncanonical bounds/IDs, bodies,
encoded paths, non-GET methods, foreign Host/Origin, forwarding headers and
cross-site fetch metadata. Host equals actual listener, optional Origin equals
`http://` plus Host. No permissive CORS, preflight or cross-origin exception.

## Store correctness and limits

- Owner: `internal/store/`, stdlib `database/sql`, pinned CGO-free
  `modernc.org/sqlite v1.59.0` / SQLite 3.53.4. Driver/notices:
  [`DEPENDENCIES.md`](../../internal/store/DEPENDENCIES.md). No new dependency.
- Private owned directory-inode nonblocking `flock` held through DB close;
  artifact directory also locked so two different state roots cannot share it.
  Paths reject symlink roots, nested state/artifact roots, nonprivate directories
  and nonregular, hard-linked, nonprivate or foreign-owned SQLite files. Parent
  aliases canonicalize (including macOS `/tmp`). Cooperating-daemon lock, not a
  security boundary against malicious same-UID processes or host administrators.
- Fail-closed filesystem allowlist: Linux ext4/XFS/Btrfs, macOS local APFS. NFS,
  SMB, FUSE, overlay, volatile and unknown types rejected. Honest kernel/device
  sync and supported mount configuration still required; allowlist is not hardware
  qualification. Shared-directory aliases cannot create another inode lock owner.
- Every SQL connection: WAL, foreign keys ON, synchronous FULL,
  fullfsync/checkpoint_fullfsync ON, trusted_schema OFF; effective values checked.
  New directory parents and DB directory entries synced before ready. Commit errors
  never become successful writes/acks. No hardware power-loss/fsync-failure claim.
- One schema owner. Empty persistent schema migrates transactionally through base
  metadata/tasks/attempts/events to schema 2 (artifact location, append-only event
  triggers). Persistent application ID `0x47414646` distinguishes it from #94.
  Unknown schemas, unrecognized DBs and fixture imports fail closed. Migration,
  fresh boot and restart recovery share one commit. Generation survives ordinary
  restart; restore/new-generation/retired namespaces remain #23, not file copying.
- Identity/event writes share one transaction. Task/attempt/assignment/message IDs,
  task epoch and event revision uniqueness plus composite foreign keys survive
  replay. One attempt per task remains deliberate: safe replacement, increasing
  epochs, grants, capacity/budget reservation and outbox delivery belong to #12/#15.
  No speculative tables or dispatch are introduced. Attempt CAS + task projection
  + event insert commit together; append-only event triggers prevent update/delete.
- Persistent writes validate v2 and current generation before replay. No transitions
  to starting/running/result/success/terminal states without future evidence owners;
  only unknown/stopping edges are available. Restart changes active attempts to
  unknown/reconciling and appends matching events atomically with fresh boot.
  Unknown/terminal history is not replayed or resumed. Exhaustion/errors abort
  startup rather than wrapping revision or fabricating safe state.
- Artifact directory is explicit and locked, **not artifact durability**. No blob,
  manifest, result receipt, lease, runner, inference, grants, repository access,
  acceptance, publication or merge exists. Store event durability proves none of
  #10's physical custody, fencing, stop or live-evidence obligations.

## Verification

```sh
npm --prefix experiments/inference-boundary ci --ignore-scripts
go mod verify
test -z "$(gofmt -l cmd internal schemas web tests/fixtures/protocol)"
experiments/inference-boundary/node_modules/.bin/tsc --noEmit --strict --target es2023 --module nodenext --allowImportingTsExtensions schemas/execution/protocol.ts schemas/readapi/types.ts tests/fixtures/protocol/check.ts tests/fixtures/readapi/check.ts --typeRoots experiments/inference-boundary/node_modules/@types
go vet ./...
go test -race -count=1 ./...
CGO_ENABLED=0 go test -count=1 ./...
node tests/fixtures/protocol/check.ts
node tests/fixtures/readapi/check.ts
npm --prefix docs ci
npm --prefix docs run build
npm --prefix docs run check
```

`.github/workflows/go-foundation.yml` runs Linux/macOS checks, embedded shell build
and two CGO-free `-trimpath -buildvcs=false` builds compared byte-for-byte. Existing
protocol, web and documentation workflows remain. Workflow presence is not proof
of exact-head CI success; delivery records actual results separately.

Tests execute real disposable SQLite files and owned daemon/test subprocesses:
fresh empty install, persistent migration/reopen, retained generation/new boot,
fixture import refusal, config/path rejection, append-only/unique/FK constraints,
unsupported `/dev` filesystem, duplicate assignment/CAS, transaction abort,
process SIGKILL inside transaction and after commit, restart uncertainty,
concurrent state/artifact ownership and graceful daemon reopen. Test SQL triggers
and subprocess control exist only in `_test.go`, not the daemon.

Real `SQLITE_FULL` (13) uses `max_page_count` and a blob trigger after authoritative
row updates, requiring rollback and intact prior state. This is SQLite capacity
failure, not a filled host volume, failed physical sync, power cut or disk loss.
No mounted NFS/SMB lab or backup/restore qualification is claimed. Tests terminate
only their own `exec.Cmd` processes. TypeScript tests build/run real fixture and
persistent daemons and validate strict read responses; Go tests retain hostile
HTTP/config/plugin probes with zero outbound trap hits and no execution sentinel.
