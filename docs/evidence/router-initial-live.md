# First initial live suite — 15 September 2026

The first authorised live trial **failed after one physical attempt**. No worker
candidate was acknowledged, and the planner and both continuity phases did not
start. The gateway and relay stopped successfully with persistent evidence retained.
The original operation remains unknown; stopping a local process does not prove
provider quiescence. No replacement trial or baseline case was started.

The [redacted observation](router-initial-live-run.json) records the exact reviewed
source and deployment packet, shared scope, physical debit and failure. The
[synthetic suite evidence](router-initial-suite.md) remains separate and unchanged.
The immutable private report, retained gateway result and stopped-container logs
were checked directly. Their digests permit the operator to verify the private
originals without publishing account identities, credentials or local paths.

## Prepared deployment

Source `ffa8dad15380b6fb9a01e370399a20ebcd8cbc69` used the pinned 9Router 0.5.75
runtime and OpenCode 1.18.30. A fresh independent review verified the concrete
packet, staged sources, fixed egress, worker isolation, ownership controls and
shared ten-attempt/600-second scope before its one start. The selected host was a
local isolated deployment. The user's existing host supplied a private, access-only
copy of two configured Codex connections; it is neither a product dependency nor a
required installation host. Source configuration and service were left unchanged.

The copied account bindings matched decoded token claims and conservative expiry
times. This was sufficient for the approved candidate trial, but local decoding is
not signature verification or proof of separate billing. No refresh credentials,
ID tokens, paid fallback or other providers were included. Actual successful
provider acceptance and the distinct-subscription continuity criterion remain
unproved.

## Observed failure

The scope began at 06:00:01.888 UTC. The first physical request, through the approved
Astra/xhigh route, reached the response-validation wrapper at approximately
06:00:08 UTC. It raised `unsupported_provider_response`. That branch establishes
that the received response had an HTTP success status, but its content type did
not contain the expected SSE marker or its body was absent. The original numeric
status, content type and body were not retained. The logged 502 was generated
locally; it is not evidence that the provider returned 502.

The receipt records one unknown original with local reason `transport_error`, no
output digest and `quiescent: false`. Durable scope spending is one. The stock
router attempted its error/fallback path, but authority fencing prevented another
physical send. The suite detected the ended unknown original, stopped later phases
and retained the failure. The boundary journal closed; the final scope snapshot
still says active. Neither that snapshot nor elapsed deadline grants another run.

The gateway and relay exited at 06:00:08.844 and 06:00:08.926 UTC respectively.
There were no durable candidate artifacts or acknowledgement decisions. The
worker's stopped process exit is not evidence of a completed model task. The
original one-use marker, scope debit and unknown receipt remain preserved.

## Remaining work

[Issue #86](https://github.com/korallis/letmecook/issues/86) tracks bounded original
HTTP diagnostics and offline regression evidence. This fixes a diagnosability gap;
it cannot reconstruct the lost response, settle the unknown operation or authorise
a retry. Live acceptance for issues #6, #7 and #8 remains pending, as do registered
real baseline observations for #1. The #9 foundation decision remains gated. No
output acceptance, model usage or operator-effort measurement is inferred from this
failed public-fixture trial.
