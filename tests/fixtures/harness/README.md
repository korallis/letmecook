# Public harness fixtures

`edit.native.jsonl`, `ask.native.jsonl`, `strict.native.jsonl` and `request.json`
are captured public synthetic OpenCode 1.18.30 run records from 14 September 2026.
Native records are preserved unchanged; no provider credential, private repository
or live model was used. The request fixture contains the harness's generated system
prompt and tool schemas as data, not instructions for this repository.

Source commit: `3104c1428ec91f809e5ab86631300de41eb6952e` at
[anomalyco/opencode](https://github.com/anomalyco/opencode). The exact release binary
and archive hashes are in `experiments/harness/pins.json`. The MIT license is
retained in `opencode-LICENSE.txt` for the copied prompt/tool material.

`gateway.ts`, `worker.ts` and `containment.ts` are Gaffer synthetic proof fixtures.
Only the host launcher may execute them inside the pinned containers. `ambient-probe`
is intentionally a negative raw-binary discovery probe after adapter admission has
refused that input; it records why flags alone cannot be trusted.

`ambient-probe` terminates when the harmless canary is observed. `ambient-timeout`
intentionally emits diagnostic stdout/stderr, then times out without creating the
canary. The worker must flush its `observation` before failing the discovery check;
the launcher verifies diagnostics remain available after the worker exits 1. A
`finished` marker is reserved for checks that actually pass.
