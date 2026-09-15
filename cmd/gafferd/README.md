# Provisional fixture-only daemon (#94)

Actual Go/SQLite application code, **not M1 acceptance**, a selected foundation,
a supported execution runtime, or authority to start original #11–#24. #9/#89/#10
and measured reconciliation remain open. Retain, port or discard this reversible
slice based on those outcomes, never sunk effort. Uses merged #93 protocol types
and embeds its unchanged public corpus from `tests/fixtures/protocol/data/`.

## Bounded local startup

Prerequisites: Go 1.26.5, local Linux ext4/XFS/Btrfs or macOS APFS disk, writable
`/tmp`. No Docker, router, account, repository or credentials needed. Only macOS
APFS has local execution evidence here; Linux CI must establish its own result.
Other OS/filesystems fail closed, including NFS, SMB, FUSE, overlay and volatile
filesystems. An allowlisted filesystem is not qualification of every hardware or
mount configuration: SQLite still relies on honest OS/device sync semantics.

```sh
mkdir -p .local
CGO_ENABLED=0 go build -trimpath -buildvcs=false -o .local/gafferd ./cmd/gafferd
.local/gafferd
```

Startup prints `fixture-only http://127.0.0.1:<ephemeral-port>/api/v1/status` only
after store creation, migration and fixture commits succeed. Fetch that exact URL
or replace `status` with `snapshot`. Stop with Ctrl-C or SIGTERM. Graceful shutdown
closes HTTP/SQLite and removes only its owned directory. Abrupt death can leave
`/tmp/gaffer-fixture-*`; no daemon startup can reopen/import it. Do not bulk-delete
stores belonging to other processes. There is no production cleanup/import command.

**The listener is not authentication.** Public fixtures only; local programs can
read them. No sessions, owner identity or secrets are invented. Do not expose this
port through a proxy or use it for real data. No arguments are accepted, including
`--bind`, `--config`, `--state`, `--fixture` and positional imports. Environment
variables (including HOME, TMPDIR, proxy, router and Gaffer options) select no
product behavior. The process never loads configuration, repository hooks/plugins,
external fixtures or account state. There is no execution/authority enable switch.

## Read contract for #95

`schemas/readapi/types.go` and `types.ts` own version `read-provisional-v1`.
Go validates store projections before writing JSON; TypeScript `decode(bytes,
'status' | 'snapshot')` validates untrusted bounded responses at runtime, not a
static type assertion. The existing execution types/messages remain
`execution-provisional-v1`. No extra schema library or UI dependency.

Only these routes exist:

| Request | Response |
| --- | --- |
| `GET /api/v1/status` | Metadata, `task_count`, `event_count` |
| `GET /api/v1/snapshot` | Metadata, `tasks`, `events` |
| `GET /api/v1/snapshot?task_id=<UUIDv4>&limit=1` | Filtered snapshot |

Metadata: `version`, `mode: "fixture-only"`, `missing_capabilities`, `generation`,
`daemon_boot`, `schema_version: 1`. Missing capabilities explicitly include
execution, inference, artifact custody, result ack, acceptance, publication, merge,
state import and sessions. Error JSON has version/mode/missing capabilities and
fixed `error` code; no reflected request, SQL error, local path or private payload.

`tasks[]` has `task_id`, protocol task `state`, and `attempt` with full protocol
identity, state, revision and observation. `events[]` has sequence, revision and
validated protocol message (assign/transition only). Sort tasks by ID, events by
ascending sequence. Each array independently caps at `limit` (default/max 50,
minimum 1). No pagination, streaming, time claims or completeness flag: this is a
bounded snapshot, not a full event export. Unknown canonical task returns 404.
Future consumers must not assume a bounded event list proves complete history.

URI capped at 512 bytes, headers at 4 KiB plus Go HTTP parser overhead, responses
at 1 MiB, 16 active database requests, 2-second query/header budget and 3-second
read/write budget. Unknown/duplicate query keys, noncanonical bounds/IDs, bodies,
encoded paths and unknown routes reject. Methods other than GET (including HEAD
and OPTIONS) return 405. Missing/foreign Host, foreign/null/duplicate Origin,
forwarding headers and cross-site fetch metadata reject. Allowed Host is exactly
`127.0.0.1:<actual-port>`; optional Origin is exactly `http://` plus that Host.
No permissive CORS, OPTIONS preflight support, static assets or cross-origin UI
server exception. #95 must preserve same-origin reads or explicitly reconcile its
serving boundary; this slice adds no such bypass.

## Store correctness and limits

- `internal/store/` uses stdlib `database/sql`, CGO-free modernc SQLite 3.53.4,
  pinned driver/dependency graph and reviewed permissive notices. See
  `internal/store/DEPENDENCIES.md`; full licence audit/owner grant stays #59.
- Fresh private directory mode 0700; SQLite DB created 0600. Directory-inode
  nonblocking OS `flock` held before opening DB until close/disposal, including
  against another test process. No stale PID-file ownership claim. This protects
  cooperating daemons, not malicious same-UID processes or shared filesystem use.
- Every SQL connection requires WAL, foreign keys ON, synchronous FULL,
  fullfsync/checkpoint_fullfsync ON, trusted_schema OFF. Startup checks effective
  settings. Parent/directory entries synced before ready. Durability means SQLite
  committed metadata on this filesystem, not artifacts, remote work or hardware
  power-loss qualification. Storage errors never become successful writes/acks.
- One transactional migration from truly empty schema 0 to schema 1 creates only
  metadata/tasks/attempts/events. Unknown versions/unrecognized state fail startup.
  Private reopen exists only for tests of their own disposable stores: generation
  persists, boot ID changes; product always creates a fresh random generation and
  random task/attempt/message/assignment IDs using crypto/rand.
- One fixture attempt per task, epoch 1; unique task/attempt/assignment/message
  IDs and composite epoch/revision constraints. No replacement-attempt admission
  or restore API. Transaction contains attempt CAS, task read-model update and
  event insert; stale identity checked before replay. Exact replay returns
  duplicate, conflicting reuse refuses. Revisions advance only on commit.
- Seed: #93 assign followed by assigned-to-unknown synthetic transition. No
  process was started: observation stays desired stop, not_started, remote unknown,
  quarantined. Synthetic terminal states never grant acceptance; succeeded maps
  only to awaiting_review. No result/receipt store or acknowledgement exists.
- Daemon links no runner/harness/enrollment, subprocess service, model/router
  client, scheduler, plugin loader or grant/acceptance/publication handler.
  Test subprocess controls and test SQL triggers exist only in `_test.go`.

## Verification

```sh
npm --prefix experiments/inference-boundary ci --ignore-scripts
go mod verify
test -z "$(gofmt -l cmd internal schemas tests/fixtures/protocol)"
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

`.github/workflows/go-foundation.yml` executes Linux/macOS checks, plus two
CGO-free `-trimpath -buildvcs=false` builds compared byte-for-byte. Existing
protocol and documentation workflows remain. Exact-head CI result, not workflow
presence, establishes CI evidence.

Real disposable SQLite tests cover creation/migration/reopen, connection setting
renewal, unique/FK constraints, invalid/stale transitions, replay, interrupted
transaction rollback, competing process ownership, SIGKILL after commit and
SIGKILL inside transaction after attempt/task writes but before event insert.
Owned child is identified by its `exec.Cmd`, never process-name matching.

Disk-full evidence uses SQLite `max_page_count` on a real file with a test-only
trigger allocating a blob after authoritative row updates: requires real
`SQLITE_FULL` code 13, rollback and successful reopen with prior committed state.
This is SQLite's bounded disk-capacity failure, **not** a filled host volume,
failed physical fsync, power cut or hardware fault experiment. Tests never fill
operator disk or kill unrelated processes. `/dev` real filesystem probe verifies
non-durable unsupported filesystem refusal; no mounted NFS/SMB lab is claimed.

API tests use real store and loopback HTTP; TypeScript check builds/runs actual
CGO-free daemon and validates its wire responses plus adverse schema mutations.
Process test supplies malicious config/plugin/state/remote-binding/proxy/router
settings and hostile requests, observes zero local HTTP trap hits and no execution
sentinel. This is evidence for declared input cases, not OS confinement of arbitrary
future code; no public-network probe or supported runner claim.

Local validation review pipeline is GPT-5.6 Sol-review, not Opus. Fresh required
Opus review before lock/guarded merge remains operator work; all head changes need
fresh exact-head review and actual green applicable CI. No merge in this task.
