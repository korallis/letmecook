# Public synthetic baseline artifacts

Local evidence only. These APIs use the unchanged types and limits in
`../execution-contract.ts`; they grant no execution, acceptance or publication authority.

Import from `./index.ts`:

| API | Result |
| --- | --- |
| `retainEvidence(store, kind, value)` | `Promise<EvidenceRef>` |
| `readEvidence(store, ref)` | `Promise<unknown>` |
| `makeManifest(files)` | `SnapshotManifest` |
| `readSnapshot(store, ref)` | `Promise<{manifest: SnapshotManifest, bytes: SnapshotBytes}>` |
| `captureSnapshot(archive, baseManifest, rules, store, revision)` | `Promise<SnapshotRef>` |
| `retainCandidate(base, candidate, rules, store)` | `Promise<EvidenceRef>` for a `CandidateBundle` |

`store.directory` must already exist, be absolute with no trailing slash or dot
segments, and be owned by the current UID with mode `0700`. Keep its ancestors
trusted and keep worker processes outside the store. JSON records use sorted keys,
UTF-8, SHA-256 filenames and mode `0600`. Retention writes an exclusive temporary
file, syncs it, publishes with an exclusive hard link, syncs file and directory,
and checks content and inode identity before acknowledging. Existing records must
match. Readers verify canonical encoding, digest, permissions, link count and
identity. A failed retention may leave an unacknowledged temporary or final record;
callers must treat rejection as failure and can retry the same content.

`makeManifest` accepts full file descriptors with optional validated base64 content.
`captureSnapshot` consumes a frozen `Uint8Array` or `AsyncIterable<Uint8Array>` of
uncompressed USTAR, per-entry PAX, or GNU long-name tar with an initial `repo/`
directory header (any portable basename), or `./`. It never extracts. Every entry
is validated, including entries outside `readPaths` and bounded root `.git/`
controls. Controls and directory metadata are excluded from the file snapshot.
Source paths use portable ASCII components, reject `.git` segments, case aliases,
duplicates and file/directory collisions; regular source modes are `0644`/`0755`.
Links, devices, sparse entries, xattrs, global PAX, nonzero padding, truncation and
concatenated archives fail closed. Per-entry PAX permits only path, size, timestamps,
UID/GID and owner names. Base snapshots must match the registered full manifest.
Candidates keep every existing file and mode; only the two existing write paths
may change. Those files must be UTF-8 without NUL. A retained candidate bundle
requires both write paths to have changed. Every source byte is retained and
checked against both the file digest and complete manifest on readback.

Limits: 64 source files, 65,536 total source bytes, 1,048,576 archive/artifact bytes;
256 control files totaling 524,288 bytes, 256 directories, 64 metadata headers
totaling 16,384 bytes, and 65,536 yielded archive chunks. JSON also has depth 32 and
100,000-value limits. The caller owns the deadline for an archive producer that
stalls without yielding; this API bounds yielded data. This is a small synthetic
POSIX store, with no garbage collector, cross-process transaction or OS isolation.

## Verification

```sh
/opt/homebrew/opt/node@24/bin/node --test experiments/baseline/artifacts/artifacts.test.ts
/opt/homebrew/opt/node@24/bin/node experiments/baseline/node_modules/typescript/bin/tsc -p experiments/baseline/artifacts/tsconfig.json
```

Tests use public generated fixtures and local files, including native `/usr/bin/tar`
USTAR/PAX exports with xattrs disabled. Fault checkpoints exercise rejected
acknowledgement and real file/directory substitution around sync/publication.
They do not simulate power loss or prove kernel/filesystem crash durability.
