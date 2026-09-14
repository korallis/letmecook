# Scoped boundary consumes enforced router authority

Issue [#79](https://github.com/korallis/letmecook/issues/79) adds a synthetic bridge
between the accepted attempt boundary and #77's real pinned router modules and
SQLite. [Reproduction and exact adapter contract](../../experiments/router-boundary-bridge/README.md).
The [run record](router-boundary-bridge-run.json) contains source/runtime identities,
individual original receipts and durable decisions, effective isolation controls,
resource observations and verified cleanup. Independent final-head review remains
required before merge.

The committed run passed all 34 scenarios using the locked Node 24.21.0 image.
All 48 recorded source hashes match the tested source. Maximum observed gateway
cgroup memory peak was 161,538,048 bytes; the separate worker peaked at 43,388,928
bytes, with no OOM and zero containers or volumes remaining after cleanup.
First-party checks under local Node 24.19.0 passed 54 boundary tests, three
lifecycle tests and both TypeScript checks. The original fixture proof passed all
33 scenarios; documentation build and link checks passed.

## Scope of the evidence

This proves a shared primitive for worker/planner-role consumers, including text,
two-tool continuation, fallback, refresh, cancellation, original failure/incomplete,
malformed receipt, policy change/mutation failure, durable send gaps and recovery.
It does not run the pinned OpenCode binary or the existing PlannerSession, select
a live router, use provider credentials, call a real provider, spend money, access
private data, deploy, or build the durable application core. Both profiles remain
synthetic; native provider-output bounds remain unsupported and live admission is
false. Existing #6/#7/#8 and the foundation gate stay open.

The subsequent [#81 limits amendment](../contracts/native-subscription-limits.md)
records operator approval of an optional native local-limits profile and bounded
evaluation. It leaves this historical evidence and runtime unchanged. Implementation
of that profile, reviewed deployment controls, live conformance, real baseline and
two-subscription results, and the #9 foundation decision remain work.

The old fixture policy/parser and its committed evidence are preserved. The new
schema-2 policies bind deployment/boot/generation/revision, exact full graph and
operation envelope. The private-process authority bridge refuses replacement.
Final/tool output requires local validation and a matching original success receipt
persisted into the boundary's durable decision; quiescent failure, refresh success
alone, unknown work and translated apparent completion cannot substitute.

Two persistence failure windows withhold output and confirmed audit references:
before opening the journal temporary file, and after rename but before the parent
directory sync acknowledges it. The artifact distinguishes `gateSnapshot` from
`journalOnDisk`; validated recovery may observe an unacknowledged on-disk decision
but does not replay inference, tools or final output.

The gateway variant is explicitly 768 MiB with actual cgroup measurements; the
separate lightweight proof worker is 128 MiB. Neither number certifies the existing
OpenCode harness. Full effective controls and OOM/cleanup observations appear in
the run artifact, rather than being inferred from requested flags.

## Retained failures

[Original failed observations](router-boundary-bridge-failures.json) retain their
pre-run source digests, runtime identity, errors and cleanup results:

- The first compatible run rejected a duplicate DONE produced by actual stock
  translation. Instrumented replay showed two DONE markers following the valid
  finish. Only the separate receipt-gated compatible codec now normalizes one
  duplicate; fixture behavior, excess duplicates and new semantic data stay strict.
- Stock SQLite's SIGTERM handler exited before bridge evidence shutdown, so the
  supervisor could not read its result file. Cooperative shutdown now uses a
  private signal; raw SIGKILL remains an uncertain recovery case.
- The first full matrix passed the ordinary scenarios but its crash worker wrongly
  expected an HTTP 404 from a gateway it had just killed. The crash-specific probe
  now records that the listener is unavailable, while live scenarios still require
  management denial. This was a proof expectation failure, not evidence that
  management became accessible.

The pinned native translator's absent DONE was separately established by source
inspection and exercised through the actual router. Its distinct codec prepares
completion at clean EOF but cannot release it without original `provider_completed`
evidence. Native failed/incomplete streams translated to apparent Chat stop are
rejected by the receipt gate.

## Existing harness #6: required additional work

After merging this shared boundary, add an actual-router mode to
`tests/fixtures/harness/gateway.ts`; retain its original fixture and evidence.
Consume the shared bridge and measured gateway variant without restoring older
copies of shared boundary files. The pinned OpenCode worker's separate 512 MiB
variant remains its own proof.

The captured OpenCode profile has four deliberate incompatibilities with the
accepted common ingress/observer: explicit `tool_choice:auto`,
`stream_options.include_usage:true`, empty-string content on tool-only assistant
history, and post-finish `choices:[]` usage frames. It also needs its exact captured
edit/write schemas and permitted path, rather than this bridge's read_file tool.
Resolve those through a separately reviewed profile or an observed supported binary
configuration; do not silently delete controls or call the current profile compatible.

Parameterize the approved route/settings in `experiments/harness/adapter.ts`, then
run the actual binary through the new mode. Join base SHA, changed file/diff/digest,
native tool/session outcome and both inference request IDs to private durable
boundary decisions. CLI exit zero and an artifact alone do not establish acceptance.
Retain approval blocking, plugin/ambient discovery, failed runs, process cleanup and
original OOM findings. Actual native consumer protocol and any live task remain
separate capability/admission work.

## Existing planner #7: required additional work

Add a shared-bridge mode to `experiments/planner-probe/fixture.ts` and exercise the
real PlannerSession. Preserve its existing fixture, bounded reader, proposal schema
and proposal-only authority. Do not turn this proof's lightweight planner-role
client into a claim that that complete flow ran.

For schema 2, construct admitted requests without choice controls; include tools
only while discovery is allowed and omit them for final generation/repair. Enforce
that local tool allowance independently. Preserve assistant/null and complete tool
result history. Empty/whitespace failed proposals cannot simply become blank
assistant history during repair; construct a valid bounded packet or report failure.

`transport.ts` must preserve per-request call IDs and the boundary request header,
then expose a safe request/decision reference to the trusted consumer instead of
reparsing every response under one fixed ID. Keep clean EOF/error gating and no
retry after uncertainty. The full policy already participates in inputRevision;
retain that binding without sending scoped secrets to the model. Preserve one
assessment, one charged repair, finite read/request/byte/time budgets and local
post-generation schema validation.

Only update the particular synthetic-authority capability proved here. Native
Responses request/continuation support, exact effort, strict provider output,
deployed authority, native provider-output bounds and live acceptance remain
separate. No native output-bound contract change is implied by these diagnostics.
