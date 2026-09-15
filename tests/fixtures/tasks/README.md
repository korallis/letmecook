# Baseline fixture records

These are version 1 inputs to the [baseline measurement contract](../../../docs/evidence/baseline.md),
not an execution harness or evidence of a live run.

| File | Purpose |
| --- | --- |
| `operator-intake.json` | Revision 2: three approved intended outcomes, opaque private authority reference/digest, conditional permissions and remaining executable-registration inputs |
| `history/operator-intake-20260914-proposed.json` | Preserved original proposal with null operator labels and its unchanged private fixture mapping |
| `development-examples.json` | Synthetic design examples for feasible, ambiguous, changed-intent, misleading-preference, unauthorized and impossible requests |
| `run-template.json` | Unstarted observation template; copy and complete it for a permitted real run |
| `check.ts` | Rerunnable semantic check of these design records and excluded synthetic examples |

Examples contain invented captures and expected responses. They are labelled
`synthetic`, all belong to development, and are excluded from measurement. No
held-out text is committed here. The real candidates were selected under user
delegation and their intended outcomes now have explicit session-user approval.
Necessary source/diffs may reach existing Codex subscriptions through 9Router only
after the approved profile/deployment controls pass review; secrets and production/user
data remain excluded. No completed outputs have been accepted. Null fields mean
unavailable or privately referenced, not a negative label or a zero measurement.
The unchanged run template records the earlier unstarted preparation state; use
the current intake's blockers when preparing a new run. Its arrays remain empty
because there are no attempts, timer intervals, observations,
checks, defects or accepted artifacts yet.

A registered real case requires every intake field to be completed with its source.
`repository.startingCommit` must resolve to a full immutable SHA, not a branch name.
For private source, retain that SHA in the operator-owned manifest and publish only
`startingCommitRef` plus the manifest digest. A public null SHA is deliberate
redaction and does not substitute for resolving the private value before execution.
`operatorLabel` records approval of the prepared intended outcome, its human author
and private source reference. Its `recordedAt` is when the assistant observed
approval, explicitly not an asserted message timestamp; `messageTimestamp` stays
null. The matching approval record is retained privately and addressed by
`authority.reference`/`sha256`. Public checks do not retrieve or authenticate it.
Synthetic expected responses must never be copied into it as if an operator approved them. Store
sensitive details externally with an immutable reference if repository publication
is not permitted. An infeasible case still needs an operator-defined expected safe
response and an explanation, even though it will not execute repository code.

Do not edit a frozen case in place to fit observed output. Create a new fixture
version and retain its predecessor. Follow the contract's separate held-out
storage and contamination rules before building an evaluation cohort.

`liveTrialAuthorization` is a conditional declaration, not a runtime permit.
Initial public checks are bounded to 10 inference attempts and 10 minutes in
aggregate. Each baseline case is bounded to 32 inference attempts and 15 minutes,
including retries, one case at a time. Astra/xhigh and existing Codex subscriptions
through 9Router are the approved scope; paid API fallback is prohibited. The
approved native profile retains the strict default, has no provider output/monetary cap and must block
replacement when remote work is unknown. Review, preflight, pinned fixtures,
local limit enforcement and a stop mechanism remain pending. Keep all three cases
`not-registered` and their execution eligibility `needs-clarification` until a
separate executable fixture records the satisfied prerequisites.

Record arrays in a real run use these shapes:

| Array | Required fields per entry |
| --- | --- |
| `operatorIntervals` | `id`, `operatorId`, `category`, UTC `start`/`end`, `durationSeconds`, `activity`, `confidence` (`measured`/`estimated`), `source`, optional shared-allocation reference |
| `attempts` | `id`, `startedAt`, `endedAt`, `status`, `stopReason`, `invocationRef`, `progressNotesRef`, `artifactRef`, `observations`, `checks`; include every attempt |
| `checks` | `criterionId`, exact redacted `command`, `exitCode` or null, `status` (`passed`/`failed`/`not-run`/`unknown`), `evidenceRef`; explain unrun/unknown checks |
| `rawObservations` | `id`, `kind`, `recordedAt`, relative `path`, `sha256`, `redactions`; point to actually retained redacted evidence |
| `usage` | `scope` (attempt/planning/review/setup), `source`, `confidence` (`observed`/`estimated`/`unknown`), `inputTokens`, `outputTokens`, `cachedTokens`, `incrementalCash`, `currency`; use null for unknown values |
| `defects` | `id`, `taskId`, `detectedAt`, `severity`, `classifiedBy`, `description`, `evidenceRef`, `revertRef` or null, `adjudication` |
| `acceptance.criteria` | `criterionId`, `met` (`true`/`false`/null), `evidenceRef`, `operatorNote`; require a human acceptance decision |

Run `status` starts as `blocked`, moves to `registered` only after prerequisites
and budgets are frozen, then `running`, and finally a terminal outcome named in
the contract. `acceptance.decision` stays null until human review, including when
the process has already terminated. Follow-up remains open for seven days after
acceptance. Run `node tests/fixtures/tasks/check.ts` from the repository root to
check the committed approval/history/example/template invariants. CI runs the same
check, including rejection of 57 invalid declarations that expand approved scope,
claim readiness, rewrite history or invent measurement evidence.
These design records remain unregistered: future frozen registrations and real run
records are separate reviewed artifacts. This checker is not a run collector or
general observation validator and cannot establish evidence or label authenticity.

For separate frozen declarations, collected observations and public synthetic
execution, see the [baseline experiment guide](../../../experiments/baseline/README.md).
