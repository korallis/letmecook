# Provisional candidate packaging and custody (#18)

`Pack` derives a complete bounded local candidate snapshot from a caller-selected,
trusted checkout and exact base commit. It invokes Git only with hooks, fsmonitor,
external diff/text conversion, filters, system config and optional locks disabled;
it never executes repository code. It records every present tracked and non-ignored
untracked regular file, classifies NUL-containing content as binary, and records
base-relative deletions. Symlinks, submodules and Git LFS pointers refuse the whole
package. Its private recovery-copy sources are then passed to `store.CustodyResult`.

The canonical `gaffer-artifact-manifest-v1` binds version, full attempt identity,
base revision plus SHA-256, outcome, explicit tracked/untracked/binary/recovery
lists and explicit deletions. `CustodyResult` verifies every copied source, syncs
staging, atomically promotes blobs and manifest on the configured local artifact
filesystem, syncs parent directories, then commits immutable SQLite references and
a receipt. A lost acknowledgement returns that same receipt. Stale generations are
quarantined and never selected current. It never moves/deletes runner recovery
files; GC only considers unreferenced expired objects.

This is an internal typed interface, not an HTTP/upload protocol. O6 still owns
network transfer, trusted checkout authority binding, session integration, concrete
retention policy, backup/restore and a qualified runner. No result delivery, launch,
verification, acceptance, publication or merge authority follows from this package.
