# Harness adapters

`harness.Harness` is a supervisor-owned compatibility seam, not launch authority.
Adapters receive `RunRequest.Launcher`; workers receive a scoped inference URL and
token, never gateway/provider credentials. The supervisor owns lease fencing,
spooling before acting on events, guardian termination, custody and finalization.

This M1 implementation is provisional. `macos-sandbox-exec-dev` is the only
operator-authorized code-execution profile on this Mac, explicitly enabled,
`qualification: development`, `supported: false`, and refused by default.
Tests use explicitly test-only process-group launchers. Neither the OpenCode
adapter nor its config-isolation probe qualifies an OS sandbox or authorizes
unattended execution. The fake adapter and its execution modes belong to the
runner-supervisor lane, not this adapter.

## OpenCode 1.18.31

Construct `opencode.New(absoluteBinaryPath)`, supplying the operator's
`--opencode-bin`. It rejects non-executable, group/world-writable or symlink binary
files and pins SHA-256 in `Descriptor.BinaryDigest`. `Describe` names the intended
version; only `Probe` verifies the binary's `--version` result. Probe evidence is
in-memory, per adapter/protocol/model, and a changed binary invalidates it.
`Start` refuses without a successful probe and rehashes the binary before launch.
No resume, sandbox implementation or execution permission is inferred.

Prepare disjoint workspace, private HOME, temp and runtime directories. HOME,
temp and runtime must be empty private directories (0700), not links or nested
inside candidate content. Existing private-home configuration is refused, not
merged. Start writes an exclusive, fsync'd 0600 `RuntimeDir/opencode.json`, outside
the candidate:

- one `gaffer` provider, exactly the selected approved model;
- `@ai-sdk/openai-compatible` for Chat, `@ai-sdk/openai` for Responses, or
  `@ai-sdk/anthropic` for Messages;
- loopback-only base URL ending `/v1` and `apiKey: "{env:GAFFER_ATTEMPT_TOKEN}"`;
- `model: "gaffer/<model>"`, `permission: {"*":"allow"}`, `share: "disabled"`,
  `autoupdate: false`.

The token value is not written to configuration. Arbitrary adapter settings,
additional providers, plugins, endpoints or model fallback are not accepted.
The route's first target is the selected model and must be in the boundary scope.

The exact argv is:

```text
<opencode-bin> run <brief> --format json -m gaffer/<model> --dir <workspace> --auto --pure --print-logs
```

The complete, non-inherited environment is:

```text
PATH=/usr/bin:/bin
HOME=<PrivateHome>
XDG_CONFIG_HOME=<PrivateHome>/.config
XDG_DATA_HOME=<PrivateHome>/.local/share
XDG_CACHE_HOME=<PrivateHome>/.cache
TMPDIR=<TempDir>
OPENCODE_CONFIG=<RuntimeDir>/opencode.json
GAFFER_ATTEMPT_TOKEN=<scoped-token>
TERM=dumb
NO_COLOR=1
OPENCODE_DISABLE_AUTOUPDATE=1
OPENCODE_DISABLE_MODELS_FETCH=1
OPENCODE_DISABLE_PROJECT_CONFIG=1
OPENCODE_DISABLE_EXTERNAL_SKILLS=1
```

The last two controls are a measured, coordinator-authorized deviation from the
integration record's original recipe. **OpenCode 1.18.31 loads repository
`opencode.json` under the prescribed environment despite `--pure`.** A contained
probe exited 0 and made two requests to an unexpected fake-gateway path after a
repository config changed the provider base URL. No operator gateway was used.

Real-binary ablation proved `OPENCODE_DISABLE_PROJECT_CONFIG=1` necessary for
repository config/agent/MCP discovery and `OPENCODE_DISABLE_EXTERNAL_SKILLS=1`
necessary for repository external skills. Removing either failed the gate.
`OPENCODE_DISABLE_CLAUDE_CODE` and `OPENCODE_DISABLE_DEFAULT_PLUGINS` were tested,
proved redundant in this recipe and removed. The remaining controls are listed
by `ConfigControls()` and in `ProbeResult.Limitations` (the shared Descriptor has
no limitations field). `--pure` still suppresses external plugins, and a fresh
HOME/XDG prevents ambient user configuration from entering the process.

## Mandatory config-isolation gate

`Probe` requires an **empty disposable** workspace and private directories. It
plants hostile user/repository `opencode.json`, `.opencode/opencode.json`, plugin,
MCP configuration, tools, agents, AGENTS/CLAUDE instructions and skill files. It
runs `--version` and a real JSON-mode run through the supplied launcher against
its own fake loopback gateway. It verifies:

- exact version, expected provider protocol/path/model and authentication;
- no unexpected tool or hostile instruction marker in the request;
- successful normalized completion;
- no plugin/MCP execution markers;
- no literal scoped token in output, config, workspace, caches, logs or database.

The probe never uses the operator gateway. Tests put hostile HOME/XDG/config in
the ambient environment as well, proving the adapter uses its separate clean
HOME. A failed probe clears the affected gate and Start remains refused. Failure
reports contain fixed limitation text, never request bodies or endpoint secrets.

An attempt-specific sandbox cannot know the probe port in advance. A supplied
probe launcher can implement this optional structural interface:

```go
WithBoundary(addr string) (isolation.Launcher, error)
```

Here `addr` is `127.0.0.1:<port>`, not a URL. Immediately after opening the fake
gateway, Probe calls this method, uses the returned launcher and cleans it up.
The runner can clone its development profile with this port and the disposable
probe workspace. Test launchers without this interface are used as supplied.
A launcher that cannot reach the probe endpoint fails closed. Successful config
probe evidence is independent of OS isolation evidence; the real run still
requires its own appropriately qualified launcher.

## Events and lifecycle

Commands are built with `exec.Command`, **not** `exec.CommandContext`. Env, argv,
Dir, stdout/stderr pipes are set before `Launcher.Wrap`; Wrap is the last mutation
before `cmd.Start`. The adapter never subsequently changes Stdin, ExtraFiles or
SysProcAttr, so a guardian launcher can own its lease/control channels. The runner
may replace handle PID/PGID/GuardianPID with its guardian's observations; adapter
ID/SessionID remain local stream keys.

`ParseEvent` maps step starts, text, tool names/status, finish reasons, unknown
kinds and token observations. The first step start is `started`, later starts are
activity; pending/running tools are `tool_requested`; stop completion is held
until process exit and both pipes reach EOF. Nonzero exit cannot become success.
Approval events are surfaced, not silently bypassed. Token counts include cache
reads/writes and are labeled `opencode_tokens`, not authoritative billing.

Raw JSON is retained up to 48 KiB per event; larger records have an explicit
bounded truncation envelope. Stderr becomes native events whose Raw is a JSON
string retaining its line endings. The supervisor can decode that string when
spooling native bytes. Literal scoped-token output is redacted before events
leave the adapter. Total stdout/stderr is bounded by `MaxStdoutBytes`; malformed
or overflowing output emits `failed` and continues discarding/draining until the
supervisor cancels. A failed event is not proof of process termination.

Events have monotonic local sequence numbers and can be read after a sequence.
A terminal event is followed by EOF. Events are retained in this adapter object;
create/discard adapters with the supervised run lifecycle rather than treating
this memory as a restart journal.

`Cancel` is an observed-pgid fallback: TERM, grace (2 s), KILL, then bounded
observation (5 s). It does not signal the guardian's own group, treats EPERM as
unknown rather than empty, and avoids killing a potentially reused group after a
completed handle. `RemoteWork` stays unknown: only `Boundary.Close/State` plus
durable receipts can establish inference quiescence. Descendant/tree containment
and supervisor-death protection remain the runner/guardian's responsibilities.

## Evidence

`tests/fixtures/harness/opencode-1.18.31/events.jsonl` contains 12 captured events
with absolute repository paths replaced by `/work/repo`. Parser goldens cover
all samples, including tool edits and cached usage. No deployed host or credential
is present.

`go test -race -count=1 ./internal/inference ./internal/harness/opencode` runs real
subprocess lifecycle/TERM/KILL tests, exact environment/argv tests, launch refusal,
credential and token absence scans, protocol-specific config generation, and the
real binary's hostile-config probe when available at `GAFFER_OPENCODE_BIN` or the
operator's default OpenCode install path. Absence of the binary is an explicit
skip, not a conformance pass.

On this Mac, all three protocol probes passed with OpenCode 1.18.31 SHA-256
`16c960ba77421da11b53e785f359b73f328a86118b48feb4af143db5d9afb198` and the two extra
controls above. The original prescribed environment was also tested and refused.
No supported runtime, provider cancellation, original downstream acceptance or
#9/#10 acceptance follows from this result.
