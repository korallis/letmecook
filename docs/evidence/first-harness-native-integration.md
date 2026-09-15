# First harness: native adapter integration

Issue #6 / draft PR #75 remains incomplete pending live evidence and final review.
This record concerns actual local execution of the pinned native OpenCode adapter
through the shared boundary and patched public 9Router with synthetic originals.
No provider call, credential read, real initial scope, deployment or publication
is part of these runs. The earlier Chat evidence and native preparation capture
remain historical and retain their original scope.

The later [legacy closure correction](first-harness-legacy-closure.md) retains a
fresh-review Chat startup failure and the affected current-source replays. It
extracts pure scope validation without changing authority behavior; native edit
and active cancellation are rechecked separately from the original matrices.

The native worker and gateway each use the measured 768 MiB profile. The adapter
pins OpenCode 1.18.30, source/binary/settings, `openai/@ai-sdk/openai`, Astra xhigh,
summary auto, store false, encrypted continuation, enabled builtins, PURE, no
external plugins/OAuth/native LLM/WebSockets, and the captured narrow apply_patch.
Provider generation-token and monetary caps remain unavailable.

Observed evidence is retained in three separate source-bound packets:

- [Core matrix](first-harness-native-integration.json): nineteen cases at
  `c3677b2ca8889d29bf4d39e55f9fa098fffbe4a1`, with accepted main `2749a990…`
  reconciled and planner-specific code excluded.
- [Default entry lifecycle](first-harness-native-entry-lifecycle.json): eleven
  affected cases at `c2bddfe54ce30ba3cad0ce89d4bb9742979f5090`, using the normal
  worker entry and a deterministic delayed Docker start.
- [Final stop/readiness lifecycle](first-harness-native-final-lifecycle.json):
  delayed start, edit, approval rejection and active container SIGTERM at
  `e4aa8518d7189588a3eda4695cc8f11f97b1d336`. Observation is emitted after relay
  closure and readiness for prompt teardown. Active cancellation skips the hold,
  exits 0, retains unknown original work, and acknowledges no artifact.

All selected checks passed with owned resources removed and every recorded source
hash verified after execution. The packets retain exact raw-input hashes and
collector identities. They are distinct runs, not one invented final-head replay
of every earlier scenario. [Development failures](first-harness-native-development.json)
remain labelled separately. Root's independent startup-race finding led to waiting
through Docker's created state and retaining state transitions at failure; the
new delayed-start case observed created state and completed successfully.

Native typecheck and twelve native/evidence unit tests pass; the preserved Chat
adapter's typecheck/fourteen tests, boundary typecheck/fifty-four tests, shared
native typecheck/twenty tests and router syntax checks also passed during this
integration. Documentation build/check pass. The CI harness workflow now runs the
native typecheck/tests; the platform-specific Docker evidence remains a separate
actual local proof.

The private supervisor prepares a synthetic consumer registry before starting
its synthetic scope, obtains the shared scoped grant, runs the fixed public
`greeting.txt` edit, and reads its artifact independently after the binary exits.
The candidate envelope includes the deployment packet/scope, policy/binding,
ordered request/decision/original-receipt identities and native tool/result/final
links. Shared immutable storage acknowledges the complete envelope. The supervisor
checks retry idempotency and reads the acknowledged file after gateway shutdown.
These are candidate artifacts; no acceptance, publication or merge is implied.

The core matrix exercises nineteen cases: edit, delayed original EOF,
blocked CLI approval, registry selection, cancellation, crash, detached process
tree, receipt write failure, decision write failure, candidate write failure,
partial output, forbidden patch path, router errors/retries, disabled builtin
hooks, ambient config/plugin, dummy OAuth, containment, transport bypass and OOM.
Registry selection keeps the same spent count/deadline and refuses the old token
and binding. Each physical retry counts. The cgroup OOM probe records the kernel
oom_kill counter increment and killed child; whole-container teardown remains the
backstop for escaping descendants.

Decision persistence failure intentionally prevents normal gateway close: its
exit is 1, the final result file is absent, and quiescence is recorded unknown.
The tool and final candidate remain withheld. Read-only retention saves the
durable journal and immutable records before synthetic cleanup; this is evidence
of a closed failure, not a successful gateway shutdown. Candidate persistence
failure returns no acknowledgement despite the already completed toy edit.

Development failures were retained while correcting the launcher: a Docker
cleanup filter typo after a successful edit; missing final-result handling after
injected journal failure; disabled-hook refusal occurring in the strict metadata
guard before upstream capture; and an unreliable Docker OOM flag replaced by
direct kernel counters. A run accidentally attempted during an unresolved local
merge failed before gateway startup and cleaned up. None is reported as a passing
matrix. The original local JSON inputs remain available for independent review. A later
test-only missing Node option separator kept the delayed-start container in
created state until timeout; its corrected regression passed.

See the [adapter README](../../experiments/harness/native/README.md) for commands,
the [builtin audit](first-harness-native-builtins.md) for source observations and
limits, and the separate [preparation record](first-harness-native-preparation.md)
for the older captured fixture. The final public matrix packet is generated by
`collect-first-harness-native.ts`, which checks every recorded source hash against
both the working files and exact Git commit, preserves original receipts and
durable candidate records, and excludes duplicate logs and private host paths.

Remaining gates are the reviewed consumer suite, concrete deployment controls,
fresh independent final-head review and CI, and the approved live disposable task.
All intended public consumers and deployment must be prepared and reviewed before
the single initial maximum of ten physical attempts and 600,000 ms begins across
roles/retries. Separately approved baselines cannot reset that scope. Continuity
needs its own exact reviewed transport evidence; unchanged/no-tool output remains
artifact_mismatch in this worker adapter.

The current worker binary wall ceiling is 30,000 ms. Synthetic gateway fixtures
use request/first-output/idle limits of 6,000/4,000/2,000 ms; these are deterministic
fault-test timings, not measured live Astra xhigh thresholds. The exact reviewed
live suite must declare suitable finite request and consumer bounds within its
shared 600,000 ms scope before admission. No limits were widened or calibrated
through model calls in this integration.
