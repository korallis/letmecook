# Model gateway contract v1

**Status:** proposed generic product requirement · **Current selection:** CLIProxyAPI ·
**Updated:** 17 September 2026

This contract defines Gaffer's model-access boundary. A gateway selection is
operator configuration, not a product dependency. The configured gateway must expose
an operator-selected HTTPS base URL and serve at least one supported protocol. Gaffer
never stores a gateway credential value or provider credential.

## Configuration

```text
model_gateway:
  gateway_id: stable local identifier
  base_url: operator-selected HTTPS base URL
  credential_ref: protected file path or secret reference; never a value
  protocols[]: chat_completions | responses | messages
  models[]: exact permitted gateway model IDs or aliases
  status_endpoint?: optional operator-configured read-only endpoint
```

`protocols` and `models` are non-empty allowlists. `chat_completions`, `responses`
and `messages` mean OpenAI Chat Completions, OpenAI Responses and Anthropic Messages.
Discovery can inform setup but does not add an allowed model or prove readiness. The
configured base URL, credential reference and optional status endpoint are trusted
operator inputs; request content,
repository text and model output cannot change them. Do not put endpoint-specific
secrets, credential values or maintainer hosts in documentation, fixtures, logs,
command lines, prompts or browser payloads.

Changing the gateway, protocol set, model allowlist or credential reference
invalidates incompatible conformance evidence and requires current authority before
new attempts are admitted. Gateway-specific setup, backup and migration stay outside
Gaffer's workflow store.

## Ownership and inference boundary

The gateway exclusively owns provider credentials, accounts and subscriptions,
credential rotation, cooldown and request fallback. Gaffer never becomes a second
account router, subscription balancer, refresh service or quota ledger. Gaffer owns
task authority, gateway-model eligibility, attempt identity, execution, recovery and
bounded task-level retries.

Workers never receive the gateway credential. A trusted per-attempt inference
boundary outside the worker sandbox resolves `credential_ref`, injects the credential
upstream and exposes only the attempt's permitted protocol paths and exact model IDs
or aliases. It denies gateway management, unrestricted discovery, alternate models,
direct provider access and direct gateway access. Planner, reviewer and coordinator
calls use the same boundary with their own scoped attempt identities.

Each admitted request is bound to the current grant, attempt, lease/fence identity,
protocol, model target, limits and expiry. Gateway-owned fallback may continue the
same request only inside the operator-approved provider/model/billing envelope.
Gaffer does not try its ranked models as a second request-fallback loop. Partial
output, unknown upstream work, cancellation uncertainty and terminal gateway failure
remain task-boundary reconciliation cases; no direct-provider bypass is allowed.

## Status, usage and conformance

Gaffer consumes gateway status and usage observations where the configured gateway
exposes a safe, authenticated interface. Project only explicit allowlisted fields,
record source and freshness, and keep management credentials outside workers.
Missing status, usage, headroom, resolved-account, reset, fallback or correlation
data is `unknown`, not zero, healthy, exhausted or attributable. Gaffer does not
infer account balances, reset schedules, selected accounts or exact cost from an
absent endpoint, missing field or stale observation.

Endpoint presence and model listing do not prove streaming, tool calls, structured
output, fallback, cancellation, output limits, usage attribution or readiness.
Qualify each supported gateway/protocol/model/harness/settings path with bounded
positive and failure-path probes before relying on those capabilities.

## Current CLIProxyAPI observation

On 17 September 2026, the operator-configured CLIProxyAPI endpoint was verified to
answer:

- `POST /v1/chat/completions`
- `POST /v1/responses`
- `POST /v1/messages`
- `GET /v1/models`

The endpoint URL and credential are operator configuration and are not recorded here.
These observations establish only that the listed paths answered. Authentication
semantics, scoped model enforcement, streaming, tools, structured output, status and
usage interfaces, account/subscription behavior, rotation, cooldown, fallback,
cancellation and limits remain unverified or unknown until separately recorded.

## 9Router mapping

9Router remains one optional gateway implementation and its existing contract and
evidence remain a historical gateway-specific profile. For a 9Router deployment,
`base_url` maps to its operator-configured origin, `credential_ref` to its inference
key reference, `protocols` to the enabled compatible paths, and `models` to approved
model or combo names. Its status adapter, account/connection observations,
freeze-and-drain requirements, migration procedure and conformance corpus apply only
when the operator selects that profile. See the
[historical 9Router contract](https://github.com/korallis/letmecook/blob/main/docs/contracts/9router.md).
