# Native read-only planner integration

The real `PlannerSession` now uses a separate native Responses profile through
the shared durable 9Router boundary. The consumer receives only its scoped
inference socket and grant. It has no provider URL, credentials, account router,
receipt database, gateway control socket or execution/publication capability.
Live conformance is still outstanding; issue #7 and PR #73 remain incomplete.

The planner protocol is `planner-probe-responses-read-file-v1`, with role
`planner`, its own source/settings/schema identities and exactly one `read_file`
tool for `fixture.txt`. Worker OpenCode identity and `apply_patch` remain a
separate protocol requiring role `worker`. Both profiles can be declared in one
registry with identical deployment, authorization, connections and scope.
Serial selection increments the authority generation without resetting the
scope start, deadline or spent physical attempts.

The complete private policy participates in `input_revision`. The model sees a
small proposal-only projection. The durable gate derives assessment, read and
repair state from the exact prior accepted requests and original outputs. It
preserves encrypted reasoning, original `call_id` and argument strings, checks
the paired file-result bytes/hash, and rejects altered or invented history.
A new grant/session cannot assess an unchanged input again. A completed invalid
plan permits at most one fixed repair; final and repair requests disable tools.
Partial, failed, uncertain or unverified output cannot trigger that repair.

Limits remain one assessment, one repair, three requests, one file, 4096 read
bytes, 32768 request and response bytes and 5000 ms locally. The native request
uses exactly Astra, `xhigh`, `store:false`, streaming and encrypted reasoning
continuation. Provider output-token and monetary caps are unavailable under the
separately approved optional native subscription profile. Local byte/time limits
are not provider-generation bounds. Strict hard-cap tasks remain ineligible.

## Verified request and output behavior

The observed logical request uses the approved route alias. The actual pinned
router physical request uses `gpt-6-astra`, preserves `reasoning:{effort:"xhigh",
summary:"auto"}`, `store:false`, `include:["reasoning.encrypted_content"]`,
`tool_choice:"auto"` for discovery and `"none"` afterward. It retains the tool
schema, removes the tool's explicit `strict:false`, injects the pinned Codex
instructions and emits no output-token cap field. These are observations of
synthetic HTTP through the actual pinned modules, not a real-provider result.

Current official documentation was checked on 14 September 2026. The
[Astra model page](https://developers.openai.com/api/docs/models/gpt-6-astra)
lists `xhigh`. The [Responses reference](https://developers.openai.com/api/reference/cli/resources/responses/methods/create)
describes encrypted reasoning for stateless continuation with `store:false`.
The [function calling guide](https://developers.openai.com/api/docs/guides/function-calling)
describes `tool_choice:"none"` and explicit `strict:false`; the pinned router's
removal of that field means this probe does not claim preservation of provider
strict-mode settings. Plan schema enforcement is local Ajv plus a gateway
validator for the pinned schema and references. The
[streaming guide](https://developers.openai.com/api/docs/guides/streaming-responses)
describes typed lifecycle events; tests exercise the actual created, item,
content, argument, delta and completed sequence, fragmented UTF-8, original EOF
and receipt-to-output digest joins. Documentation does not establish deployed
subscription-route conformance.

## Evidence and acknowledgement

`native-boundary-port.ts` is the production transport implementation. Every
completion still passes shared original receipt classification, durable decision
storage and post-storage scope/request/lease fencing before the boundary releases
bytes. The consumer's completed proposal is data. A trusted supervisor separately
reads the gateway's complete `evidence(attemptId)` response and builds a closed
`candidate` envelope. The gateway checks ordered decisions, original receipt and
output digests, binding, packet, policy and current scope independently, validates
the final plan and fsyncs the immutable candidate before acknowledgement.

The named artifact is `plan-proposal.json`, with closed metadata
`{inputRevision,authority:"proposal_only"}`. It conveys no filesystem execution
authority. The acknowledgement does not grant independent acceptance,
publication or merge. This uses the same evidence/ack API introduced by PR #75;
the planner PR is manually stacked on its frozen worker branch so that helper is
owned once. PR #84 is accepted on main. After the worker PR can be merged, retarget
the planner PR to main and review the resulting final diff again.

The [compact observed evidence](evidence/native-integration-run.json) identifies
the exact consumed sources and full retained raw reports. Observed checks include:

- 112 native consumer/shared-protocol tests and 35 preserved legacy fixture tests.
- 16 actual pinned-router native planner cases: direct/read/final, one repair,
  blank and invalid plans, prohibited worker/final/repair calls, role reload,
  receipt/decision write failures, post-durability scope/request/lease expiry and
  unknown original work. Failed release yields no read, proposal or repair.
- The production gateway with a separately staged 128 MiB planner: no direct
  TCP, management, shared state/control/source access or writable snapshot;
  complete original receipts precede delivery; eleven supervisor tamper cases
  and six invalid candidate submissions are denied; idempotent acknowledgement
  and the full immutable proposal record survive gateway shutdown.
- Serial planner-to-worker profile selection keeps the shared scope and fences
  the previous grant. This selection proof does not run the worker binary;
  the worker's independent adapter suite provides its execution evidence.
- All 43 preserved Chat/translated-native router cases, shared boundary/lifecycle
  tests, worker native adapter tests and native authority tests pass. The
  translated-native Chat cases remain diagnostics with their original settings.

## Reproduce the offline checks

Use Node 24, the public router source at
`17c4cc76877bd1755030a8414f8d0083f48dcccf`, Docker context `desktop-linux` and cached
`gaffer-router-extension-deps:0.5.75-locked` image
`sha256:f95ae5b2218838d3da613273705b7914a149593c400a33087e9c383c2e12650a`.
The source-verifying loader and effective Docker controls are checked. Runners
allocate unique labels and remove only their own containers, volumes and staging.

```sh
npm ci --prefix experiments/planner-probe
npm --prefix experiments/planner-probe run check
npm --prefix experiments/planner-probe test
npm --prefix experiments/planner-probe run check:native
npm --prefix experiments/planner-probe run test:native
GAFFER_ROUTER_SOURCE=/absolute/pinned/public/router \
  npm --prefix experiments/planner-probe run prove:native-router -- /absolute/matrix
GAFFER_ROUTER_SOURCE=/absolute/pinned/public/router \
  npm --prefix experiments/planner-probe run prove:native-gateway -- /absolute/gateway
GAFFER_BRIDGE_ROUTER_SOURCE=/absolute/pinned/public/router \
  npm --prefix experiments/planner-probe run prove:router -- /absolute/legacy.json
```

These commands use synthetic original HTTP and spend zero live inference
attempts. The initial live suite remains wholly unstarted. Finish both intended
consumers, fresh independent reviews and review of the concrete deployment packet
before the single approved aggregate scope of **10 physical inference attempts
and 600000 ms** starts. Retries and all consumer roles share it. Review delays,
replacement grants, profile selection and 32/900000 baseline scopes cannot
replenish that initial authorization. No live eligibility is inferred from these
offline results.
