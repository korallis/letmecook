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

`Profile` is sealed. The sole product implementation, `Unqualified`, always refuses
with `no proven Docker-free execution profile (#89)`, OS/arch, Docker discoverability
on the recorded check PATH and missing/drifted confinement observations. Docker is
never invoked; binary presence is not daemon availability or qualification. The
`test-only-unconfined` executor exists only in `_test.go`, including its constructor.
It proves library mechanics, not confinement, process-tree stop or product readiness.

`store.SelectVerificationCandidate` fences replacement; late reports remain history.
Store methods are trusted in-process boundaries, not worker/authentication APIs.
No supported runtime or execution, acceptance, publication or merge authority is
provided. #89 qualification, real verifier integration and mode-bearing custody
remain prerequisites. Tests: `go test -race ./internal/verification ./internal/store`.
