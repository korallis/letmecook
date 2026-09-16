# Explicit repository inputs (#14, provisional)

Related work: [#14](https://github.com/korallis/letmecook/issues/14), on merged
[#11 store](../../cmd/gafferd/README.md), [#12 grants](../../internal/authority/README.md)
and [#13 identity](../../cmd/gafferd/IDENTITY.md). Operator-authorized reversible
work only. **Closure remains deferred pending accepted/locked #9/#10.** No
foundation lock, supported execution runtime, execution readiness or downstream
acceptance follows from this slice.

## Typed entry points

`internal/repositories/` owns `Profile`, `Selection`, validation, protected-path
checks and trusted Git checkout. `internal/store/repositories.go` owns immutable
registration in the existing SQLite store (schema 5), not another database.
No repository HTTP endpoint, CLI command, discovery, UI, scheduler or job launcher
is added. The later CLI can call these typed operations through an authenticated
boundary; do not expose a raw fingerprint argument over the wire.

- `Store.ValidateRepository(ctx, actor, profile)` first checks the current owner,
  then reads the explicitly selected remote/ref into a disposable checkout. Returns
  the exact profile SHA-256 and resolved base commit. It stores nothing.
- `Store.RegisterRepository(ctx, actor, expectedRevision, profile)` repeats that
  access check, then rechecks owner identity and enrolled, non-revoked runner IDs
  in the commit transaction. Zero expected revision creates revision 1; replacements
  increase by exactly one. Identical retries are idempotent. Changes to any approved
  input, including verification commands or protected paths, need a fresh explicit
  owner approval/revision. Stored identity and historical profiles cannot mutate.
- `Store.RepositoryProfile(ctx, actor, id)` returns current approved inputs to a
  proven owner. It is not a freshness/access check.
- `Store.CheckRepositorySelection(ctx, selection)` checks current profile digest,
  revision, canonical remote, pinned base and exact runner/root pairing, plus current
  runner enablement/revocation. Relabelling another remote with an existing project
  ID is refused, even if it contains the same commit. One canonical remote has one
  registered ID. Old profile selections fail after replacement.
- `repositories.Prepare(ctx, profile, selection)` checks the exact selection and
  local canonical private root, then creates a new independent clone under it.
  Returns owned checkout path, profile digest and base commit; caller owns successful
  checkout cleanup. Failed preparation removes only its newly created directories.
- `Profile.CheckChanges(paths)` rejects changes to protected files/directories or
  their ancestors. Literal prefixes, not glob patterns. The future trusted verifier
  must compute the complete candidate diff itself, including both sides of renames;
  a worker-supplied list is not evidence. Commands in `Verification` are approved
  argv/directory/time-limit data, **never run by registration or trusted checkout**.

The actor is a certificate fingerprint established by #13's proof-of-possession
boundary (or explicit local owner setup), not authentication by possession of a
string. Owner auth is checked again after slow Git I/O; SQL commit failure returns
no registration. Read/profile errors use fixed repository or identity codes; Git
stderr, credentials and raw remote output are not returned.

Registration grants no execution, local acceptance, publication or merge. It creates
no execution grant, task or event and does not enable runners. Existing authority
checks remain separate. #15 must evaluate current repository, grant and runner-local
policy in its admission transaction; neither a selection check nor a checkout result
is a dispatch token. #16 must enforce host-local restrictions and OS confinement.
An owner-selected runner/root is a candidate allowlist entry, not authority to weaken
that runner's policy.

## Profile contract

Version: `repository-provisional-v1`. Profile binds:

- Stable repository ID and exact canonical remote; immutable identity mapping.
- Fully qualified `refs/heads/...`, 40/64 lowercase-hex commit and `pinned` policy.
  Fast-forward, force-push, missing ref and changed base all require explicit
  revalidation/reapproval. No follow-tip, implicit rebase or tag peeling policy.
- Non-null sorted unique protected paths and context scope. Paths are portable
  literal repository-relative names; `.` means entire tree. No traversal, `.git`,
  backslash or globs. Context scope records approval, not filesystem confinement.
- Named approved verification profile with ordered argv, relative working directory
  and positive timeout for each command. It cannot be replaced by repository text.
- Sorted exact enrolled runner-ID / absolute clean root pairs. Roots for remote
  runners are recorded, not probed by the daemon; local preparation validates its
  selected root. Symlink aliases, world/group-accessible roots and roots inside
  ordinary or bare Git administration are refused. Roots must be independently
  provisioned by that host's operator outside existing checkouts.

Collections cap at 128 entries, verification commands at 32 and serialized profile
at 64 KiB. SHA-256 binds the full typed profile including revision, protection,
verification, context and runner roots. SQL retains approval actor and validation
wall-clock timestamp alongside it. Git SHA binds content, not forge ownership.

## Read-only access and checkout boundary

**Only anonymous HTTPS and explicit local `file://` remotes are currently supported.**
HTTPS URLs must be canonical lowercase host plus literal `.git` path, without
userinfo, port, query, fragment, encoded aliases or redirects. File URLs must name
an existing canonical absolute directory without symlink aliases. Unsupported SSH,
remote helpers, implicit paths and embedded credentials fail closed. No general
forge credential, host SSH agent, private forge credential helper or write-access
probe is used. Private repositories needing credentials are unavailable until a
separately reviewed, repository-scoped read credential broker exists; there is no
fallback to host credentials. Anonymous public repositories and operator-permitted
local repositories provide actual read access without a forge credential.

Trusted checkout uses Git from the trusted host installation, with a fresh empty
HOME and explicit environment rather than inherited Git, proxy, token or SSH state.
System/global configuration, templates, attributes and unmanaged hooks are disabled;
credential helpers are empty, redirects forbidden, protocols allowlisted, submodules
not initialized, replace objects disabled and maintenance disabled. Each Git command
has a 30-second deadline, overall preparation two minutes, bounded stdout and no
returned stderr. Linux/macOS cancellation kills only that command's owned process
group, including transport children; unsupported platforms refuse preparation. It fetches only the selected branch at depth 1 into fresh independent
Git administration, compares the fetched commit to the approved immutable SHA,
rechecks the advertised ref, then checks out detached. No reset, clean, branch change
or command runs in the operator's existing checkout. No shared object alternates or
worktree administration. Returned clone retains neither remote nor credential config.

A ref can move after validation; the delivered checkout still contains the exact
pinned commit. Every later preparation revalidates the ref. URL plus pinned commit
is this provisional repository identity, **not** a forge numeric-ID attestation or
guarantee that the server will never be renamed/reassigned. No silent URL migration.
Local Git reads may read that selected repository's object storage; they never grant
read access to arbitrary operator repositories or automatically discover them.

Git controls prevent accidental hooks/config execution here; they are not a worker
security boundary. A worker can change Git configuration, and repository builds,
plugins and scripts are arbitrary code. The eventual job must receive only this
checkout inside qualified OS confinement, no host home, daemon state, general forge
credential, SSH agent or credential-bearing environment. Trusted Git I/O currently
has time/output bounds, not a qualified disk/CPU/native containment profile. Use only
operator-permitted bounded repositories for this provisional API. Same-UID hostile
root replacement and malicious Git/native-tool vulnerabilities are not contained by
these checks. No execution support or resource/isolation qualification is claimed.

Publication remains [#35/#36](../implementation-plan.md): separate trusted executor,
separate scoped credentials, approved exact artifact/action and durable idempotency.
A registration, readable remote, local commit or execution grant never authorizes a
push, pull request or merge. This implementation performs no forge writes.

## Verification

```sh
go test -race -count=1 ./internal/repositories ./internal/store
```

Tests generate real disposable Git repositories and SQLite stores with stdlib test
helpers, not source-string assertions or live repositories. Coverage includes:

- Read/checkout success, pinned SHA and clean detached clone, independent object
  storage, no stored remote; dirty staged/unstaged/untracked operator files, user
  branch and refs remain unchanged.
- Inaccessible/missing remote/ref, branch drift, relabelled repository or remote,
  wrong/aliased root, checkout-root refusal, cancellation and failed-clone cleanup.
- Malicious operator/remote/template hooks, inherited hook/URL rewrite/filter/helper
  configuration, local upload-pack hook, askpass and SSH-agent variables; marker
  side effects never occur. Owned transport-child cancellation has a separate
  executable side-effect test.
- Real protected-file edit/rename diffs, unchanged-name prefix boundaries, changed
  protection digest, hostile profile values and stale selection refusal.
- Owner-only writes/reads, disabled and revoked runners, CAS races, immutable rows,
  rollback after injected SQLite insert failure, idempotent replay, reopen durability,
  schema 4 migration and fixture-store refusal.

No live forge/private credential, inference, production host, publication, OS
isolation, disk exhaustion or hardware-power-loss qualification is claimed.
