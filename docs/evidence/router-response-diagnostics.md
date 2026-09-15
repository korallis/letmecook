# Original HTTP response diagnostics (#86)

The pinned router now commits a closed response observation before response
interpretation, bound to the exact request ID and physical ordinal. The observation
contains only original numeric HTTP status, media-type category and Fetch body-stream
presence. It never contains raw headers, body bytes, status text, credentials,
account identities or reflected header parameters. The diagnostic category does
not change the existing MIME acceptance or original-terminal rules.

The new table is additive. Old operation rows stay intact, and an absent observation
is exposed as `null`; legacy serialized receipts may still omit the field. There
is no inferred status, MIME or body presence for the failed historical live attempt.
That attempt and its unknown-work fence remain unchanged.

## Offline observations

The [machine-readable report](router-response-diagnostics-run.json) retains bounded
results from actual public 9Router source
`17c4cc76877bd1755030a8414f8d0083f48dcccf`, using its real handler, executor, HTTP
transport and SQLite adapter inside the locked dependency image
`sha256:f95ae5b2218838d3da613273705b7914a149593c400a33087e9c383c2e12650a`.
The source loader verified the pin before evaluating upstream code. All HTTP
responses, identities and credentials were synthetic, inside network-none
containers; no private state or provider endpoint was accessed.

| Synthetic original response | Retained observation | Qualification |
| --- | --- | --- |
| Unsupported JSON success | 202 / json / body present | Unknown |
| HTML success | 200 / html / body present | Unknown |
| No Content-Type | 200 / missing / body present | Unknown |
| Bodyless HTTP 204 with SSE MIME | 204 / sse / no body stream | Unknown |
| Empty SSE HTTP 200 | 200 / sse / body stream present | Unknown |
| Malformed JSON rejection | 418 / json / body present | Unknown |
| JSON rejection without the required error shape | 422 / json / body present | Unknown |
| Empty rejection body | 503 / json / body stream present | Unknown |
| Valid original SSE | 200 / sse / body present | Completed, released after durable success |
| Observation insert failure | Unavailable | Unknown; physical response disposed |
| Failure after insert inside the SQLite transaction | Unavailable after rollback | Unknown; physical response disposed |
| Observation insert and local-stop write both fail | Unavailable | Unknown; physical response disposed |
| Synthetic pre-observation schema upgraded | Unavailable before and after restart | Unknown |

All thirteen cases made one physical send and retained one scope debit. Every
unknown case withheld output, kept its reservation, and denied another physical
send and policy replacement. Each case then closed the original owner and reopened
the same database in a fresh process. The new boot remained closed, preserved the
scope debit/start/deadline and original operation identity, and retained committed
observations. Unknown operations gained only the existing `crash_unknown` local-stop
marker. Missing or rolled-back observations stayed unavailable. The legacy fixture
uses a test-only overlay without observation capture/table creation, then restarts
with the current overlay; it never opens the historical live database.

The existing aggregate account-fallback case also retained separate observations
for its first request's two physical ordinals: original 429/JSON rejection followed
by 200/SSE completion. Diagnostics did not spend an additional allowance or change
fallback authority.

## Verification and reproduction

Run from a checkout of this PR with Node 24 and the public pinned router source:

```sh
GAFFER_ROUTER_SOURCE=/absolute/public/router node experiments/native-evaluation/run-router.ts /tmp/response-diagnostics-evidence
GAFFER_ROUTER_SOURCE=/absolute/public/router node experiments/native-evaluation/run-router.ts /tmp/response-diagnostics-legacy legacy
node --test --test-concurrency=1 experiments/router-authority-extension/responses-terminal.test.mjs experiments/router-authority-extension/chat-terminal.test.mjs experiments/router-authority-extension/response-observation.test.mjs
```

The first runner passed all 51 native cases, including the 13 new diagnostic cases;
container exit, OOM state, runtime/isolation readback and cleanup checks passed.
The second runner passed all 41 existing router/transport/crash regressions,
including its intentional SIGKILL/restart and source-tampering refusal checks.
Host checks passed: 56 inference-boundary tests, 20 native/scope/deployment tests,
3 bridge lifecycle tests, 52 original-observer/diagnostic tests, the three affected
TypeScript package checks, extension syntax checks, and `git diff --check`.
Docs build/check passed and left the generated reader unchanged. The deployment's
recursive source-manifest selection includes the new observation module; its
digest is retained in the report. The diagnostic cases and observer tests are
included in the existing CI workflow.

No new live acceptance is claimed. These observations do not resolve the original
response-shape mismatch's cause, qualify the failed live attempt, establish remote
quiescence, or complete issues #1, #6, #7, #8 or the #9 foundation decision.
