# Legacy harness staging correction

Fresh review found that native-policy static imports prevented the preserved
Chat gateway from starting at `e44f374d433667035cabc0d289df7d2a59a58673`.
The [failed observation](first-harness-legacy-closure-review-failure.json) retains
`ERR_MODULE_NOT_FOUND`, zero completed cases, the gateway's exit state, verified
cleanup and all 97 source hashes matched against that exact commit. Earlier
successful Chat matrices did not prove the newer launcher still worked.

The fix at `7315bcaf7dd25bc9fd2121f5b4e63db219a0d775` supplies the full static
dependency closure at the actual import paths. The scope validator is moved
unchanged into pure `scope-profile.mjs` and re-exported by `evaluation-scope.mjs`.
The actual-router worker stages only client files and pure profile validation;
it receives no scope authority, router source, SQLite, control server, private
state or provider credentials. A separate read-only gateway mount supplies the
pure Responses codecs. The default synthetic fixture stages those same codecs.
Existing Chat profiles, 128-token cap, edit/write tools, resource limits and
completion classifiers are unchanged.

Actual local execution against this frozen source produced:

- [Pinned-router Chat replay](first-harness-legacy-closure-router.json): seven
  selected cases passed: edit, write, approval rejection, decision persistence
  failure, partial output, cancellation and containment. The successful edit
  cases retain original receipts, durable decisions, exact wire/body settings,
  native events and artifact joins. All 100 recorded source hashes match the
  commit and were checked against working files after the run.
- [Default fixture replay](first-harness-legacy-closure-fixture.json): all thirteen
  cases passed, including ambient plugin discovery, the intentional diagnostic
  timeout, edit, approval, absent usage, partial/forbidden output, router error,
  cancellation, crash, process-tree teardown and OOM. Its 40 source hashes match
  the commit and working files after execution.
- [Native regression replay](first-harness-legacy-closure-native.json): edit and
  active default-entry cancellation at the same source. The native collector
  retains original receipts, durable candidate records and process outcomes.

Each replay removed owned containers and volumes. Isolated subprocess import
tests additionally check the staged worker and fixture closures; removing the
transitive scope validator makes the isolated worker import fail. Harness 16,
native/evidence 12 and shared scope/native 20 tests passed, with their respective
typechecks. This is synthetic, local evidence only. PR #75 stays draft; live
consumer/deployment review, suitable finite live timing, fresh final-head review,
CI and the approved disposable live task remain outstanding.

Reproduce using the public pinned binary, locked images and Docker profile from
the [harness README](../../experiments/harness/README.md). Set the following
absolute paths to public runtime inputs on the verification machine:

```sh
GAFFER_BRIDGE_ROUTER_SOURCE=/absolute/public/pinned-router \
GAFFER_HARNESS_BINARY=/absolute/public/pinned-opencode \
GAFFER_HARNESS_ROUTER_CASES=edit,write,ask,persist-failure,partial,cancel,containment \
node experiments/harness/router-run.ts /tmp/legacy-router.json

GAFFER_HARNESS_BINARY=/absolute/public/pinned-opencode \
node experiments/harness/run.ts /tmp/legacy-fixture.json

GAFFER_ROUTER_SOURCE=/absolute/public/pinned-router \
GAFFER_OPENCODE_BINARY=/absolute/public/pinned-opencode \
node experiments/harness/native/router-run.ts /tmp/legacy-native.json edit,entry-cancel
```

Run the collectors only after execution ends, from the frozen runtime source.
They verify recorded hashes against exact Git blobs; successful runs also require
unchanged working files. The legacy collector retains complete observations,
including failures, with the original raw-input digest and collector hash.

```sh
node docs/evidence/collect-first-harness-legacy.ts /tmp/legacy-router.json /tmp/router-packet.json 7315bcaf7dd25bc9fd2121f5b4e63db219a0d775 router
node docs/evidence/collect-first-harness-legacy.ts /tmp/legacy-fixture.json /tmp/fixture-packet.json 7315bcaf7dd25bc9fd2121f5b4e63db219a0d775 fixture
node docs/evidence/collect-first-harness-native.ts /tmp/legacy-native.json /tmp/native-packet.json 7315bcaf7dd25bc9fd2121f5b4e63db219a0d775
node docs/evidence/collect-first-harness-legacy.ts /tmp/review-failure.json /tmp/failure-packet.json e44f374d433667035cabc0d289df7d2a59a58673 review-failure
```
