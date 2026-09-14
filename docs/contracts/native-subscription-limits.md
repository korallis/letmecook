# Native subscription local limits

Operator-approved contract amendment for
[#81](https://github.com/korallis/letmecook/issues/81), **14 September 2026**.
The operator approved the optional profile, bounded live trials after review of
the controls, and the prepared private baseline tasks/data policy. The product
choice is resolved; implementation and live evidence remain outstanding. This
document performs no deployment, credential operation or inference.

This extends the [9Router integration contract](9router.md) and
[task-routing eligibility](task-routing.md). Public installations remain
host/provider/model configurable. 9Router exclusively owns subscriptions,
provider credentials, account selection, refresh and request fallback. Gaffer
selects an eligible named route and does not add an account or quota router.

## Two explicit limits profiles

| Requirement | `strict-provider-output-v1` | `native-subscription-local-v1` |
| --- | --- | --- |
| Selection | Default; existing grants retain this interpretation | Explicit operator authorization in the grant or standing policy |
| Local controls | Finite enforced request/response bytes, concurrency, request/subattempt/retry counts and time limits | Same mandatory controls |
| Provider output-token cap | Positive finite integer cap, preserved and enforced on every approved path | Unavailable; reject a task or grant requiring a hard provider output bound |
| Provider monetary cap | Requires separate verified maximum-cost evidence when mandatory | Unavailable; reject a task or grant requiring a hard monetary cap |
| Billing envelope | Every reachable connection/fallback must fit reviewed policy | Every reachable connection/fallback must be reviewed as subscription billing; paid or unclassified overflow rejects |
| Remote uncertainty | Retain reservation; no replacement or drain completion until quiescence is proved | Identical; local limits do not prove remote stop |

The pinned native Codex experiment observed removal of configured output-token
fields before the original backend send. The
[retained evidence](../evidence/router-authority-extension.md#remaining-gates)
is about that inspected path, not all present or future subscription backends.
The new option represents this missing capability honestly; it neither restores
that cap nor certifies the existing experimental harness/request profile.

Use a discriminated union rather than an optional numeric cap whose absence could
silently permit a different profile. This is a **logical contract shape**, not a
runtime API or a migration of existing experimental schemas. Legacy grants without
a stored discriminator retain strict cap semantics; new grants encode it explicitly:

```ts
type LimitsProfile =
  | {
      kind: "strict-provider-output-v1";
      providerOutput: { requirement: "hard"; maxTokens: number };
    }
  | {
      kind: "native-subscription-local-v1";
      providerOutput: { requirement: "not_required"; capability: "unavailable" };
      providerMonetaryCap: { requirement: "not_required"; capability: "unavailable" };
      subscriptionEnvelopeRef: string;
      authorizationPolicyRef: string;
    };
```

Both variants carry all required local limits and explicit granted authority.
The policy/grant identity binds the exact named route, complete graph fingerprint,
router build, protocol/harness/settings profile, limits profile, applicable
capability evidence and authorization policy. Native authorization must resolve
to the operator's actual grant or standing policy; an opaque reference alone is
not proof of authority. Every fallback must satisfy protocol, tools, exact settings,
modalities, context, data and isolation requirements. A supported first model
cannot compensate for an unsupported fallback. Desired Astra/Sol/Terra names
remain preferences with individual evidence gaps.

Changing profile needs explicit authorization, a new policy/grant identity and the
existing fenced freeze/drain procedure. No automatic downgrade, silent migration,
assessor-authorized widening or request-time profile substitution is allowed.
A harness requiring an unsupported token cap stays ineligible until its native
request semantics are reviewed. Visible text counts, prompt instructions and
reasoning/verbosity preferences never stand in for hard provider limits. If an
optional output-length preference is added later, keep it outside hard-limit fields.

The [strict example](../../tests/fixtures/9router/examples.json) retains its 4,096
provider-token cap. The [native example](../../tests/fixtures/9router/native-limits-example.json)
uses concrete finite local controls and explicit unavailable provider bounds. Its
identities and evidence references are synthetic placeholders, not deployment
attestations or a live grant. Neither example fixes native production defaults.

## Stop, completion and durability

Durable admission must precede router work and bind attempt, grant, lease/fence,
policy identity and request ID. Every inference, retry, fallback and refresh
operation remains inside the approved graph and tracked authority. Stop,
revocation, lease expiry, fence change and deadline prevent new admissions and
subattempts, stop forwarding output and cancel local transport.

Unknown remote work retains its reservation as `cancelled_unknown`/unreconciled.
Process exit, local time or byte limits, transport EOF and a zero pending count
are not remote cancellation acknowledgement, quiescence or a refund. No transparent
retry, route swap, replacement under that reservation or policy-drain completion
is allowed without sufficient evidence. Crash or failed mutation recovers closed;
the unresolved state can persist indefinitely.

Absence of a configurable token cap does not preclude future trustworthy evidence
that an individual original provider request ended. A verified original terminal
or explicit remote cancellation acknowledgement may reconcile that request without
establishing token/cost bounds. A model maximum does not prove that request ended.
Positive quiescence support must be tested on the deployed path. Quiescent
failed/incomplete requests remain stopped failures. Tools and final output still
need complete schema-valid results, trustworthy successful original terminals,
scope/fence checks and durable decisions; tools also need separate execution
authority and durable action identity. Preserve acknowledged tool results and
provisional artifacts.

## Operator-facing capability wording

Display before authorization and retain with run details, using the configured
values and **verified** control status:

> **Native subscription — local limits**
>
> Local time, request/response size, concurrency and request/retry limits: enforced
> at the displayed values.
>
> Provider output-token limit: unavailable.
>
> Provider monetary limit: unavailable.
>
> If remote stop cannot be verified, the request remains unresolved and replacement
> work is blocked.

Unverified controls must instead show “Route conformance not verified” and block
admission. Exclusions name the actual requirement: “This task requires a hard
provider output limit” or “This task requires a verified monetary cap.” Show
individual capability gaps, not a single ready badge. Do not label this profile
“unlimited,” “free,” “safe to retry” or “bounded cost.” Keep unknown usage and cost
distinct from measured zero. UI wording itself grants no authority.

## Approved evaluation envelope

This is a scoped operator evaluation decision, **not a public product default**.
Use existing Codex subscriptions, initially Astra with exact `xhigh` semantics
verified on the selected path. A reusable isolated local router copy is allowed;
its setup must use reviewed controls and router-owned configuration/credential
handling. No paid API fallback or direct-provider bypass is authorized. All
reachable connections and fallback models still require individual classification
and compatibility evidence. Other installations choose their own hosts, providers,
models and limits.

| Phase | Approved maximum |
| --- | --- |
| Initial conformance checks | 10 inference attempts and 10 minutes **in aggregate**, across all initial checks |
| Each subsequently approved baseline case | 32 inference attempts and 15 minutes per case; one case at a time |

Stop when either the count or time ceiling is reached. Count every physical
inference attempt, including failed/discarded attempts and all router/harness
retries and model/account fallback; coordinator, planner and reviewer inference
also share the relevant envelope. No request-layer counter may hide subattempts.
Refresh is not inference, but its retries and time remain finite, within the
tracked authority and overall deadline. These ceilings do not authorize replay
of uncertain remote work. Retain unresolved outcomes and reservations; local
expiry cannot reset allowance or begin a replacement case while remote work is
unreconciled. A phase/case allowance is not renewed by a process restart or a
repeated check. Apply smaller local or existing task limits as well.

Real calls wait for reviewed implementation, verified deployment controls and the
applicable authorized data policy. Review must cover the selected build and exact
full route/profile identity, authenticated private ingress, exclusive router
process/storage ownership, graph writer fencing, worker isolation and finite local
controls before admitting live inference. Live observations then establish only
the capabilities actually tested; the implementation review is not live evidence.

The operator approved the prepared private baseline tasks and permitted data use
under a separate private record. Public artifacts retain only opaque authorized
case/source references and redacted results. Do not publish private source or
repository identities, paths, prompts, account identities, personal hostnames or
credentials. Approval of intended outcomes is not acceptance of finished outputs,
permission for unrelated data use, or measured operator effort. Capture actual
start/stop/intervention records when measuring human minutes; never invent them.

## Required evidence and remaining work

Before claiming live eligibility, implementation must demonstrate:

1. Strict/default missing or invalid caps reject and cannot downgrade. Native
   grants without explicit authority reject; hard provider output/cost tasks,
   paid/unclassified fallback and incompatible harness/settings reject before
   inference. Enforce finite bytes, concurrency, request/subattempt/retry and time
   limits, including failure paths, without disguising unsupported cap fields.
2. Full admission identity, graph freeze/writer fencing and all fallback/refresh
   operations retain scoped authority. Complete tool/result continuation and a
   successful original terminal precede release. Test UTF-8/SSE fragmentation,
   malformed/incomplete/failed/refusal/length/contradictory terminals and fabricated
   translation success.
3. Stop before headers, midstream, during retry and refresh permits no later
   subattempt. Durable crash recovery, missing/unknown receipts, stale fences and
   failed writes recover closed with reservations retained. Verify positive
   quiescence only where trustworthy remote evidence exists.
4. UI/exported evidence separates local limits, unavailable provider bounds,
   unknown usage, stopped failures, unresolved cancellation, synthetic and actual
   live observations. Contract-example validation is not runtime conformance.

Runtime implementation of this profile remains work after #81. Existing accepted
[#77](https://github.com/korallis/letmecook/issues/77) authority and
[#79](https://github.com/korallis/letmecook/issues/79) bridge evidence stays synthetic;
its historical caps, failures and `liveAdmission:false` are unchanged. The current
[#6](https://github.com/korallis/letmecook/issues/6)/[#7](https://github.com/korallis/letmecook/issues/7)
draft live gates remain, followed by actual baseline results and two-subscription
continuity in [#8](https://github.com/korallis/letmecook/issues/8), and the foundation
decision in [#9](https://github.com/korallis/letmecook/issues/9). No application core,
native conformance, completed output acceptance or human-minute result is delivered
by this contract approval. Final-head independent review and observed CI checks
gate delivery of the contract PR; live execution has the separate controls above.

## Shared implementation evidence

The [#83 native evaluation experiment](../evidence/native-evaluation.md) implements
this optional profile in the existing M0 boundary and router authority. Its
synthetic measurements and deployable controls remain distinct from the concrete
deployment review and live #6/#7 results required by this contract.
