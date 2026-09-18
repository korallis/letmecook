#!/usr/bin/env bash
# Public-binary M1 development proof. Never changes product code or fixture data.
set -euo pipefail
umask 077
ROOT=$(cd "$(dirname "$0")/.." && pwd -P)
export GAFFER_PROOF_CHECKOUT="$ROOT"
export GAFFER_PROOF_BIN=/private/tmp/gaffer-m1-bin
export GAFFER_SYSTEM_ROOT="${GAFFER_SYSTEM_ROOT:-$HOME/.gaffer-system/$(date -u +%Y%m%dT%H%M%SZ)-$$}"
export GAFFER_PROOF_JSON="${GAFFER_PROOF_JSON:-$ROOT/docs/evidence/m1-acceptance-run.json}"
mkdir -p "$GAFFER_PROOF_BIN" "$GAFFER_SYSTEM_ROOT"
chmod 700 "$GAFFER_SYSTEM_ROOT"
cd "$ROOT"
for binary in gafferd gaffer gaffer-runner; do
  CGO_ENABLED=0 go build -trimpath -buildvcs=false -o "$GAFFER_PROOF_BIN/$binary" "./cmd/$binary"
done
go test -tags system -count=1 -timeout 90m -v ./tests/system/...
