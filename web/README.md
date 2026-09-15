# Provisional embedded read-only shell (#95)

React 19 / TypeScript 5.9 / StyleX 0.19 page compiled by Vite with the official
`@stylexjs/unplugin` integration and embedded into `gafferd` by `web/embed.go`.
It renders one bounded `GET /api/v1/snapshot?limit=50` read from the #94
fixture-only daemon and nothing else. Not M1/M2 acceptance, not the #26 shell,
not authentication, not a supported UI runtime; retain, port or discard it with
the #9 foundation decision.

## Build and serve

```sh
npm --prefix web ci --ignore-scripts
npm --prefix web run build          # web/dist (deterministic; ignored by git)
CGO_ENABLED=0 go build -trimpath -buildvcs=false -o .local/gafferd ./cmd/gafferd
.local/gafferd                      # open the printed origin at /
```

`go:embed all:dist` includes whatever is in `web/dist` at Go build time. Build the
shell first; each build removes previous output and restores the tracked
`.gitkeep` placeholder. A Go binary built without the shell answers `GET /` with the daemon's JSON
`ui_unavailable` refusal (503) instead of serving any fallback. There is no dev
server, proxy or CORS path: the daemon refuses foreign `Host`/`Origin`, so the
shell is only ever tested and served from the daemon's own loopback origin.

Only `/` (index.html) and hashed `/assets/<name>.js|css` are servable. Both go
through every existing #94 boundary check (loopback peer, exact Host/Origin,
no forwarding headers, GET only, no query, no body, 512-byte URI) and a stricter
`script-src 'self'; connect-src 'self'` CSP. Everything else is 404/400/405 JSON.

## What the page shows and refuses

- Single `tokens.stylex.ts` (`defineVars`) with light/dark pairs at 8.4:1 or
  better; all styling via `stylex.create`/`stylex.props`. No CSS framework,
  component library, router, state library or second styling system.
- Status is one explicit word set from `src/read.ts`: `Loading`, `Read`,
  `Empty`, `Server error (HTTP, code)`, `Malformed (HTTP)`, `Unavailable`,
  `Unknown`. Colour never carries meaning. Labels reuse the daemon's own
  vocabulary (`fixture-only`, `missing_capabilities`, protocol states,
  `not_started`, `quarantined`) and never claim saved, acknowledged, executed,
  accepted or reviewed.
- Every response is decoded with the shared `schemas/readapi/types.ts` runtime
  schema; error bodies must match the daemon's closed error shape or are treated
  as malformed. Responses over 1 MiB, non-JSON, redirects and unknown codes fail
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
