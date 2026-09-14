# Live router authority feasibility

Observed 14 September 2026 for public 9Router 0.5.75 source at
`17c4cc76877bd1755030a8414f8d0083f48dcccf`. The
[reproducible source experiment](../../experiments/router-authority/README.md) and
[actual result](router-authority-run.json) establish a narrow integration gap.
They do **not** establish live router, authentication or provider conformance.

## Executable observations

| Probe | Actual observation | Consequence |
| --- | --- | --- |
| Mark one request pending; send no completion mark | Count is 1 at 0 and 59,999 ms, then 0 at 60,000 ms | Counter expiry cannot prove work has finished. |
| Wire the stream controller's disconnect callback as chat core does | Count becomes 0 immediately; abort signal remains false through 499 ms and becomes true at 500 ms | A zero count even precedes local transport abort. |
| Invoke the combo PUT and repository functions while a request is pending | Status 200; route changes from `fixture_old` to `fixture_new`; count remains 1 | This writer has no shared active-request gate at the inspected layer. |

The experiment executes unmodified, digest-verified
[usage tracking](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/src/lib/db/repos/usageRepo.js),
[stream controller](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/open-sse/utils/streamHandler.js),
[combo repository](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/src/lib/db/repos/combosRepo.js)
and [PUT handler](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/src/app/api/combos/%5Bid%5D/route.js).
The disconnect composition mirrors the callback in
[chat core](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/open-sse/handlers/chatCore.js).
Its synthetic clock, in-memory database and framework/import adapters are explicit.
The combo result is not a claim of an unauthenticated management vulnerability.

The recorded local run used Linux ARM64, Node 24.21.0, the pinned Node image in the
reproduction instructions and an operator-selected Docker context. Requested
controls were: network disabled, nonroot UID/GID 1000, all capabilities dropped,
no new privileges, read-only root/source/probe mounts, private cgroup/IPC, init,
0.5 CPU, 128 MiB memory/swap, 64 PIDs, 256 file descriptors and no core dump.
An outer 15-second command limit has a one-second TERM-to-KILL allowance.
The container exited 0 and a subsequent ownership-label query found no containers.
Appending a harmless comment to a disposable copy of the usage module produced
exit 1 with `source_digest_mismatch` and no result JSON, before any module was
evaluated. Its container was also removed. CI repeats this refusal case.
These requested settings and source-module results are not full worker-profile
conformance, real elapsed-minute testing, or proof of remote cancellation.

## Required live integration

The accepted [freeze-and-drain contract](../contracts/9router.md#atomic-policy-changes)
already requires stronger authority. The synthetic boundary from #3 satisfies it
only for its honest fixture. Stock 0.5.75 does not expose a proven live equivalent.
The harness and planner live criteria in #6/#7 remain open.

1. A trusted router-side integration must own one durable fenced generation for
   admission and all policy writers, conservatively draining the whole router
   first. Its sanitized snapshot must include the complete effective fallback
   graph: aliases, provider nodes, endpoint/billing classes, capacity defaults,
   helper calls and settings consulted during retries. Freezing only a named
   combo is insufficient: [chat](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/src/sse/handlers/chat.js)
   reads settings again during execution and the
   [capacity adapter](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/open-sse/services/capacityAdapter.js)
   can add candidates, including a default model.
2. The same fence must govern repository/import writes and alternate management
   paths. Process, storage and management ingress isolation must prevent other
   writers from bypassing it. Existing
   [imports](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/src/lib/db/index.js)
   rewrite configuration independently. A dashboard toggle or periodic hash check
   cannot enforce this rule or undo exposure after an out-of-band edit.
3. Trusted request IDs/revisions need upstream admission and durable receipts
   covering every executor subattempt. Bind evidence to boot/generation and
   policy revision. Unknown IDs, delayed ingress, crashes and missing records
   remain uncertain, never automatically quiescent. Stock router does not supply
   this meaning merely because Gaffer's boundary sends identifying headers.
4. Distinguish validated provider terminal, local transport termination and
   cancellation uncertainty. A genuine directly observed provider terminal can
   settle that operation; successful fallback does not settle earlier unknown
   operations. [Base](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/open-sse/executors/base.js)
   and [Codex](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/open-sse/executors/codex.js)
   executors have their own retries. Stop further retries on fencing/cancellation;
   retain `cancelled_unknown` until every relevant operation is reconciled.
5. Account choice, fallback, credentials, refresh and cooldown remain in 9Router.
   Permit credential/health writes only when the approved policy projection is
   unchanged; [refresh](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/src/sse/services/tokenRefresh.js)
   can also update project/provider metadata. Disable unrelated inference in the
   initial profile or account for it: optional
   [quota auto-ping](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/src/shared/services/quotaAutoPing.js)
   sends actual inference requests. Gaffer must not implement another account
   router or balance ledger.

A tightly controlled stock-router profile may support a limited feasibility run
with exclusive ingress, process/storage ownership, audited configuration and
uncertainty retained. It cannot claim automatic cancellation recovery or hot
policy replacement without the missing evidence. Verified router termination
alone does not prove remote stop; recovery also needs provider-side bounds or
terminal evidence. This experiment implements neither deployment nor extension.

## Optional configuration copy

The [migration procedure](../contracts/9router-migration.md) remains operator
selected and independent of any personal host. Starting another instance after
copying credentials may initiate background refresh from its
[server](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/custom-server.js)
or [bootstrap](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/src/shared/services/initializeApp.js).
Treat that as a migration/cutover step with session consequences, not an inert
copy. No credentials were inspected/exported/copied, existing settings changed,
second service started or provider inference requested by this experiment.
