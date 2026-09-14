# Restricted planner experiment

Run with Node 24+; no provider key, router URL, Docker or private repository is used.

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
