# Contributing

Work is specified in [GitHub issues](https://github.com/korallis/letmecook/issues).
Each issue delivers one independently reviewable outcome in one PR. Specification
and validation issues also produce a PR: an agreed contract or reproducible evidence
report is a deliverable. They are not permission to build all their dependencies.

Before taking an issue, check that prerequisite PRs have merged and their acceptance
evidence applies. A closed issue alone is insufficient if it was cancelled or did
not deliver its contract. `status:ready` identifies the initial dependency-free
queue; maintainers update readiness as work lands. It does not guarantee available
credentials, machines or human evaluation participants.

Create `codex/<issue-number>-<short-name>` from current `main`. Keep one writer in
each branch/worktree. Dependent PRs should normally wait for prerequisites to merge;
when a stack is necessary, name its parent PR and rebase/retarget after it merges.
Use GitHub and plain git; Graphite and cloud coding agents are not part of this flow.

Implement the acceptance criteria and preserve the issue's exclusions. If new work
is necessary, describe the missing contract or defect in a focused linked issue
instead of expanding the current PR into a second feature. Treat proposed paths as
navigation hints and update them when the selected foundation establishes real code.

Open one PR with `Closes #<issue-number>`, a concise behaviour description, criterion
coverage and actual verification results. Explain omitted or blocked validation.
The implementing task covers its branch and PR; merging and deployment remain
separate operator decisions. GitHub review and required checks govern merge.

For documentation reader changes:

```sh
npm --prefix docs ci
npm --prefix docs run build
npm --prefix docs run check
```

Keep generated HTML synchronized. Run experiment-specific checks from the changed
experiment's README. Evidence reports must identify versions, fixtures, commands,
observations and limitations. Report a failed gate honestly; waiting for a real
seven-day cohort cannot be replaced with invented data.

Never commit credentials, private transcripts or personal fixture data. Live model
probes use operator-configured 9Router connections and declared budgets. The normal
offline fixture suite should use synthetic secrets and disposable repositories.
