#!/usr/bin/env bash
# Generates the three disposable synthetic Gaffer acceptance repositories.
#
# Usage: make-repos.sh <output-dir>
#
# Idempotent: re-running against an existing output directory is safe. Bare
# repositories whose initial commit SHA already matches the pinned value are
# left untouched; only missing repositories are (re)created. Each generated
# repository is a bare git repo (work happens in fresh trusted checkouts, never
# here) plus a repo-profile.json skeleton with the trusted verification
# commands the verifier will run.
#
# Determinism: every commit uses fixed identity and dates
# (GIT_AUTHOR_DATE/GIT_COMMITTER_DATE, author "fixture" <fixture@example.invalid>),
# so the initial commit SHAs printed below are stable across runs and machines.
# They are pinned in this script and verified after generation.
#
set -euo pipefail

if [ "$#" -ne 1 ]; then
  echo "usage: $0 <output-dir>" >&2
  exit 2
fi
OUT=$1
mkdir -p "$OUT"

# Fixed commit identity and dates: SHAs below are only stable because of these.
export GIT_AUTHOR_NAME=fixture GIT_AUTHOR_EMAIL=fixture@example.invalid
export GIT_COMMITTER_NAME=fixture GIT_COMMITTER_EMAIL=fixture@example.invalid
export GIT_AUTHOR_DATE='2026-09-17T00:00:00Z+00:00'
export GIT_COMMITTER_DATE='2026-09-17T00:00:00Z+00:00'
GITCFG=(git -c user.name=fixture -c user.email=fixture@example.invalid)
# Pin hash algorithm and disable any inherited or system configuration so the
# objects (and therefore the SHAs) do not depend on the generating machine.
export GIT_CONFIG_NOSYSTEM=1
export GIT_CONFIG_GLOBAL=/dev/null

# Pinned initial commit SHAs (created and verified by this script). Plain
# variables, not associative arrays: macOS ships bash 3.2.
#   greeting: 479eec44aa5cda0a5f91a48b2e5b6119c18570ba
#   calc:     cd739784278eb03050235d38d07c327c7811b36b
#   notes:    82cabef608abfb716d1415eb14e9dcb2044f6712
PINNED_greeting=479eec44aa5cda0a5f91a48b2e5b6119c18570ba
PINNED_calc=cd739784278eb03050235d38d07c327c7811b36b
PINNED_notes=82cabef608abfb716d1415eb14e9dcb2044f6712
BRANCH_greeting=refs/heads/main
BRANCH_calc=refs/heads/main
BRANCH_notes=refs/heads/main

# 1x1 RGBA PNGs, byte-for-byte (color source: pngsuite.com public 1x1 basis
# test images; white seed and green target generated from the PNG spec).
PNG_WHITE=$'\211\120\116\107\015\012\032\012\000\000\000\015\111\110\104\122\000\000\000\001\000\000\000\001\010\006\000\000\000\037\025\304\211\000\000\000\013\111\104\101\124\170\332\143\370\017\004\000\011\373\003\375\150\372\034\314\000\000\000\000\111\105\116\104\256\102\140\202'
PNG_GREEN=$'\211\120\116\107\015\012\032\012\000\000\000\015\111\110\104\122\000\000\000\001\000\000\000\001\010\006\000\000\000\037\025\304\211\000\000\000\015\111\104\101\124\170\332\143\140\370\317\360\037\000\004\001\001\377\256\265\125\365\000\000\000\000\111\105\116\104\256\102\140\202'

# seed_greeting <dir>: shell/text repo. greeting.txt starts as "hello\n"; the
# happy-path S-04 task edits it to "hello, gaffer\n".
seed_greeting() {
  local d=$1
  mkdir -p "$d"
  printf 'hello\n' >"$d/greeting.txt"
  printf '# greeting\n\nDisposable synthetic shell/text repository for Gaffer acceptance scenarios.\n' >"$d/README.md"
}

# seed_calc <dir>: tiny Go module with one passing unit test.
seed_calc() {
  local d=$1
  mkdir -p "$d"
  cat >"$d/go.mod" <<'EOF'
module example.invalid/gaffer-calc

go 1.26
EOF
  cat >"$d/calc.go" <<'EOF'
// Package calc is a disposable synthetic calculator used by Gaffer acceptance scenarios.
package calc

// Add returns a + b.
func Add(a, b int) int { return a + b }

// Mul returns a * b.
func Mul(a, b int) int { return a * b }
EOF
  cat >"$d/calc_test.go" <<'EOF'
package calc

import "testing"

func TestAdd(t *testing.T) {
	if got := Add(2, 3); got != 5 {
		t.Fatalf("Add(2, 3) = %d, want 5", got)
	}
}

func TestMul(t *testing.T) {
	if got := Mul(2, 3); got != 6 {
		t.Fatalf("Mul(2, 3) = %d, want 6", got)
	}
}
EOF
}

# seed_notes <dir>: markdown/docs repo with an internal link graph and a tiny
# committed Node link-check script the trusted verification runs.
seed_notes() {
  local d=$1
  mkdir -p "$d/docs" "$d/scripts"
  cat >"$d/README.md" <<'EOF'
# notes

Disposable synthetic markdown/docs repository for Gaffer acceptance scenarios.

- [Overview](docs/overview.md)
- [Guide](docs/guide.md)
- [Placeholder](docs/placeholder.md)
EOF
  cat >"$d/docs/overview.md" <<'EOF'
# Overview

Synthetic overview page. See the [guide](guide.md) for usage.
EOF
  cat >"$d/docs/guide.md" <<'EOF'
# Guide

Synthetic guide page. Start at the [overview](overview.md).
EOF
  cat >"$d/docs/placeholder.md" <<'EOF'
# Placeholder

Reserved for generated content.
EOF
  cat >"$d/scripts/check-links.js" <<'EOF'
#!/usr/bin/env node
// Checks that every relative markdown link in the repository resolves to an
// existing file. Synthetic trusted check committed to the notes repository.
'use strict';
const fs = require('fs');
const path = require('path');
const roots = process.argv.length > 2 ? process.argv.slice(2) : ['.'];
const link = /\[[^\]]*\]\(([^)#\s]+)[^)]*\)/g;
let failures = 0;
const walk = dir => {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    if (entry.name === '.git' || entry.name === 'scripts') continue;
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) { walk(full); continue; }
    if (!entry.name.endsWith('.md')) continue;
    const base = path.dirname(full);
    const text = fs.readFileSync(full, 'utf8');
    for (const match of text.matchAll(link)) {
      const target = decodeURIComponent(match[1]);
      if (/^[a-z][a-z0-9+.-]*:/i.test(target) || target.startsWith('/') || target.startsWith('#')) continue;
      const resolved = path.join(base, target);
      if (!fs.existsSync(resolved)) {
        console.error(`broken link: ${path.relative('.', full)} -> ${target}`);
        failures++;
      }
    }
  }
};
for (const root of roots) walk(root);
if (failures > 0) process.exit(1);
EOF
  chmod +x "$d/scripts/check-links.js"
}

# profile_greeting / profile_calc / profile_notes: repo-profile.json skeletons.
# Placeholder <sha> values are replaced with the actual initial commit SHA after
# commit; remote/runner_roots are per-installation placeholders the operator
# completes when registering (see README.md).
profile_greeting() {
  cat <<EOF
{
  "version": "repository-provisional-v1",
  "id": "greeting",
  "revision": 1,
  "remote": "file://$OUT_ABS/greeting.git",
  "base": {
    "ref": "refs/heads/main",
    "commit": "$1",
    "policy": "pinned"
  },
  "protected_paths": [".gitignore", "repo-profile.json"],
  "verification": {
    "name": "greeting-echo-v1",
    "commands": [
      {
        "argv": ["grep", "-qx", "hello, gaffer", "greeting.txt"],
        "directory": ".",
        "timeout_ms": 10000
      },
      {
        "argv": ["test", "-f", "README.md"],
        "directory": ".",
        "timeout_ms": 10000
      }
    ]
  },
  "context_scope": ["."],
  "runner_roots": [
    {
      "runner_id": "11111111-1111-4111-8111-111111111111",
      "root": "/private/tmp/gaffer-system-run/roots/greeting"
    }
  ]
}
EOF
}

profile_calc() {
  cat <<EOF
{
  "version": "repository-provisional-v1",
  "id": "calc",
  "revision": 1,
  "remote": "file://$OUT_ABS/calc.git",
  "base": {
    "ref": "refs/heads/main",
    "commit": "$1",
    "policy": "pinned"
  },
  "protected_paths": [".gitignore", "repo-profile.json"],
  "verification": {
    "name": "calc-go-v1",
    "commands": [
      {
        "argv": ["go", "test", "./..."],
        "directory": ".",
        "timeout_ms": 120000
      },
      {
        "argv": ["gofmt", "-l", "."],
        "directory": ".",
        "timeout_ms": 30000
      }
    ]
  },
  "context_scope": ["."],
  "runner_roots": [
    {
      "runner_id": "11111111-1111-4111-8111-111111111111",
      "root": "/private/tmp/gaffer-system-run/roots/calc"
    }
  ]
}
EOF
}

profile_notes() {
  cat <<EOF
{
  "version": "repository-provisional-v1",
  "id": "notes",
  "revision": 1,
  "remote": "file://$OUT_ABS/notes.git",
  "base": {
    "ref": "refs/heads/main",
    "commit": "$1",
    "policy": "pinned"
  },
  "protected_paths": [".gitignore", "repo-profile.json"],
  "verification": {
    "name": "notes-links-v1",
    "commands": [
      {
        "argv": ["node", "scripts/check-links.js", "."],
        "directory": ".",
        "timeout_ms": 30000
      }
    ]
  },
  "context_scope": ["."],
  "runner_roots": [
    {
      "runner_id": "11111111-1111-4111-8111-111111111111",
      "root": "/private/tmp/gaffer-system-run/roots/notes"
    }
  ]
}
EOF
}

# make_repo <name> <seedfn> <profilefn>: create bare repo + working clone,
# seed it, commit once with the fixed identity/date, push, verify the pinned
# SHA, then write the profile skeleton next to the bare repo.
make_repo() {
  local name=$1 seedfn=$2 profilefn=$3
  local bare="$OUT/$name.git"
  local work
  work=$(mktemp -d "${TMPDIR:-/tmp}/gaffer-mr-XXXXXX")
  trap 'rm -rf "$work"' EXIT

  if [ -d "$bare" ]; then
    local existing pinned
    pinned=$(eval "echo \"\$PINNED_$name\"")
    existing=$("${GITCFG[@]}" -C "$bare" rev-parse HEAD 2>/dev/null || true)
    if [ "$existing" = "$pinned" ]; then
      echo "$name: ok (pinned $pinned)"
      return 0
    fi
    echo "$name: WARNING existing $bare has unexpected HEAD ${existing:-<none>}; regenerating" >&2
    rm -rf "$bare"
  fi

  "${GITCFG[@]}" init --bare --initial-branch=main "$bare" >/dev/null
  "${GITCFG[@]}" clone --quiet "$bare" "$work/$name" >/dev/null 2>&1

  $seedfn "$work/$name"
  # Store the binary seed bytes where a later corpus binary-edit task finds
  # them; checked in so the edit is a pure content change, not a new file type.
  if [ "$name" = greeting ]; then
    printf '%s' "$PNG_WHITE" >"$work/$name/asset.png"
  fi

  "${GITCFG[@]}" -C "$work/$name" add -A
  "${GITCFG[@]}" -C "$work/$name" commit -q -m 'seed: synthetic fixture content'
  local sha pinned2
  pinned2=$(eval "echo \"\$PINNED_$name\"")
  sha=$("${GITCFG[@]}" -C "$work/$name" rev-parse HEAD)
  local branch
  branch=$(eval "echo \"\$BRANCH_$name\"")
  "${GITCFG[@]}" -C "$work/$name" push -q origin HEAD:"$branch"

  if [ "$sha" != "$pinned2" ]; then
    echo "$name: FATAL generated initial commit $sha does not match pinned $pinned2; regenerate pins deliberately, never silently" >&2
    exit 1
  fi

  $profilefn "$sha" >"$OUT/$name.repo-profile.json"
  echo "$name: created $(basename "$bare") initial-commit $sha"
}

OUT_ABS=$(cd "$OUT" && pwd)
make_repo greeting seed_greeting profile_greeting
make_repo calc seed_calc profile_calc
make_repo notes seed_notes profile_notes
trap - EXIT
