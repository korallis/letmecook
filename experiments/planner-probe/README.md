# Restricted planner experiment

Run the original schema-1 fixture with Node 24+; it needs no provider key, router URL, Docker or private repository. The separate actual-router synthetic mode uses the accepted pinned Docker gateway and a separately staged planner consumer.

```sh
npm ci --prefix experiments/planner-probe
npm --prefix experiments/planner-probe run check
npm --prefix experiments/planner-probe test
npm --prefix experiments/planner-probe run demo
npm --prefix experiments/planner-probe run prove
node experiments/planner-probe/capabilities.ts
```

The last command exits **2** because live admission is blocked. `prove` records
successful synthetic tests separately from that live gate; it never marks #7
complete. Supply a different artifact path with `node
experiments/planner-probe/prove.ts /absolute/path/result.json`.

The host snapshots the pinned public `fixture.txt`, creates a synthetic scoped
planner grant through the accepted inference boundary, and runs one bounded
assessment. A complete `read_file` tool call can receive that snapshot once. Text
is validated against [the explicit plan schema](../../tests/fixtures/planner/plan.schema.json)
after clean stream completion. At most one separately charged repair is allowed.
No returned proposal is executed or granted acceptance/publication/merge authority.

See [the evidence and live prerequisites](../../docs/evidence/planner-probe.md).

For the actual-router synthetic proof (accepted #77/#79 required):

```sh
GAFFER_BRIDGE_ROUTER_SOURCE=/absolute/path/to/pinned/public/9router \
  npm --prefix experiments/planner-probe run prove:router -- /absolute/path/result.json
```

The source-verifying loader, image/runtime pin and Docker isolation checks are
mandatory. See the evidence page for exact versions and optional case selection.
`RouterAuthority` supplies the durable receipt join; the real `PlannerSession`
retains bounded read-only discovery and proposal-only authority. Native translated
Chat is a synthetic diagnostic with no provider generation bound or consumer
Responses codec. Live model/settings, deployed conformance and #7 remain incomplete.

Native Responses has a separate closed read-only profile, native codec and
production boundary port. `check:native` and `test:native` validate the consumer
and shared protocol; `prove:native` remains an explicitly in-memory proof.
`prove:native-router` runs the actual pinned router failure matrix and
`prove:native-gateway` runs the staged consumer against the production gateway's
private evidence and immutable proposal acknowledgement controls. Neither command
calls a real provider. See [native integration and evidence](native-integration.md)
for commands, current official parameter notes and the outstanding live gate.
