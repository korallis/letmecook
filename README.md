# letmecook

**Turn an idea into reviewed software, and keep work moving on machines you own.**

Gaffer is the working product name for letmecook: a proposed self-hosted coordinator
for coding agents. Capture an idea, approve a bounded plan, run it on an eligible
machine, and review the exact changes with verification evidence.

**M0 experiments are in progress.** The reviewed v0.4 design, documentation reader
and issue-based workflow now sit alongside isolated feasibility experiments and
the [provisional execution protocol](schemas/execution/README.md). The application
is not implemented yet; milestone evidence determines what gets built next.
See the [inference boundary proof](docs/evidence/inference-boundary.md),
[passive status experiment](experiments/router-status/README.md),
[Linux isolation proof](docs/evidence/linux-profile.md), and the
[9Router integration contract](docs/contracts/9router.md).

All worker, planner and reviewer model calls go through **9Router**. It owns provider
authentication, multiple subscriptions from the same provider, account rotation and
request fallback. Gaffer owns task authority, execution, recovery and review.

Gaffer is designed for installation on infrastructure **you choose**. The daemon,
runners and 9Router may share a suitable host or run on separate configured hosts.
No maintainer machine, hostname, account or private network is a product dependency.
The released app must support installation and the complete execution workflow,
including workers, without requiring Docker. Docker is optional for testing.
Supported unattended execution still requires an independently proven OS/native
or dedicated VM confinement profile; unavailable profiles block execution.
This is a product requirement, not a claim that a Docker-free runner or installer
has already been validated.
Connect an existing 9Router instance or set up your own; supported migration can
preserve router configuration without bringing provider credentials into Gaffer.
See [operator-selected deployment](docs/spec.md#12-operator-selected-deployment).

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
| [Baseline measurement contract](docs/evidence/baseline.md) | Fixture design, collection procedure and missing M0 evidence |
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
