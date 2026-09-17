# Per-attempt inference boundary

`Start(ctx, Scope, ReceiptSink)` creates a supervisor-owned HTTP listener at
`127.0.0.1:0`. `Addr()` is a host:port; the adapter receives `http://` plus that
address, a fresh scoped token and the approved protocol/model tuple. The worker
never receives a gateway credential, credential reference, gateway origin or CA
bundle. This package does not select accounts, route between models, estimate
quota, retry requests or grant execution authority.

This is provisional M1 infrastructure. The only operator-authorized execution
profile on this Mac is `macos-sandbox-exec-dev`, with `qualification: development`
and `supported: false`, refused by default. A loopback proxy is not OS isolation.
A caller must separately fence the worker, close admission and establish durable
boundary quiescence before finalizing an attempt or releasing its reservation.

## Operator configuration

`LoadGatewayConfig(path)` accepts closed, bounded `gateway-config-v1` JSON:

```json
{
  "version": "gateway-config-v1",
  "gateway_id": "operator-gateway",
  "base_url": "https://gateway.example",
  "credential_ref": {"kind": "file", "path": "/protected/gateway-credential"},
  "ca_bundle": "/protected/optional-gateway-ca.pem",
  "protocols": ["chat_completions", "responses", "messages"],
  "models": ["approved-model"]
}
```

The example contains no deployed endpoint or secret. `ca_bundle` is optional and
must be an absolute PEM file path when supplied. The boundary adds those roots to
system trust; hostname and certificate verification always remain enabled. This
supports an operator's local HTTPS mock gateway without an insecure-skip switch.
Loading configuration reads neither the credential nor the CA file. Its canonical
profile digest includes the CA path, protocol set, model set and credential
reference. Configuration/evidence revision remains the caller's responsibility.

At `Start`, the credential reference must identify a regular non-symlink file with
mode exactly 0600. It is opened once, identity/mode rechecked, bounded to 16 KiB,
trimmed of surrounding whitespace and retained only in supervisor memory. Invalid
references and transport errors yield fixed, redacted errors. There is no
credential environment variable, CLI argument, config value or serialized field.
The attempt token must be at least 32 bytes and different from the credential.

## Routes and validation

Every route requires exactly one `Authorization: Bearer <attempt-token>` or
`x-api-key: <attempt-token>` header, never both. Authentication hashes each
candidate and compares fixed-size digests with `subtle.ConstantTimeCompare`.

- `POST /v1/chat/completions`, when `chat_completions` is scoped.
- `POST /v1/responses`, when `responses` is scoped.
- `POST /v1/messages`, when `messages` is scoped.
- `GET /v1/models`, a local list containing only `Scope.Models`.

No management, alternate discovery, query parameters, escaped paths, redirects,
client compression or additional methods are admitted. Other requests receive
403 `boundary_refused`; oversize bodies receive 413 `request_too_large`; exhausting
the durable request-reservation budget receives 429 `budget_exhausted`.

The scope must be a subset of the gateway's canonical protocol/model allowlists,
with a current-shaped execution identity and positive local bounds. Request JSON
is UTF-8, duplicate-free, nesting/size bounded and closed at the protocol envelope;
provider tool arguments, schemas and nullable fields remain data. The owned
`protocoljson` validator preserves structural `closedjson` checks while allowing
null data at any depth; control-wire decoding is unchanged. `model`, `stream` and
`background` are separately checked, so null never weakens routing authority.
The model is an exact allowlist member and
the request bytes are not rewritten. Responses `background:true` is refused:
acknowledging asynchronous upstream work is not synchronous completion.

OpenCode 1.18.31 issues at least two gateway requests per observed run (the first
without tools). Its attempt `Scope.Limits.Requests` must therefore be at least 2;
this is an integration allowance, not permission for the boundary to raise caps.

Forwarding uses a fresh header set: JSON/stream negotiation, a fixed user agent,
and only the injected gateway credential (Bearer for OpenAI-compatible protocols,
`x-api-key` plus the pinned API version for Messages). Worker auth, forwarding,
proxy, cookie and provider-routing headers do not cross. The HTTP transport has
`Proxy=nil`, compression disabled, verified HTTPS and no redirect following.
Only the response content type and `Cache-Control: no-store` are returned from the
boundary's header policy. Upstream status and body bytes pass through unchanged,
including gateway 5xx responses.

## Reservations, receipts and unknown work

`ReceiptSink.Reserve` must commit durably before returning success. It is called
before any upstream send; `Complete` must commit before returning success and is
called only after the bounded upstream body has reached a terminal result. The
boundary exposes no acknowledgement that bypasses the sink's durability contract.
A failed/ambiguous reserve or completion keeps the reservation unresolved.

JSON usage and SSE usage are observed without rewriting bytes. Recognized forms
are Chat `usage`, Responses terminal `response.*.response.usage`, and Messages
`message_start.message.usage` plus cumulative `message_delta.usage`.
Anthropic cache-read/cache-creation input counts are included in prompt totals.
Responses streams require a matching terminal response status; Messages streams
require `message_stop`; Chat streams require `[DONE]`. Premature SSE EOF, truncated
bodies, response-byte overflow, async status and transport timeout remain unknown.
Response-byte overflow or a non-EOF read failure aborts the local HTTP response
rather than returning a clean-looking truncated body. Nullable unrelated fields
do not erase observed usage or terminal status. Null/malformed usage in a known
terminal Responses result is terminal with unknown usage, not unknown remote work.

`Receipt.Source == "gateway_usage"` means **both** prompt and completion token
counts were observed. `gateway_usage_unknown` means absent, incomplete or rejected
usage; its numeric zero placeholders are **not** a claim of zero consumption.
Receipts contain request UUID, protocol/model, status, byte counts and timestamps,
not prompts, response text, credentials, tokens, paths, accounts or endpoint URLs.
Usage is observational, not billing or provider-output enforcement.

Worker disconnect does not cancel upstream work. The boundary drains within its
original `RequestTimeout`, stops trying to write to a dropped/slow worker (a
per-chunk write deadline of min(5 seconds, RequestTimeout)), and
completes the receipt only if a terminal result is actually observed. Otherwise
`Reservations != TerminalReceipts`, so `Quiescent` stays false even after
`InFlight` becomes zero. A completion-journal call has its own bounded timeout so
an upstream finishing at its deadline still has a chance to become durable.

`Close(ctx)` stops admission immediately and drains admitted requests. A Close
deadline returns the current non-quiescent state, never fabricated terminal
receipts; bounded upstream drains may finish later. Call Close again to observe
that later state. The caller must persist reservations/receipts and retain unknown
remote-work state across supervisor restart; this in-memory boundary is not a
restart journal or a remote-cancellation protocol.

## Evidence and limits

`go test -race -count=1 ./internal/inference ./internal/harness/opencode` covers
real TLS gateways, protocol JSON/SSE, exact streaming bytes, header stripping,
request-cap races, reservation-before-send, failed journal writes, broken client
drains, hangs, early Close deadlines, malformed/oversize input, redirects, proxy
configuration, mode/symlink refusal, and untrusted/wrong-host CA failures. Adapter
coverage includes recursive credential/token absence checks in worker roots and
fsync'd receipt/event journals.

The live smoke test is intentionally separate and performs one inference, not
repository execution:

```sh
GAFFER_LIVE_GATEWAY=1 GAFFER_GATEWAY_CONFIG=/protected/gateway.json \
  go test -tags live -count=1 -run '^TestLiveGatewayBoundary$' -v ./internal/inference
```

Without the explicit environment gate it skips. It prints status/counts/usage
source only. One authorized run on 17 September 2026 returned status 200, one
reservation, one terminal receipt and quiescence; usage was classified
`gateway_usage_unknown`, not asserted zero. That historical run preceded the
nullable-data parsing correction; it was not repeated. Local TLS JSON/SSE tests
now cover provider null fields alongside nonzero usage for all three protocols.
This is not conformance evidence for
provider fallback, cost, cancellation or supported unattended execution.

## Gateway contract appendix placement

The base branch did not yet contain `docs/contracts/model-gateway.md` (PR #113).
The implementation notes above are the intended appendix once that contract
merges. They complement `gateway-local-bounds-v1` / `billing: gateway-managed`:
Gaffer enforces local request/byte/time scope; no provider-cost or provider-output
guarantee and no second account router are added. Move/link this appendix after
#113 rather than inventing a second product gateway contract here.
