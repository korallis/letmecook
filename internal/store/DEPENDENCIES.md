# Provisional SQLite dependency review (#94)

Reviewed before adding the driver on 15 September 2026. This is a bounded
source/licence-fit review, not legal certification, the full #59 audit, a licence
grant for Gaffer, or release approval. Original code still has no applied licence;
MIT remains intended (`docs/README.md#licence`). No candidate project code imported.

Use `database/sql` with CGO-free `modernc.org/sqlite v1.59.0`. Review used the
versioned Go module archives (authenticated by `go.sum`), their `LICENSE` files,
and libc's `LICENSE-3RD-PARTY.md`. SQLite itself is public domain; modernc's Go
translation/wrapper is BSD-3-Clause. Keep their distinct notices.

| Module | Pinned version | Terms |
| --- | --- | --- |
| modernc.org/sqlite | v1.59.0 | BSD-3-Clause; SQLite public domain |
| modernc.org/libc | v1.75.7 | BSD-3-Clause; bundled Go BSD, musl MIT/BSD/permissive math notices, go-netdb MIT, Nixpkgs MIT |
| modernc.org/mathutil | v1.7.1 | BSD-3-Clause |
| modernc.org/memory | v1.12.1 | BSD-3-Clause |
| golang.org/x/sys | v0.47.0 | BSD-3-Clause |
| github.com/dustin/go-humanize | v1.0.1 | MIT |
| github.com/google/uuid | v1.6.0 | BSD-3-Clause |
| github.com/mattn/go-isatty | v0.0.24 | MIT |
| github.com/ncruces/go-strftime | v1.0.0 | MIT |
| github.com/remyoudompheng/bigfft | v0.0.0-20230129092748-24d4a6f8daec | BSD-3-Clause |

These permissive terms fit the intended MIT original-code direction without
relicensing third-party material: retain copyrights, licence/disclaimer text and
no-endorsement conditions. `THIRD_PARTY_NOTICES.txt` retains the reviewed runtime
notices. `go list -deps -f '{{with .Module}}{{.Path}} {{.Version}}{{end}}' ./cmd/gafferd`
identifies the imported module graph, rather than treating every upstream development
tool as distributed code. Stdlib remains under Go's BSD licence. libc imports
`os/exec` for unrelated C compatibility routines; this is not a daemon subprocess
service. Inspection of the local CGO-free linked binary (`go tool nm`) found no
`os/exec`, `Xsystem` or `Xpopen` symbols after Go dead-code elimination. Product
SQL is fixed/parameterized, extension loading is never enabled and no SQL API is
exposed. Behavioral malicious-config/request tests remain the boundary evidence,
not an import-string test.

Upstream module declarations also mention development-only `modernc.org/fileutil
v1.4.0` (BSD-3-Clause) and `github.com/google/pprof
v0.0.0-20260802141513-ef3492d7dac3` (Apache-2.0). They are not imported by the daemon.
Their mere presence in upstream module metadata is not a shipped-code claim.
Before distribution, #59 must audit the selected target's actual binary and bundled
sources (including translated libc's per-component notices), packaging, all other
project dependencies and final owner licence authority. No release in this slice.

Reproduce source evidence with `go mod download -json <module>@<version>`; its
`Dir` contains the notices above, and `Sum` must match `go.sum`. Recheck this review
and notices whenever the selected dependency graph changes.
