# Public synthetic baseline case01

On September 15, 2026, both native mechanics scenarios passed at source commit
`df137b280cb3a56046e0cfe818767205bdc01314`. The actual pinned OpenCode binary and
9Router runtime executed in isolated Linux ARM64 containers against a fake
original HTTP server. No real provider or private baseline case was called.

[Retained evidence](baseline-native-case01-run.json) includes complete canonical
record closures for both runs: frozen registration, prepared packet, invocation,
native observations, physical requests, durable receipts, transcript inputs and
verification, full base/candidate snapshots, check observations, acknowledgement,
and measurement export. Source manifests and registration Git commit identities
are retained. The coordinator verified those local Git blobs before packaging.
The six failed development runs remain listed with their original journal digests;
their local evidence and state volumes were preserved.

| Observation | Two-request case | Three-request case |
| --- | --- | --- |
| Physical requests, all joined to durable scope debits | 2 | 3 |
| Tool rounds | 1 | 2 |
| Existing files changed; complete bytes retained | 2 | 2 |
| Base pin / candidate pin checks | failed / passed | failed / passed |
| Base audit / candidate audit checks | failed / failed | failed / failed |
| Repository destroyed, saved candidate reconstructed | passed | passed |
| Worker/gateway cleanup | passed | passed |
| Real baseline denominator | 0 | 0 |

The failed audit is a frozen invented advisory, preserved across revisions. These
fixture checks do not execute upstream workflows or establish a real advisory
result. The measurement record is `failed`, with no human acceptance label and
unknown operator effort, usage and costs; mechanics success does not satisfy #1.

Validation observed by the coordinator: 185 unit tests before capture packaging,
19 actual-container checks, TypeScript, documentation build/check (5 documents,
127 anchors), and 57 invalid fixture claims rejected. The container checks cover
read-only inputs, denied egress/control access, deadlines, output floods,
descendants, abnormal exits, abort and missing cleanup acknowledgement. The added
`capture.test.ts` reconstructs and requalifies the retained captures offline; CI
runs it with the other baseline tests. Container tests require the pinned image
and are run separately.

The local acknowledgement joins all required bytes and evidence within the bound
scope. Receipt debit fields come through the existing router authority projection;
no new account routing or raw database access was added. Filesystem tests cover
sync failures and substitution; they do not prove power-loss survival. The selected
Docker context is an operator choice, and no Matilda dependency is introduced.

See [PR #64](https://github.com/korallis/letmecook/pull/64) for delivery status.
Real baseline registration, live evidence, operator labels and follow-up remain
incomplete. The original live request with
unknown outcome is unchanged; these synthetic runs grant no live replacement.
The M0 foundation decision, application UI, StyleX and React Doctor remain later
work required by the issue plan.
