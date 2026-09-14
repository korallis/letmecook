# Attempt-scoped inference boundary experiment

For [issue #3](https://github.com/korallis/letmecook/issues/3), implementing the
accepted [9Router contract](../contracts/9router.md) as an isolated M0 experiment.
The [recorded run](inference-boundary-run.json) contains actual automated results,
runtime identity and source digests. All upstream responses, credentials and policy
authority are synthetic. No installed router, provider account or live model was
called. No application core, task scheduler or unattended runner is introduced.

## Run locally

Use Node 24+ on a host with Unix-domain sockets and filesystem file/directory
sync support (observed locally on macOS; CI also tests Linux). No Docker, private
hostname, network, provider credential or existing router setup is needed.

```sh
npm ci --prefix experiments/inference-boundary
npm --prefix experiments/inference-boundary run check
npm --prefix experiments/inference-boundary test
npm --prefix experiments/inference-boundary run demo
npm --prefix experiments/inference-boundary run prove
```

`demo` runs one scoped synthetic planner request and reports its safe attribution;
it never executes the returned tool. `prove` reruns the failure tests and writes a
redacted record. Supply another output path with `node
experiments/inference-boundary/prove.ts /absolute/path/result.json`. Only the selected
output artifact is retained; fixtures remove their task-owned sockets/state.

## Exactly supported profile

`chat-text-tools-v1` permits only `POST /v1/chat/completions`, `stream:true`, the
grant's exact `model`, and integer `max_completion_tokens`. The tested envelope is
text messages plus the fixed `read_file` function schema for `fixture.txt`, including
assistant/tool-result continuation. Optional `tool_choice` is `auto`, `required` or
`none` with the approved tools. Up to eight complete function calls can be assembled
independently in a response. Tools are buffered until valid arguments, an explicit
`tool_calls` finish reason, `[DONE]` and clean EOF; text streams incrementally.
This experiment never dispatches tools or grants filesystem/execution authority.

Messages, Responses, `max_tokens`, `stream_options`, strict structured output,
reasoning settings, media/remote resources, hosted tools and unknown fields are
denied. In particular, OpenCode's OpenAI-compatible `max_tokens`/usage-stream request
shape is **not compatible with this profile**; a negative test records that gap.
Issue #6 must select or add an explicitly tested harness profile rather than assume
that “Chat Completions” implies matching every request shape. No strict-schema or
live translation-completion capability is claimed.

The parser rejects duplicate keys (including escaped duplicates), non-object
envelopes, invalid syntax/UTF-8, nonfinite numbers and nesting beyond 32 levels.
HTTP methods, raw aliases, management paths, queries, encoding tricks, absolute
URLs, CONNECT and upgrade requests do not create upstream requests. Compressed or
non-JSON bodies are denied. Caller cookies, forwarding headers, alternate API keys
and arbitrary headers are rejected. The boundary constructs the upstream method,
path, authorization and attribution headers; there is no redirect following or
upstream-origin selection from caller input.

## Authority and bounds

The trusted host API issues a random opaque credential once per attempt. Its stored
binding contains task/attempt/grant/lease identity, fence, role, router/route,
policy revision/epoch and credential/lease expiry. The token is stored as a hash
and never written to the journal or audit. Worker, planner and reviewer identities
use the same admission checks. Revocation/fence replacement cancels active and
body-reading requests; a final check after durable admission prevents a stop during
journal I/O from initiating upstream work. A new fence requires a new attempt;
old credentials never acquire the new policy epoch.

Each grant inherits the immutable policy's bounds, with its own attempt count,
concurrency reservation and elapsed-time budget. The default fixture uses:

| Bound | Default |
| --- | --- |
| Request / raw and projected response | 64 KiB each |
| Output limit | At most 256 tokens |
| Concurrent requests / admitted requests | 1 / 8 per attempt |
| Total request / first semantic output / semantic idle | 2 s / 1 s / 500 ms |
| Attempt duration | 10 s |
| Process-wide admitted reservations / issued grants | 16 / 64 |

Tokens expire at the earlier credential, lease or attempt deadline. SSE comments
and empty role metadata do not extend semantic deadlines. The bounded profile
validator rejects excessive/missing limits and unsupported policy graphs. Socket
connections and headers are separately capped. Request/response reads and
downstream backpressure remain under the request deadline. Each admitted forward
creates exactly one upstream HTTP request. There is no account selection, provider
retry, credential refresh, quota ledger or hard-money guarantee.

## Frozen policy, cancellation and recovery

`PolicyGate` serializes admission and the sole trusted fixture configuration writer.
Before forwarding, it writes an admission reservation via temporary-file sync,
atomic rename and directory sync. A policy change first persists `draining`, denies
new admission, waits for active streams and asks the authority about all uncertain
work. Only verified quiescence permits mutation, exact policy readback, fingerprint
and epoch update. The router/model/billing graph stays frozen while old requests
run. Narrowing changes use the same rule. Readback, persistence or authority failure
keeps admission closed. The experiment policy accepts only the declared synthetic
graph; fusion, capability adapters, remote-resource and helper paths are rejected.

Socket abort, timeout and worker disconnect propagate to the upstream transport but
do **not** prove provider cancellation. `cancelled_unknown` reservations remain
until the trusted authority proves quiescence. Tests deliberately keep hidden
fixture work alive after disconnect: replacement requests remain constrained and
the writer cannot drain until that work actually stops. Successful protocol EOF
also uses authority evidence to release the reservation. Audit records report
local outcome, scope and a separate verified/unknown quiescence result; they never
infer provider/model/account/usage attribution. Transport loss before headers keeps
the uncertain reservation and never triggers an automatic retry or refund.

The private journal holds only reviewed synthetic policy, safe identities, request
counts and reservations. A single-owner lock prevents a second supervisor.
Restart always begins closed and requires the trusted authority to reconcile any
retained work; attempt credentials do not survive. A process-crash test records an
active reservation and stale lock before abrupt exit. For manual recovery, first
verify that the owning supervisor is dead and no competing supervisor exists,
then remove **only that experiment directory's** `owner.lock`. Reopening still
requires authoritative router/provider quiescence and a frozen policy; never edit
away uncertain reservations. Corrupt state fails closed. This small admission
journal is not the durable application core or a router deployment recovery tool.

The fixture authority is honest, sole-writer test code outside the worker endpoint.
It cannot certify the real 9Router dashboard/import/database/CLI writers or hidden
provider work. A live adapter must fence every such writer and prove quiescence;
otherwise it must stay closed. No HTTP management API or live adapter is supplied.
The combined harness/isolation work proves network bypass denial separately.

## Evidence and deliberate limits

Tests cover credential/route/path/feature/byte/token denials with zero upstream
requests; text and complete tool continuation; every byte split of UTF-8/SSE;
multiple tool IDs; malformed/partial streams; response caps; concurrency/count/time
bounds; pre-forward and midstream stop; hidden-work cancellation; drain races;
failed readback; second-writer refusal; corrupt state and abrupt-exit recovery.
The accepted Chat corpus's complete, partial, synthetic-DONE and post-text error
files are consumed directly. Messages/Responses remain unsupported and are denied.

Synthetic provider secrets are injected into upstream headers, cookies, error
bodies and nested metadata. The boundary reconstructs permitted text/tool chunks
with local request/tool IDs and emits enumerated errors, excluding those secret
fields from caller responses, audit and proof artifacts. It does not copy upstream
errors, headers or metadata into logs. Model-generated text and tool arguments are
application content; this is **not** a universal detector for secrets that a model
deliberately writes into otherwise permitted text. Provider credentials stay in the
router trust boundary; they are never supplied to this forwarding component.

The synthetic router key tests boundary authentication and header replacement;
it is not evidence that the selected live router rejects absent/invalid/disabled
keys. Live auth, hidden fallback, trustworthy translation terminals, upstream
cancellation, actual writer fencing and combined OS/harness compatibility remain
deployment conformance gates. `unattendedSupported` stays false.
