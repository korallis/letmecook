# Local workflow store and identity scaffold (#11/#13, provisional)

Runnable Go/SQLite metadata foundation on the decided-but-unlocked narrow Go core.
This store is an **operator-authorized reversible provisional deliverable**,
reconciling the merged #94 store toward the decided (not-yet-locked) foundation.
**Full #11 acceptance reconciliation is deliberately deferred until #9 is accepted
and locked and #10 O1–O8 close**, including execution/runtime qualification.
This extension adds persistent empty installation/reopen, fail-closed restart
handling and a pinned mTLS owner/runner identity boundary. #13 closure is also
deferred pending accepted/locked #9/#10; identity enrollment grants no execution.
The provisional [#12 grant service](../../internal/authority/README.md)
adds typed in-process authority checks; no grant HTTP mutation API is exposed.
The provisional [#14 repository profile](../../docs/operations/repositories.md)
adds owner-gated in-process registration and separate trusted checkout, not a
repository HTTP endpoint or execution authority. The provisional
[#15 admission service](../../internal/scheduler/README.md) adds in-process atomic
attempt/reservation/outbox metadata, not lease issuance or process launch.
This supplies no formal #11
acceptance, supported worker runtime, execution readiness or product readiness.
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
Before owner bootstrap, this loopback-only mode has no mutation endpoint. After
bootstrap, plaintext startup refuses; use the HTTPS identity configuration below.
No task creation, execution, grant or import HTTP endpoint exists. Atomic dispatch
and reconciliation are trusted in-process entry points with synthetic v2 tests;
no network assignment sender, lease or process launcher exists.
Grant methods are trusted in-process entry points, not authenticated APIs.

Installation configuration is **flags only**:

| Flag | Required meaning |
| --- | --- |
| `--state-dir` | Absolute clean path on local disk. Creates final directory only, under an existing parent; private owned mode 0700. Holds `state.db`, WAL/SHM and directory-inode ownership lock. |
| `--artifacts-dir` | Same path, ownership and creation requirements as `--state-dir`, validated on each startup. May change on reopen or share/nest with other configured directories; no artifact lock or persisted path binding. Reserved location only: no uploads, artifact writes, custody or acknowledgements. |
| `--listen` | Explicit IP:port, 0..65535; plaintext requires exact `127.0.0.1`. HTTPS permits an operator-selected IP (including explicit wildcard); no DNS discovery or proxy exception. 0 requests an ephemeral port. |
| `--fixture` | Exclusive alternative to install flags: creates/seeds fresh disposable public #94 data and serves the existing shell. Graceful exit removes only this owned fixture directory. |

Choose any operator-controlled host meeting the local storage requirements. No
configuration discovery from HOME, environment, files, repository plugins, accounts,
proxy/router variables or a private network. Unknown/positional flags reject.
The identity API accepts connections only; it never contacts, discovers or enrolls
network hosts on its own. Runner runtime and 9Router clients remain absent; no
provider credentials are stored.

**Plaintext loopback mode is not authentication.** Before owner bootstrap, local
programs can read metadata. Use disposable synthetic data only in that mode; never
proxy it. Owner bootstrap permanently requires authenticated HTTPS for this store.
Browser sessions remain absent. A private network alone provides no authorization.

For historical fixture UI/tests use `.local/gafferd --fixture`. Abrupt fixture death
may leave `/tmp/gaffer-fixture-*`; no persistent startup imports it. Never bulk-delete
other processes' stores. Persistent and fixture databases have different SQLite
application identities, checked before migration; no version relabelling/import.

## Owner bootstrap and runner enrollment

[Identity setup, API and recovery](IDENTITY.md) owns the provisional #13 boundary.
Follow it for local owner commands, HTTPS configuration and runner enrollment.

## Read contract for #95

`schemas/readapi/types.go` / `types.ts` retain `read-provisional-v1`, extending its
closed mode/schema combinations: `fixture-only` / schema 1, `store-only` / schema 2,
3, 4, 5, 6, 7, 8 or 9 (current). New persistent opens migrate to 9; clients retain historical
schema 2/3/4/5/6/7/8 reads. Schema 8 adds provisional durable stop metadata and schema 9 adds
provisional verification/local-review metadata only; neither response exposes grants,
repository profiles or dispatch input records.
Older strict clients refuse unsupported mode/schema combinations rather than
misreading persistent state as fixture data.
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

Read boundary: URI 512 bytes, headers 4 KiB plus Go parser overhead, responses
1 MiB, 16 active DB requests, 2-second query/header and 3-second read/write budgets.
Reject unknown/duplicate query keys, noncanonical bounds/IDs, bodies, encoded paths,
non-GET methods, foreign Host/Origin, forwarding headers and cross-site fetch
metadata. Plaintext Host equals actual listener; optional Origin equals `http://`
plus Host. HTTPS Host equals configured endpoint, rejects every Origin, Cookie and
Authorization header, and requires a current owner credential for store reads.
No CORS, preflight, browser session or cross-origin exception.

## Store correctness and limits

- Owner: `internal/store/`, stdlib `database/sql`, pinned CGO-free
  `modernc.org/sqlite v1.59.0` / SQLite 3.53.4. Driver/notices:
  [`DEPENDENCIES.md`](../../internal/store/DEPENDENCIES.md). No new dependency.
- Private owned state directory-inode nonblocking `flock` held through DB close.
  Paths reject symlink roots and nonprivate directories
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
- One schema owner. Empty persistent schema migrates transactionally from base
  metadata/tasks/attempts/events through schema 2 (append-only event triggers; legacy
  artifact-location column retained but unused) to schema 3 (principals,
  credential-pin tombstones and expiring enrollment hashes), then schema 4 (immutable
  grants, CAS heads and durable invalidations), then schema 5 (immutable repository
  identities and approved input profiles), then schema 6 (atomic dispatch input,
  reservation/outbox, eligibility history, sticky stops and ack/release receipts;
  retained attempts with a unique current-attempt index). Persistent application ID
  `0x47414646` distinguishes it from #94.
  Unknown schemas, unrecognized DBs and fixture imports fail closed. Migration,
  fresh boot and restart recovery share one commit. Generation survives ordinary
  restart; restore/new-generation/retired namespaces remain #23, not file copying.
- Attempt identity/event writes share one transaction. Task/attempt/assignment/message IDs,
  task epoch and event revision uniqueness plus composite foreign keys survive
  replay. The [admission service](../../internal/scheduler/README.md) owns increasing
  epochs, sequential reservations, immutable route decisions and outbox replay;
  the [grant service](../../internal/authority/README.md) owns #12 authority revisions.
  Snapshot selects each task's highest retained epoch. Attempt CAS + task projection
  + event insert commit together; append-only event triggers prevent update/delete.
- Persistent attempt writes validate v2 and current generation before replay. The
  [admission lifecycle](../../internal/scheduler/README.md#charges-stop-and-release)
  owns stop and proof-gated terminal transitions. Starting/running/result/success
  transitions still await their evidence owners.
  Restart changes active attempts to unknown/reconciling and appends matching
  events atomically with fresh boot.
  Recovery leaves unknown/terminal history unchanged and never resumes execution.
  Exhaustion/errors abort startup rather than wrapping revision or fabricating safe state.
- Artifact directory is explicit configuration, **not artifact durability**. No blob,
  manifest, result receipt, lease, runner runtime, inference,
  acceptance, publication or merge exists. Repository validation is a separate
  typed in-process operation; daemon startup never contacts repositories. Store
  event durability proves none of #10's physical custody, fencing, stop or
  live-evidence obligations.

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
concurrent state ownership, artifact reconfiguration/sharing and graceful daemon reopen. Test SQL triggers
and subprocess control exist only in `_test.go`, not the daemon.

Real `SQLITE_FULL` (13) uses `max_page_count` and a blob trigger after authoritative
row updates, requiring rollback and intact prior state. This is SQLite capacity
failure, not a filled host volume, failed physical sync, power cut or disk loss.
No mounted NFS/SMB lab or backup/restore qualification is claimed. Tests terminate
only their own `exec.Cmd` processes. TypeScript tests build/run real fixture and
persistent daemons and validate strict read responses; Go tests retain hostile
HTTP/config/plugin probes with zero outbound trap hits and no execution sentinel.
