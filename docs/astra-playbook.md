# Building letmecook with GPT-6 Astra

The unit of work is one GitHub issue and one pull request. An issue states the
outcome, context, dependencies, exclusions and observable acceptance criteria.
The executing agent chooses the implementation details within that contract.

## Published guidance used

Checked **14 September 2026**. OpenAI's [GPT-6 Astra model guidance](https://developers.openai.com/api/docs/guides/latest-model?model=gpt-6-astra)
recommends explicit completion boundaries, autonomous follow-through, clear
instruction precedence, deliberate delegation, concise communication and
verification proportional to the change. These are adapted here to this repository's
one-issue delivery workflow; the issue prompts are project instructions, not quotations.

OpenAI's [Rethinking skills and prompts for GPT-6 Astra](https://developers.openai.com/blog/rethinking-skills-and-prompts-for-gpt-6-astra),
published **11 September 2026**, advises keeping instructions lean, selecting context
for the actual task, avoiding obsolete recipes and defining where completion ends.
Accordingly, each issue links the sections it needs rather than requiring a full
documentation audit on every run. We have not installed a stack of generic skills.

These sources were the latest relevant official guidance found on the check date.
Recheck them when changing the workflow or model; do not silently change a task's
scope because a later prompt example exists.

## Handoff prompt

Replace the issue reference and use the issue's own focused instruction:

```text
Implement korallis/letmecook#<number> using GPT-6 Astra.

Read AGENTS.md, the issue, its prerequisite outcomes and its linked context.
Confirm the prerequisites exist on the branch. Deliver the stated outcome and
acceptance criteria in one codex/<number>-<short-name> branch and one PR.
Choose routine details yourself and continue through implementation, relevant
verification and fixes. Keep the PR within this issue's scope. If a material
contract is missing, explain it with evidence and continue independent work.

Use local Astra subagents for independent work or review when it helps; keep one
writer per branch/worktree. Preserve the model-gateway ownership boundary and the
authority, isolation and durability requirements relevant to this issue.

Open or update the PR with Closes #<number>, criterion coverage, observed check
results and remaining limitations. Do not merge or deploy. Report the PR URL and
any blocked criterion. Do not invent live-probe or human-evaluation results.
```

## Completion by issue type

| Type | Reviewable result |
| --- | --- |
| Specification | Versioned contract or decision, examples/failure traces and a clear implementation boundary |
| Implementation | Working behaviour with relevant tests, integration evidence and updated docs |
| Validation | Reproducible procedure, declared fixture/cohort, raw redacted observations and an honest gate decision |

A failed feasibility result can complete an investigation when its evidence and
decision are recorded. It cannot unblock an implementation whose required capability
failed. A gate-validation issue remains blocked if mandatory observations are missing;
do not collapse a multi-day study into a synthetic run.

## Source and scope discipline

Read the current issue body and relevant accepted contracts. The v0.4 planning docs
are the initial design baseline, not an immutable implementation API. If the issue
and accepted specification disagree materially, resolve that discrepancy explicitly
and update the affected issue/docs before building incompatible behaviour.

The coding model is GPT-6 Astra. Runtime model targets are a separate product concern:
all application inference still goes through the operator-configured model gateway.
Integration probes must verify the deployed model's actual protocol and tool
capabilities; using Astra to build the repository does not prove that every
gateway/harness combination supports it.

Later experiments in the roadmap require a separate decision and scoped issue.
They are not implicit scope in a nearby v1 ticket.
