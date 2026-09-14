# Proposed 9Router conformance corpus

All inputs are **synthetic**. `UPSTREAM_SECRET_SENTINEL` and similar strings are
deliberately fake leak markers, not credentials. No file is a deployed-router
record. The normative proposal and source evidence are in
[the contract](../../../docs/contracts/9router.md).

| File | Downstream use |
| --- | --- |
| `status.schema.json` | Closed outgoing status v1; validate after allowlist projection |
| `input-profiles.json` | Explicit source/artifact identity and scalar allowlists for each inspected input profile |
| `status-cases.json` | Source payload/context and exact expected projection; malformed, hostile, stale and missing observations |
| `examples.json` | Strict/default policy and bound profile identity, three protocol requests, tool continuation and strict-output requirement |
| `native-limits-example.json` | Explicit native local-limits logical policy/grant example; unavailable provider bounds and synthetic references; not a runtime API or live grant |
| `limits-profile.schema.json` | Discriminated logical profile shape used only to validate contract examples |
| `failure-cases.json` | Required fault/race experiments and expected decisions, including zero unauthorized forwards/tool replays |
| `streams/*.sse` | Byte fixtures for success, fragmented tool output, error and deceptive completion |

Run with Node.js 24 or newer:

```sh
npm --prefix tests/fixtures/9router ci
npm --prefix tests/fixtures/9router test
```

The checker validates the JSON schema, all expected status objects, negative
mutations of those objects, safe expected references, timestamps and referenced
stream files. It also checks both limits-profile examples, their finite local
controls and matching grant identities, and rejects ambiguous/missing cap or native
authorization-reference shapes. Schema-valid references are not evidence of actual
authority or conformance. It does **not** run an adapter, authorize a request or
claim live failure-path behavior.
Issues #3–#8 must execute the specified cases against their real boundary/adapter,
use a deterministic clock for freshness tests, and save redacted actual results.

Input profiles are separate: `rickicode-0.3.98-69724d8` covers the historical
canonical fields, while `decolua-0.5.75-17c4cc7` covers the selected release's
legacy fields. Its `active` never becomes eligible. The optional negative-state
extension has separate fixtures; a declared disabled/unknown-only implementation
may leave negative observations unknown. Changing profiles never widens readiness.
The independently observed deployment metadata lives in the contract directory;
the synthetic `examples.json` deployment placeholders are not that live record.

Unknown source fields are ignored. Unknown/malformed values in inspected scalar
fields invalidate their observation, while a malformed top-level response becomes
a minimal unknown projection. Raw upstream labels/IDs must match a separately
supplied trusted mapping before a local reference can be emitted. This corpus uses
`raw-connection-a` and `raw-connection-b` mapped to `subscription_a` and
`subscription_b` under local `provider_primary`; those IDs are illustrative only.
An incomplete upstream response must not make a missing connection disappear or
make a discovered route ready. The adapter emits the configured missing connection
with unknown state.

Schema validity is necessary, not sufficient: timestamps must be consistent with
the observation clock and source, a ready result needs deployment/policy/capability
evidence, and semantic correctness must be checked against the expected pair.
Each failure case declares its layer and assertions so a mock result cannot be
mistaken for a provider or containment proof.

The [#81 amendment](../../../docs/contracts/native-subscription-limits.md) approves
the native option but leaves current experimental runtimes unchanged. Existing
grants retain strict semantics; there is no implicit migration. The original
`request-bounds` case is explicitly strict. Added native refusal, authority,
fallback and quiescence cases are requirements for future execution, not recorded
passes. The native request illustration needs a separately reviewed compatible
harness/profile before use. Evaluation allowances are documented separately from
these synthetic examples and from public product defaults.
