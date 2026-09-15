# Provisional embedded read-only shell (#95)

React / TypeScript / StyleX page compiled by Vite with the official
`@stylexjs/unplugin` integration and embedded into `gafferd` by `web/embed.go`.
[`package.json`](package.json) owns the direct dependency pins.
It renders one bounded snapshot from the daemon's fixture mode on load and
each refresh; the [read contract](../cmd/gafferd/README.md#read-contract-for-95)
owns the snapshot bounds. Not M1/M2 acceptance, not the #26 shell,
not authentication, not a supported UI runtime. The
[#9 reconciliation record](../docs/decisions/0001-execution-foundation.md#provisional-slice-reconciliation)
owns the shell's provisional disposition.

## Build and serve

Use Node.js satisfying [`package.json`'s `engines`](package.json). From the repository root:

```sh
npm --prefix web ci --ignore-scripts
npm --prefix web run build          # web/dist (deterministic; ignored by git)
```

Then follow the [daemon build and startup](../cmd/gafferd/README.md#bounded-local-startup)
with `--fixture`. Persistent `store-only` installations expose read API only, not
this fixture shell; no UI promotion follows from persistent storage.
Open its printed origin at `/`, replacing `/api/v1/status` in the printed URL.

`go:embed all:dist` includes whatever is in `web/dist` at Go build time. Build the
shell first; each build removes previous output and restores the tracked
`.gitkeep` placeholder. In fixture mode, a Go binary built without the shell answers
`GET /` with the daemon's JSON `ui_unavailable` refusal (503) instead of serving any fallback.
There is no dev-server or proxy path; the shell is tested and served from the
daemon's own loopback origin.

The shell serves `/` (index.html) and embedded `/assets/<name>.js|css` files;
Vite generates hashed names, while `Asset` in [`embed.go`](embed.go) owns path
validation. Both routes pass the daemon's [boundary checks](../cmd/gafferd/README.md#read-contract-for-95)
and reject queries. The shell CSP permits only same-origin scripts, styles and
connections, and forbids framing, base URLs and form actions. Other shell paths
are refused as JSON.

## What the page shows and refuses

- Single [`tokens.stylex.ts`](src/tokens.stylex.ts) (`defineVars`) owns light/dark
  colours and typography. Component styling uses `stylex.create`/`stylex.props`;
  `global.css` sets the browser colour scheme and removes the body margin. No CSS
  framework, component library, router, state library or second styling system.
- [`STATUS_WORDS` in `src/read.ts`](src/read.ts) owns the explicit status labels;
  HTTP status and validated daemon error code add detail when available.
  Status never depends on colour. Labels reuse the daemon's own
  vocabulary (`fixture-only`, `missing_capabilities`, protocol states,
  `not_started`, `quarantined`) and never claim saved, acknowledged, executed,
  accepted or reviewed.
- Successful snapshots are decoded with the shared `schemas/readapi/types.ts`
  runtime schema; error bodies must match the daemon's closed error shape or are treated
  as malformed. Oversized responses, non-JSON, redirects and unknown codes fail
  closed. The single control is "Refresh snapshot".
- No login, device, task action, approval, stop claim, router/account state,
  SSE, WebSocket, service worker, storage, cookies or offline state exist. The
  browser tests observe requests and runtime API calls during load and refresh,
  and check that no persistent browser state is created.

## Checks

```sh
npm --prefix web run typecheck
npm --prefix web run doctor          # react-doctor, telemetry off, errors block
npm --prefix web run build
go test -count=1 ./internal/httpapi/ ./web/
npm --prefix web exec playwright install chromium
npm --prefix web test                # real daemon, axe, 360/1280 light/dark
```

`npm test` checks daemon cleanup on startup failures and teardown, then builds
and starts the real CGO-free daemon, opens the embedded page,
asserts rendered rows against the daemon's own JSON, drives the refresh button
through malformed/server-error/empty/unavailable/loading/unknown by intercepting
the request in the browser, walks the keyboard path, checks visible focus and
reduced motion, and probes from the page that direct requests cannot enable
actions, imports, remote binding or non-GET methods. axe must report zero
serious/critical findings. Outputs land in `web/reports/` (git-ignored):
`react-doctor.json`, `playwright.json`, `screenshots/{360,1280}-{light,dark}.png`.
CI (`.github/workflows/web-shell.yml`) uploads the same files as an artifact and
checks that rebuilding removes stale assets and produces byte-identical output.
Doctor score does not replace behaviour
or rendered review.

Skipped deliberately: dev-server proxy (no CORS by design), component unit tests
without a browser (all rendering assertions run against the served build),
supply-chain scan inside React Doctor (network; `--no-supply-chain`).
