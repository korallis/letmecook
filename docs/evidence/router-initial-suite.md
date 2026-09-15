# Prepared worker, planner and continuity suite

Issue #8 now packages the complete initial suite before the shared clock starts:
one default OpenCode edit worker, one read-only planner and two fixed-text
continuity requests. Source `28822245f6a3d6d7ed25003295d8b5e1927ec013` includes
the reviewed worker and planner parents. This is a preparatory draft; every provider
response and credential used in these captures is synthetic.

The [compact evidence record](router-initial-suite-run.json) binds the final
production-source run to 190 verified Git blobs and 537 installed, locked planner
dependency files. Preparation hashes all staged code, fixtures, schemas and the
pinned binary. Changed packet or staged dependency bytes fail before the first
scope starts. Generated evidence is excluded from the runtime manifest.

The ordinary run used the actual deployment CLI, normal gateway loader and default
consumer entries. Six physical sends produced a durable worker candidate, a durable
proposal-only planner acknowledgement and continuity on opaque connection A then B.
The router selected B after the one-use recorded health transition on A. Both
continuity requests retained the same profile/settings. Scope identity, start and
deadline stayed unchanged; stop, evidence retention and cleanup passed. The
planner's synthetic responses each waited six seconds, beyond its old five-second
fixture ceiling.

The additive `initial-suite-v1` variant fixes session walls at 180 seconds for the
worker, 150 for the planner and 90 for each continuity member. Request total/first
output/idle ceilings are 120/90/45 seconds for worker and planner and 75/60/30 for
continuity. Four walls plus 15 seconds of acknowledgement time per phase and 15
seconds for stop require 585 seconds before worker admission. The remaining 15
seconds cover startup. Fixed session/grant deadlines and the original ten-send,
600-second scope are enforced; continuations cannot renew them. These ceilings are
engineering choices, not measured live Astra latency. Schema-1 defaults remain
unchanged.

The default package test and scoped CI workflow run all 31 control, client and
suite checks. Actual isolated fault captures provide the following additional
evidence. Each row has its own synthetic fixture scope; none renews a live budget.

| Case | Physical sends | Observed result |
| --- | ---: | --- |
| Ordinary production suite | 6 | Four qualified phases; stopped and cleaned |
| Direct planner / repaired planner | 5 / 7 | Valid proposal and continuity; actual debit joined |
| Stale packet / changed dependency | 0 / 0 | Refused before scope start |
| Partial worker original | 1 | Unknown; no acknowledgement or later phase |
| Persistent suite observation storage failure | 2 | Failed; no acknowledgement; worker stopped, logs retained |
| Persistent old attach-consumer storage failure | 1 | Failed; worker stopped, logs retained |
| Stop-command failure after qualified phases | 6 | Overall failed; independent physical fencing |

The storage cases reproduce and correct an independent review finding: cleanup
previously removed the consumer after every observation save failed. Consumers now
physically stop while retaining container logs, staging references and the last
durable identifiers. Stopping destroys the `/work` tmpfs; retaining a container does
not retain that worktree. Independent artifact durability remains unavailable, and
worker logs cannot qualify the failed run. Only the synthetic proof harness removes
those retained resources after collecting diagnostics. A stop failure likewise
preserves phase success facts while making the overall suite fail.

Historical `cb277212` ordinary/stale/dependency captures and earlier fault captures
keep their original digests. The final source adds default test/CI wiring and
removes hardcoded synthetic labels from the general coordinator's facts. Evidence
classification remains explicit in the caller's record. Earlier continuity,
outage, replay, lifecycle and transition-fault evidence remains in the
[continuity record](router-continuity-run.json).

See the [fixture commands](../../experiments/router-continuity/README.md) for the
preparation and single-start path. The captures above remain synthetic. A separately
reviewed live deployment subsequently consumed one physical attempt and failed;
the [15 September live record](router-initial-live.md) preserves that result.
Live worker, planner and continuity acceptance remain pending. Two configured rows
and injected health state prove neither separate billing nor natural quota
exhaustion. Usage/headroom remain unknown; refresh and paid fallback remain denied.
Issue #9 still gates the durable application core.
