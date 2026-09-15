# Provisional protocol consistency fixtures

Use Node 24+ and the Go version declared in [go.mod](../../../go.mod). Run from
repository root, installing the existing pinned TypeScript toolchain first:

```sh
npm --prefix experiments/inference-boundary ci --ignore-scripts
experiments/inference-boundary/node_modules/.bin/tsc --noEmit --strict --target es2023 --module nodenext --allowImportingTsExtensions schemas/execution/protocol.ts tests/fixtures/protocol/check.ts --typeRoots experiments/inference-boundary/node_modules/@types
test -z "$(gofmt -l schemas/execution tests/fixtures/protocol)"
go vet ./...
go test -race ./...
node tests/fixtures/protocol/check.ts
```

Node runs TypeScript directly; Go uses only stdlib. These commands match the
[protocol workflow](../../../.github/workflows/execution-protocol.yml).
No application dependency or test framework is added.
`check.ts` calls the actual TypeScript validator/check interfaces, runs the Go
fixture command with the same JSON, and asserts every output against both explicit
expectations and its peer. `go test` independently runs the same expectations.
`main.go` is an owned test command, not a product runner or service.

`cases.json` owns synthetic wire messages, raw malformed byte cases, all 100
attempt-state edge checks, and named expected adverse traces. `decode` consumes
JSON text/hex bytes with optional space padding (including exact/over-size bounds).
Other cases reference named wire messages, decode them, then execute public checks.
`trace` groups ordered, explicitly supplied evidence/check steps; `observe` folds
observations through recovery events. The fixture driver supplies trusted claims;
it does not collect evidence or emulate a network, filesystem or real process.

Covered: duplicate message/assignment IDs and conflicts; lost assignment/result
acks; generation/epoch/attempt/task mismatch; malformed, duplicate-key, unsupported
version/field, UTF-8, depth, digest and numeric/byte limits; request-send anchored
lease cutoffs, clock reversal, drift/termination margins, delayed/old nonce replies,
expired prior lease, restart/partition quarantine; event-only persistence refusing
ack, mismatched manifest/receipt, and lost-ack resend retaining identity.

These tests establish contract consistency only. Lease cutoff cannot prove a
watchdog stopped; process termination cannot prove remote quiescence; a synthetic
receipt cannot prove artifact custody. Quarantine is never cleared automatically,
and no function returns execution/retry, local acceptance or publication permission.
See [contract and reconciliation checklist](../../../schemas/execution/README.md).
