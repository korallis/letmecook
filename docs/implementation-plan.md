# Implementation issue index

Created **14 September 2026** from the reviewed v0.4 plan. The backlog contains **63 focused issues across seven milestones**, each with one outcome, context links, dependencies, acceptance checks, exclusions and a GPT-6 Astra instruction.

[GitHub issues](https://github.com/korallis/letmecook/issues) are the live work-status source of truth. This file is a navigation and traceability index, not a duplicate status tracker. Read current issue bodies and native blocked-by relationships before starting. Later milestone details may be refined by evidence without changing their single-purpose delivery rule.

## Start here

- [#1 Define representative fixtures and record the standalone baseline](https://github.com/korallis/letmecook/issues/1) — Establish the task/capture fixture and measurement method before tuning the product, so later value claims have a fair comparator.
- [#2 Specify the pinned 9Router integration contract](https://github.com/korallis/letmecook/issues/2) — Make the shared model-access boundary implementable without duplicating provider-account management.

The [ready queue](https://github.com/korallis/letmecook/issues?q=is%3Aissue%20is%3Aopen%20label%3Astatus%3Aready) starts with those two items. Readiness describes issue dependencies, not the availability of operator-labelled examples, configured test subscriptions, isolated hosts or evaluation participants. Required external inputs must be recorded honestly.

## Delivery rules

One issue → one `codex/<issue-number>-<short-name>` branch → one PR containing `Closes #<number>`. Specification PRs deliver contracts/decisions; implementation PRs deliver behaviour; validation PRs deliver reproducible evidence. Check prerequisite outcomes on the branch, not only an issue’s closed state.

Follow [AGENTS.md](../AGENTS.md), [Contributing](../CONTRIBUTING.md) and the [Astra playbook](astra-playbook.md). Assigned implementation includes its branch and PR; merge, deployment and release publication remain separate decisions. Creating this backlog does not launch its implementation.

M0 experiments establish the selected foundation. Application package paths in later issues are proposed starting points until that decision lands. The coding agent is Astra; configured runtime model routes remain a separate product choice, always through 9Router.

When a prerequisite lands, review its evidence and update dependent readiness labels. Cancelled or failed-capability issues cannot unblock a dependent implementation by being closed. Long-running gates require actual observations; fixes discovered by a gate get their own focused issue and PR.

## M0 — Evidence and feasibility

[Milestone on GitHub](https://github.com/korallis/letmecook/milestone/1) · 9 issues.

| ID | Issue | Type | Prerequisites |
| --- | --- | --- | --- |
| M0-01 | [#1 Define representative fixtures and record the standalone baseline](https://github.com/korallis/letmecook/issues/1) | specification | None |
| M0-02 | [#2 Specify the pinned 9Router integration contract](https://github.com/korallis/letmecook/issues/2) | specification | None |
| M0-03 | [#3 Prototype attempt-scoped inference access through 9Router](https://github.com/korallis/letmecook/issues/3) | implementation | [#2](https://github.com/korallis/letmecook/issues/2) |
| M0-04 | [#4 Project sanitized 9Router status](https://github.com/korallis/letmecook/issues/4) | implementation | [#2](https://github.com/korallis/letmecook/issues/2) |
| M0-05 | [#5 Prove one Linux execution isolation profile](https://github.com/korallis/letmecook/issues/5) | implementation | [#2](https://github.com/korallis/letmecook/issues/2) |
| M0-06 | [#6 Run the first coding harness through a named 9Router route](https://github.com/korallis/letmecook/issues/6) | implementation | [#3](https://github.com/korallis/letmecook/issues/3), [#5](https://github.com/korallis/letmecook/issues/5) |
| M0-07 | [#7 Prove restricted planning and discovery through 9Router](https://github.com/korallis/letmecook/issues/7) | implementation | [#3](https://github.com/korallis/letmecook/issues/3), [#5](https://github.com/korallis/letmecook/issues/5) |
| M0-08 | [#8 Prove same-provider subscription continuity](https://github.com/korallis/letmecook/issues/8) | validation | [#4](https://github.com/korallis/letmecook/issues/4), [#6](https://github.com/korallis/letmecook/issues/6), [#7](https://github.com/korallis/letmecook/issues/7) |
| M0-09 | [#9 Choose build, adopt or extend from measured foundation evidence](https://github.com/korallis/letmecook/issues/9) | specification | [#1](https://github.com/korallis/letmecook/issues/1), [#8](https://github.com/korallis/letmecook/issues/8) |

## M1 — Durable bounded execution

[Milestone on GitHub](https://github.com/korallis/letmecook/milestone/2) · 15 issues.

| ID | Issue | Type | Prerequisites |
| --- | --- | --- | --- |
| M1-01 | [#10 Specify fenced attempt execution and recovery messages](https://github.com/korallis/letmecook/issues/10) | specification | [#9](https://github.com/korallis/letmecook/issues/9) |
| M1-02 | [#11 Establish the durable workflow store and application scaffold](https://github.com/korallis/letmecook/issues/11) | implementation | [#10](https://github.com/korallis/letmecook/issues/10) |
| M1-03 | [#12 Enforce immutable execution grants and revisions](https://github.com/korallis/letmecook/issues/12) | implementation | [#11](https://github.com/korallis/letmecook/issues/11) |
| M1-04 | [#13 Authenticate owner bootstrap and runner enrollment](https://github.com/korallis/letmecook/issues/13) | implementation | [#11](https://github.com/korallis/letmecook/issues/11) |
| M1-05 | [#14 Validate and register an explicitly permitted repository](https://github.com/korallis/letmecook/issues/14) | implementation | [#12](https://github.com/korallis/letmecook/issues/12), [#13](https://github.com/korallis/letmecook/issues/13) |
| M1-06 | [#15 Commit eligible dispatch and reservations atomically](https://github.com/korallis/letmecook/issues/15) | implementation | [#12](https://github.com/korallis/letmecook/issues/12), [#13](https://github.com/korallis/letmecook/issues/13), [#14](https://github.com/korallis/letmecook/issues/14) |
| M1-07 | [#16 Supervise a durable runner attempt under a lease](https://github.com/korallis/letmecook/issues/16) | implementation | [#15](https://github.com/korallis/letmecook/issues/15), [#6](https://github.com/korallis/letmecook/issues/6) |
| M1-08 | [#17 Preserve ordered run output across reconnects](https://github.com/korallis/letmecook/issues/17) | implementation | [#16](https://github.com/korallis/letmecook/issues/16) |
| M1-09 | [#18 Preserve complete candidate artifacts before acknowledging results](https://github.com/korallis/letmecook/issues/18) | implementation | [#16](https://github.com/korallis/letmecook/issues/16) |
| M1-10 | [#19 Make pause, cancellation and global stop durable](https://github.com/korallis/letmecook/issues/19) | implementation | [#12](https://github.com/korallis/letmecook/issues/12), [#16](https://github.com/korallis/letmecook/issues/16) |
| M1-11 | [#20 Reconcile interruptions before bounded task retry](https://github.com/korallis/letmecook/issues/20) | implementation | [#18](https://github.com/korallis/letmecook/issues/18), [#19](https://github.com/korallis/letmecook/issues/19) |
| M1-12 | [#21 Verify and locally accept an exact candidate](https://github.com/korallis/letmecook/issues/21) | implementation | [#12](https://github.com/korallis/letmecook/issues/12), [#18](https://github.com/korallis/letmecook/issues/18) |
| M1-13 | [#22 Expose the bounded local workflow through a typed CLI](https://github.com/korallis/letmecook/issues/22) | implementation | [#17](https://github.com/korallis/letmecook/issues/17), [#20](https://github.com/korallis/letmecook/issues/20), [#21](https://github.com/korallis/letmecook/issues/21) |
| M1-14 | [#23 Restore a consistent paused state snapshot](https://github.com/korallis/letmecook/issues/23) | implementation | [#20](https://github.com/korallis/letmecook/issues/20), [#22](https://github.com/korallis/letmecook/issues/22) |
| M1-15 | [#24 Record the durable-core acceptance gate](https://github.com/korallis/letmecook/issues/24) | validation | [#22](https://github.com/korallis/letmecook/issues/22), [#23](https://github.com/korallis/letmecook/issues/23) |

## M2 — Intent and operator workflow

[Milestone on GitHub](https://github.com/korallis/letmecook/milestone/3) · 16 issues.

| ID | Issue | Type | Prerequisites |
| --- | --- | --- | --- |
| M2-01 | [#25 Specify owner browser authentication and device sessions](https://github.com/korallis/letmecook/issues/25) | specification | [#24](https://github.com/korallis/letmecook/issues/24) |
| M2-02 | [#26 Implement authenticated private browser access](https://github.com/korallis/letmecook/issues/26) | implementation | [#25](https://github.com/korallis/letmecook/issues/25) |
| M2-03 | [#27 Expose alpha 9Router connection and route status](https://github.com/korallis/letmecook/issues/27) | implementation | [#26](https://github.com/korallis/letmecook/issues/26), [#4](https://github.com/korallis/letmecook/issues/4) |
| M2-04 | [#28 Specify capture-to-approval revision semantics](https://github.com/korallis/letmecook/issues/28) | specification | [#24](https://github.com/korallis/letmecook/issues/24) |
| M2-05 | [#29 Implement a durable phone capture inbox](https://github.com/korallis/letmecook/issues/29) | implementation | [#26](https://github.com/korallis/letmecook/issues/26), [#28](https://github.com/korallis/letmecook/issues/28) |
| M2-06 | [#30 Produce bounded discovery and structured intent proposals](https://github.com/korallis/letmecook/issues/30) | implementation | [#29](https://github.com/korallis/letmecook/issues/29), [#7](https://github.com/korallis/letmecook/issues/7), [#27](https://github.com/korallis/letmecook/issues/27) |
| M2-07 | [#31 Implement editable brief-and-plan approval](https://github.com/korallis/letmecook/issues/31) | implementation | [#30](https://github.com/korallis/letmecook/issues/30) |
| M2-08 | [#32 Show reconnectable progress and honest stop controls](https://github.com/korallis/letmecook/issues/32) | implementation | [#26](https://github.com/korallis/letmecook/issues/26) |
| M2-09 | [#33 Compose sequential candidates before verification](https://github.com/korallis/letmecook/issues/33) | implementation | [#24](https://github.com/korallis/letmecook/issues/24) |
| M2-10 | [#34 Implement artifact-specific browser review and acceptance](https://github.com/korallis/letmecook/issues/34) | implementation | [#32](https://github.com/korallis/letmecook/issues/32), [#33](https://github.com/korallis/letmecook/issues/33), [#31](https://github.com/korallis/letmecook/issues/31) |
| M2-11 | [#35 Specify publication authority and external-action reconciliation](https://github.com/korallis/letmecook/issues/35) | specification | [#33](https://github.com/korallis/letmecook/issues/33) |
| M2-12 | [#36 Publish an authorized candidate as a reconciled GitHub PR](https://github.com/korallis/letmecook/issues/36) | implementation | [#34](https://github.com/korallis/letmecook/issues/34), [#35](https://github.com/korallis/letmecook/issues/35) |
| M2-13 | [#37 Persist minimal human-approved project memory](https://github.com/korallis/letmecook/issues/37) | implementation | [#31](https://github.com/korallis/letmecook/issues/31), [#23](https://github.com/korallis/letmecook/issues/23) |
| M2-14 | [#38 Record local operator effort and resource accounting](https://github.com/korallis/letmecook/issues/38) | implementation | [#34](https://github.com/korallis/letmecook/issues/34), [#4](https://github.com/korallis/letmecook/issues/4) |
| M2-15 | [#39 Implement an actionable in-app work digest](https://github.com/korallis/letmecook/issues/39) | implementation | [#36](https://github.com/korallis/letmecook/issues/36) |
| M2-16 | [#40 Record the private-alpha intent and workflow gate](https://github.com/korallis/letmecook/issues/40) | validation | [#37](https://github.com/korallis/letmecook/issues/37), [#38](https://github.com/korallis/letmecook/issues/38), [#39](https://github.com/korallis/letmecook/issues/39), [#1](https://github.com/korallis/letmecook/issues/1) |

## M3 — Portable knowledge

[Milestone on GitHub](https://github.com/korallis/letmecook/milestone/4) · 5 issues.

| ID | Issue | Type | Prerequisites |
| --- | --- | --- | --- |
| M3-01 | [#41 Reconcile context files into a disposable scoped index](https://github.com/korallis/letmecook/issues/41) | implementation | [#40](https://github.com/korallis/letmecook/issues/40) |
| M3-02 | [#42 Retrieve bounded approved context by reference and FTS](https://github.com/korallis/letmecook/issues/42) | implementation | [#41](https://github.com/korallis/letmecook/issues/41) |
| M3-03 | [#43 Expose context history, correction and export](https://github.com/korallis/letmecook/issues/43) | implementation | [#42](https://github.com/korallis/letmecook/issues/42) |
| M3-04 | [#44 Flag stale commands with source evidence](https://github.com/korallis/letmecook/issues/44) | implementation | [#43](https://github.com/korallis/letmecook/issues/43) |
| M3-05 | [#45 Measure memory utility and recovery](https://github.com/korallis/letmecook/issues/45) | validation | [#44](https://github.com/korallis/letmecook/issues/44), [#38](https://github.com/korallis/letmecook/issues/38) |

## M4 — Capacity and parallel delivery

[Milestone on GitHub](https://github.com/korallis/letmecook/milestone/5) · 6 issues.

| ID | Issue | Type | Prerequisites |
| --- | --- | --- | --- |
| M4-01 | [#46 Specify fair admission and review-backlog limits](https://github.com/korallis/letmecook/issues/46) | specification | [#40](https://github.com/korallis/letmecook/issues/40) |
| M4-02 | [#47 Add a second harness using the same 9Router instance](https://github.com/korallis/letmecook/issues/47) | implementation | [#40](https://github.com/korallis/letmecook/issues/40) |
| M4-03 | [#48 Expose explicit multi-runner placement controls](https://github.com/korallis/letmecook/issues/48) | implementation | [#46](https://github.com/korallis/letmecook/issues/46) |
| M4-04 | [#49 Admit bounded work using router observations](https://github.com/korallis/letmecook/issues/49) | implementation | [#47](https://github.com/korallis/letmecook/issues/47), [#48](https://github.com/korallis/letmecook/issues/48) |
| M4-05 | [#50 Run independent tasks with serial dependency integration](https://github.com/korallis/letmecook/issues/50) | implementation | [#49](https://github.com/korallis/letmecook/issues/49), [#33](https://github.com/korallis/letmecook/issues/33) |
| M4-06 | [#51 Measure beta value and parallel task classes](https://github.com/korallis/letmecook/issues/51) | validation | [#50](https://github.com/korallis/letmecook/issues/50), [#45](https://github.com/korallis/letmecook/issues/45) |

## M5 — Recurring work

[Milestone on GitHub](https://github.com/korallis/letmecook/milestone/6) · 5 issues.

| ID | Issue | Type | Prerequisites |
| --- | --- | --- | --- |
| M5-01 | [#52 Specify standing triggers and occurrence identity](https://github.com/korallis/letmecook/issues/52) | specification | [#51](https://github.com/korallis/letmecook/issues/51) |
| M5-02 | [#53 Create bounded schedule occurrences](https://github.com/korallis/letmecook/issues/53) | implementation | [#52](https://github.com/korallis/letmecook/issues/52) |
| M5-03 | [#54 Poll and deduplicate authorized GitHub CI failures](https://github.com/korallis/letmecook/issues/54) | implementation | [#53](https://github.com/korallis/letmecook/issues/53), [#36](https://github.com/korallis/letmecook/issues/36) |
| M5-04 | [#55 Schedule meaningful in-app digests](https://github.com/korallis/letmecook/issues/55) | implementation | [#53](https://github.com/korallis/letmecook/issues/53), [#39](https://github.com/korallis/letmecook/issues/39) |
| M5-05 | [#56 Record recurring-work endurance evidence](https://github.com/korallis/letmecook/issues/56) | validation | [#54](https://github.com/korallis/letmecook/issues/54), [#55](https://github.com/korallis/letmecook/issues/55) |

## M6 — External usability and v1

[Milestone on GitHub](https://github.com/korallis/letmecook/milestone/7) · 7 issues.

| ID | Issue | Type | Prerequisites |
| --- | --- | --- | --- |
| M6-01 | [#57 Specify installation and upgrade transactions](https://github.com/korallis/letmecook/issues/57) | specification | [#56](https://github.com/korallis/letmecook/issues/56) |
| M6-02 | [#58 Retain opt-in offline capture drafts](https://github.com/korallis/letmecook/issues/58) | implementation | [#57](https://github.com/korallis/letmecook/issues/57), [#29](https://github.com/korallis/letmecook/issues/29) |
| M6-03 | [#59 Resolve the project licence and dependency obligations](https://github.com/korallis/letmecook/issues/59) | specification | [#57](https://github.com/korallis/letmecook/issues/57) |
| M6-04 | [#60 Produce signed installable artifacts for validated platforms](https://github.com/korallis/letmecook/issues/60) | implementation | [#59](https://github.com/korallis/letmecook/issues/59) |
| M6-05 | [#61 Execute tested upgrades and rollback](https://github.com/korallis/letmecook/issues/61) | implementation | [#60](https://github.com/korallis/letmecook/issues/60) |
| M6-06 | [#62 Complete the operator maintenance and support guide](https://github.com/korallis/letmecook/issues/62) | implementation | [#58](https://github.com/korallis/letmecook/issues/58), [#61](https://github.com/korallis/letmecook/issues/61) |
| M6-07 | [#63 Record independent v1 installation and recovery evidence](https://github.com/korallis/letmecook/issues/63) | validation | [#62](https://github.com/korallis/letmecook/issues/62) |

## PRD traceability

These links identify the primary work slices, not a claim that a story is implemented. Multi-stage stories retain their release boundaries in the PRD.

| Story | Planned coverage |
| --- | --- |
| US-A1 — Get to a first useful result | [#22](https://github.com/korallis/letmecook/issues/22), [#40](https://github.com/korallis/letmecook/issues/40), [#57](https://github.com/korallis/letmecook/issues/57), [#60](https://github.com/korallis/letmecook/issues/60), [#62](https://github.com/korallis/letmecook/issues/62), [#63](https://github.com/korallis/letmecook/issues/63) |
| US-A2 — Decide which machines can work | [#5](https://github.com/korallis/letmecook/issues/5), [#13](https://github.com/korallis/letmecook/issues/13), [#14](https://github.com/korallis/letmecook/issues/14), [#48](https://github.com/korallis/letmecook/issues/48) |
| US-A3 — Protect machines allocated to other work | [#5](https://github.com/korallis/letmecook/issues/5), [#13](https://github.com/korallis/letmecook/issues/13), [#16](https://github.com/korallis/letmecook/issues/16), [#48](https://github.com/korallis/letmecook/issues/48) |
| US-A4 — Manage all subscriptions through one router | [#2](https://github.com/korallis/letmecook/issues/2), [#3](https://github.com/korallis/letmecook/issues/3), [#4](https://github.com/korallis/letmecook/issues/4), [#8](https://github.com/korallis/letmecook/issues/8), [#27](https://github.com/korallis/letmecook/issues/27), [#47](https://github.com/korallis/letmecook/issues/47), [#49](https://github.com/korallis/letmecook/issues/49) |
| US-A5 — Connect a repository with scoped access | [#14](https://github.com/korallis/letmecook/issues/14), [#35](https://github.com/korallis/letmecook/issues/35), [#36](https://github.com/korallis/letmecook/issues/36) |
| US-A6 — Use a private application from a phone | [#25](https://github.com/korallis/letmecook/issues/25), [#26](https://github.com/korallis/letmecook/issues/26), [#58](https://github.com/korallis/letmecook/issues/58) |
| US-B1 — Capture before the idea disappears | [#28](https://github.com/korallis/letmecook/issues/28), [#29](https://github.com/korallis/letmecook/issues/29), [#40](https://github.com/korallis/letmecook/issues/40), [#58](https://github.com/korallis/letmecook/issues/58) |
| US-B2 — Correct a brief before code changes | [#7](https://github.com/korallis/letmecook/issues/7), [#28](https://github.com/korallis/letmecook/issues/28), [#30](https://github.com/korallis/letmecook/issues/30), [#31](https://github.com/korallis/letmecook/issues/31), [#40](https://github.com/korallis/letmecook/issues/40) |
| US-B3 — Learn only what the work needs | [#7](https://github.com/korallis/letmecook/issues/7), [#28](https://github.com/korallis/letmecook/issues/28), [#30](https://github.com/korallis/letmecook/issues/30) |
| US-B4 — Approve a short plan | [#7](https://github.com/korallis/letmecook/issues/7), [#12](https://github.com/korallis/letmecook/issues/12), [#28](https://github.com/korallis/letmecook/issues/28), [#30](https://github.com/korallis/letmecook/issues/30), [#31](https://github.com/korallis/letmecook/issues/31) |
| US-B5 — Understand current progress | [#17](https://github.com/korallis/letmecook/issues/17), [#22](https://github.com/korallis/letmecook/issues/22), [#32](https://github.com/korallis/letmecook/issues/32) |
| US-B6 — Review the outcome with its evidence | [#18](https://github.com/korallis/letmecook/issues/18), [#21](https://github.com/korallis/letmecook/issues/21), [#33](https://github.com/korallis/letmecook/issues/33), [#34](https://github.com/korallis/letmecook/issues/34), [#35](https://github.com/korallis/letmecook/issues/35), [#36](https://github.com/korallis/letmecook/issues/36), [#40](https://github.com/korallis/letmecook/issues/40) |
| US-B7 — Use the right permitted machine | [#15](https://github.com/korallis/letmecook/issues/15), [#46](https://github.com/korallis/letmecook/issues/46), [#48](https://github.com/korallis/letmecook/issues/48), [#50](https://github.com/korallis/letmecook/issues/50) |
| US-C1 — Return to a useful digest | [#39](https://github.com/korallis/letmecook/issues/39), [#55](https://github.com/korallis/letmecook/issues/55) |
| US-C2 — Respond to capacity limits | [#2](https://github.com/korallis/letmecook/issues/2), [#4](https://github.com/korallis/letmecook/issues/4), [#8](https://github.com/korallis/letmecook/issues/8), [#15](https://github.com/korallis/letmecook/issues/15), [#27](https://github.com/korallis/letmecook/issues/27), [#46](https://github.com/korallis/letmecook/issues/46), [#49](https://github.com/korallis/letmecook/issues/49) |
| US-C3 — Recover from provider outages | [#2](https://github.com/korallis/letmecook/issues/2), [#6](https://github.com/korallis/letmecook/issues/6), [#8](https://github.com/korallis/letmecook/issues/8), [#20](https://github.com/korallis/letmecook/issues/20), [#27](https://github.com/korallis/letmecook/issues/27), [#49](https://github.com/korallis/letmecook/issues/49) |
| US-C4 — Start recurring work | [#52](https://github.com/korallis/letmecook/issues/52), [#53](https://github.com/korallis/letmecook/issues/53), [#54](https://github.com/korallis/letmecook/issues/54), [#56](https://github.com/korallis/letmecook/issues/56) |
| US-C5 — Stop with an honest acknowledgment | [#10](https://github.com/korallis/letmecook/issues/10), [#12](https://github.com/korallis/letmecook/issues/12), [#16](https://github.com/korallis/letmecook/issues/16), [#19](https://github.com/korallis/letmecook/issues/19), [#22](https://github.com/korallis/letmecook/issues/22), [#24](https://github.com/korallis/letmecook/issues/24), [#32](https://github.com/korallis/letmecook/issues/32) |
| US-D1 — Stop rediscovering established facts | [#37](https://github.com/korallis/letmecook/issues/37), [#41](https://github.com/korallis/letmecook/issues/41), [#42](https://github.com/korallis/letmecook/issues/42), [#43](https://github.com/korallis/letmecook/issues/43), [#45](https://github.com/korallis/letmecook/issues/45) |
| US-D2 — Remember how the operator uses words | Deferred: learned idioms need scoped ambiguity and wrong-application evaluation before a new implementation issue. |
| US-D3 — Challenge stale memory | [#44](https://github.com/korallis/letmecook/issues/44), [#45](https://github.com/korallis/letmecook/issues/45) |
| US-D4 — Reduce review selectively | Deferred: automatic review reduction/auto-merge needs a separately requested narrow policy and reliable candidate/check/revocation evidence. Human approval remains in v1. |
| US-D5 — Survive process failure and restore | [#10](https://github.com/korallis/letmecook/issues/10), [#11](https://github.com/korallis/letmecook/issues/11), [#16](https://github.com/korallis/letmecook/issues/16), [#17](https://github.com/korallis/letmecook/issues/17), [#18](https://github.com/korallis/letmecook/issues/18), [#20](https://github.com/korallis/letmecook/issues/20), [#23](https://github.com/korallis/letmecook/issues/23), [#24](https://github.com/korallis/letmecook/issues/24), [#41](https://github.com/korallis/letmecook/issues/41), [#57](https://github.com/korallis/letmecook/issues/57), [#61](https://github.com/korallis/letmecook/issues/61), [#63](https://github.com/korallis/letmecook/issues/63) |
| US-D6 — Measure what was delivered | [#1](https://github.com/korallis/letmecook/issues/1), [#38](https://github.com/korallis/letmecook/issues/38), [#40](https://github.com/korallis/letmecook/issues/40), [#51](https://github.com/korallis/letmecook/issues/51), [#56](https://github.com/korallis/letmecook/issues/56) |
| US-D7 — Reduce overhead without reducing quality | [#1](https://github.com/korallis/letmecook/issues/1), [#38](https://github.com/korallis/letmecook/issues/38), [#42](https://github.com/korallis/letmecook/issues/42), [#45](https://github.com/korallis/letmecook/issues/45), [#50](https://github.com/korallis/letmecook/issues/50), [#51](https://github.com/korallis/letmecook/issues/51) |
| US-E1 — Support another harness predictably | [#6](https://github.com/korallis/letmecook/issues/6), [#47](https://github.com/korallis/letmecook/issues/47), [#51](https://github.com/korallis/letmecook/issues/51) |
| US-E2 — Keep memory usable without Gaffer | [#37](https://github.com/korallis/letmecook/issues/37), [#41](https://github.com/korallis/letmecook/issues/41), [#43](https://github.com/korallis/letmecook/issues/43), [#62](https://github.com/korallis/letmecook/issues/62), [#63](https://github.com/korallis/letmecook/issues/63) |
| US-E3 — Reuse workflows as skills | Deferred: skill loading needs a scoped provenance/permissions contract and observed workflow demand; skills must not expand authority. |

## Deferred experiments

The [roadmap entry conditions](roadmap.md#later-experiments-and-entry-conditions) govern automatic memory promotion, learned idioms, embeddings, auto-merge, hunk editing, browser/desktop runners, third-party adapter ABI, team use and local-only inference. Create a separate bounded issue when its evidence/authority condition is met; do not add it implicitly to a nearby v1 issue. Compact encoding/TOON is an optional measured optimization, not a required transport or a claimed source of extra subscription capacity.
