# Provisional exact-content verification (#21)

`Recreate` reads the custody manifest v1 and exact Git base using plumbing with
hooks, fsmonitor, filters, replacement objects, lazy fetch and ambient Git config
disabled. It checks canonical manifest/content digests and base-relative omissions,
then copies into a fresh private directory without running repository code. Recovery
blobs are verified but excluded from the candidate tree. Symlinks, submodules,
unsafe paths and incompatible manifests refuse recreation. Manifest v1 has no mode
metadata: reconstruction is content-only, mode 0600, with that limitation retained.

`Run` takes an operator-approved `TrustedChecks` value (argv, explicit environment
allowlist/values, cwd, timeout, required flags and approval reference). Suggestions
and model success assertions are retained data only. Every check gets a fresh copy.
Reports include bounded output prefixes, full-stream SHA-256/lengths, timestamps,
exit/failure/refusal and observed environment. The journal commits the report and
all evidence IDs before `Run` returns; `Evaluate` requires every required check.

`Profile` is sealed. The default `Unqualified` implementation refuses with
`no proven Docker-free execution profile (#89)`. The explicitly selected
`NewDevelopmentProfile` measures write/network canaries before every trusted check
and remains development-only, not qualified for unattended execution. Its canonical
0700 state root must be outside system-temp exceptions. Each check gets separate
private HOME, TMPDIR/TMP/TEMP, XDG config/cache/data/state, GOPATH, GOCACHE and
GOMODCACHE directories in the isolation workspace beside that state root. These
runtime-owned variables override check-supplied values without mutating the approved
check; report `EnvKeys` still identifies the approved environment. GOENV is off and
GOTOOLCHAIN is local. No sandbox permissions are broadened: Go dependencies must
be vendored or otherwise available offline; network fetching remains denied.

Reports retain OS/arch, Docker discoverability on the recorded check PATH and
missing/drifted confinement observations. Docker is never invoked; binary presence
is not daemon availability or qualification. The `test-only-unconfined` executor
exists only in `_test.go`, including its constructor. It proves library mechanics,
not confinement, process-tree stop or product readiness.

`store.SelectVerificationCandidate` fences replacement; late reports remain history.
Store methods are trusted in-process boundaries, not worker/authentication APIs.
No supported runtime or execution, acceptance, publication or merge authority is
provided. #89 qualification, real verifier integration and mode-bearing custody
remain prerequisites. Tests: `go test -race ./internal/verification ./internal/store`.
