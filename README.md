# letmecook

**Turn an idea into reviewed software, and keep work moving on machines you own.**

Gaffer is the working product name for letmecook: a proposed self-hosted coordinator
for coding agents. Capture an idea, approve a bounded plan, run it on an eligible
machine, and review the exact changes with verification evidence.

**Pre-code planning baseline v0.4.** This repository currently contains the reviewed
design, documentation reader and issue-based delivery workflow. The application
does not exist yet. Milestone gates determine what gets built next.

All worker, planner and reviewer model calls go through **9Router**. It owns provider
authentication, multiple subscriptions from the same provider, account rotation and
request fallback. Gaffer owns task authority, execution, recovery and review.

| Start here | Purpose |
| --- | --- |
| [Product overview](docs/README.md) | Intended workflow and scope |
| [Evaluation](docs/evaluation.md) | Research, challenges and build-versus-reuse decision |
| [PRD](docs/PRD.md) | User stories and acceptance targets |
| [Technical specification](docs/spec.md) | System contracts and failure handling |
| [Roadmap](docs/roadmap.md) | M0–M6, with evidence gates |
| [GitHub issues](https://github.com/korallis/letmecook/issues) | Assignable specification, implementation and validation work |
| [Implementation index](docs/implementation-plan.md) | Issue sequence, entry points and PRD traceability |
| [Milestones](https://github.com/korallis/letmecook/milestones) | Delivery sequence |
| [Astra playbook](docs/astra-playbook.md) | How GPT-6 Astra should execute an issue |
| [Contributing](CONTRIBUTING.md) | One issue, one branch, one pull request |

## Read the HTML documentation

Clone or download the repository and open `docs/index.html` locally. GitHub's file
viewer displays HTML source; this repository is not a deployed website. The old
`docs/reader-template.html` address redirects to the reader. Edit the Markdown
sources and `docs/reader-template.html.in`, then regenerate with Node.js 24 or newer:

```sh
npm --prefix docs ci
npm --prefix docs run build
npm --prefix docs run check
```

The initial application direction is Go for the daemon and runner, and React with
TypeScript for the web client. M0 validates the execution foundation before the
application scaffold is committed to that choice.

MIT is intended for original code and documentation; no licence has yet been
applied. The release backlog includes the licence and dependency decision.
