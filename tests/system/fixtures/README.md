# M1 acceptance fixture corpus (issue #24)

Version 1 fixture data for the S-11, S-12 and S-13 system scenarios described in
`docs` job record 0002 (M1 end-to-end integration contract). **Everything here is
synthetic and public: three disposable repositories (`greeting`, `calc`, `notes`),
generated task definitions, expected fault outcomes and a recovery schedule.**

**Nothing in this directory is evidence of execution.** These are inputs and
expected-state plans for `tests/system` Go tests that do not exist yet. No
supported execution runtime, verification report, acceptance or downstream
approval follows from anything committed here.

| File | Purpose |
| --- | --- |
| `corpus-v1.json` | S-12: 20 representative sequential tasks across the three repos with deterministic fake-harness edit specs, expected outcomes and per-task trusted-check expectations |
| `fault-matrix-v1.json` | S-11: expected terminal state, reservation, `remote_work` and invariant assertions for each fault-injection fake mode |
| `recovery-100-v1.json` | S-13: 100 generated trivial tasks, the 10-SIGKILL restart schedule and the timing/percentile method (30 s old-lease barrier included) |
| `repos/make-repos.sh` | Idempotent bash generator for the three bare git repositories with pinned initial commits and `repo-profile.json` skeletons |
| `repos/gen-recovery.ts` | Deterministic generator for `recovery-100-v1.json` (Node 24+, no dependencies); re-running must reproduce the committed file byte-for-byte |
| `check.ts` | Structural validator for everything above; run `node tests/system/fixtures/check.ts` from the repository root |

## The three repositories

`greeting` is a shell/text repo (a one-line greeting file plus a 1x1 PNG asset),
`calc` a tiny Go module with one passing test, `notes` a markdown/docs repo whose
trusted check is a committed Node link-checker. All seeds, authors and dates are
fixed. Initial commit SHAs are pinned in `make-repos.sh` and are only stable
because the script fixes `GIT_AUTHOR_DATE`/`GIT_COMMITTER_DATE` and the author
`fixture <fixture@example.invalid>`; the script verifies them after generation
and fails loudly if they drift.

Each repo's `repo-profile.json` skeleton follows `repository-provisional-v1`
with `verification.commands` as trusted argv checks the verifier will run:
`greeting-echo-v1` (grep the greeting line, README exists), `calc-go-v1`
(`go test ./...`, `gofmt -l .`), `notes-links-v1` (`node scripts/check-links.js .`).
The `remote` and `runner_roots` fields are per-installation placeholders the
operator rewrites when registering against a concrete system-test root.

## How tests/system consumes this

The system tests (S7 slice, `//go:build system`) are written later on top of the
real binaries; this corpus is their data:

1. Run `repos/make-repos.sh <tmp>` to create the three bare repos; the pinned
   initial commits are printed and become each repo's registered `base.commit`.
2. Register each repo with `gaffer repo register` from its generated profile
   (after rewriting `remote`/`runner_roots` for the test root).
3. Drive `corpus-v1.json` tasks in id order, one active attempt at a time, via
   `gaffer flow run` (or the API directly), feeding each task's `fake_spec` to
   the fake harness and asserting the task's `expected_outcome`, the per-command
   `verification` expectations, and that observed artifacts equal the pinned
   edit contents byte-for-byte.
4. Drive `fault-matrix-v1.json` rows one per attempt against the greeting repo
   and assert each row's `attempt_state`, `reservation_released`, `remote_work`,
   `receipt` and `assertions` against persisted store state.
5. Drive `recovery-100-v1.json` under `--auto-retry`, SIGKILLing gafferd at the
   scheduled task indexes, recording the four timestamps per restart and
   computing p50/p95/max exactly as `timing_method` prescribes.

## Checks

```
node tests/system/fixtures/check.ts          # from the repository root
repos/make-repos.sh /tmp/any-dir             # twice; SHAs must be identical
```

`check.ts` validates task counts and unique ids, repo references, path/edit
agreement, outcome enums and outcome/mode coherence, fault-matrix rows and
invariants, recovery 100/10 counts, canonical JSON formatting, gen-recovery
determinism and that `make-repos.sh` is executable with the pinned SHAs. It is
a data validator only: it never runs a harness, never starts a daemon and never
produces execution evidence.
