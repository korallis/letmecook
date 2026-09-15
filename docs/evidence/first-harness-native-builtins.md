# Native OpenCode builtin and ambient-state audit

This source audit concerns the pinned OpenCode 1.18.30 release path at
`3104c1428ec91f809e5ab86631300de41eb6952e`, plus actual adapter controls. It is
not a claim that PURE disables all startup behavior.

`packages/opencode/src/plugin/index.ts` constructs twelve builtins: Codex,
Copilot, Modal, GitLab, Poe, Cloudflare Workers, Cloudflare Gateway, Azure,
DigitalOcean, Snowflake, xAI and Cerebras. PURE excludes external plugin origins;
it does not skip this list. `DISABLE_DEFAULT_PLUGINS` does skip it. The selected
profile keeps the builtins enabled because the Codex plugin's `chat.params`
hook removes `maxOutputTokens` for provider `openai`. Its `chat.headers` hook
adds the observed OpenCode originator/session metadata. These hooks operate
without OAuth. The exact binary capture demonstrates both effects; the disabled
control emits cap 128 and is refused by the adapter's unchanged metadata guard
before any original send.

The same Codex plugin registers OAuth login, refresh and fetch rewriting, and an
optional WebSocket pool. Those paths are distinct from the unconditional params
and headers hooks: auth loading requires stored auth, OAuth rewriting requires
OAuth, and WebSockets require the experimental option. The worker has fresh
private home/XDG directories, no mounted auth stores, no parent credentials,
explicit `experimentalWebSockets:false`, native LLM false and no auth command.
`provider/provider.ts` loads stored plugin auth when present; an enabled-provider
list alone is not an auth-discovery boundary. Admission therefore rejects known
auth files before launch, independently of the provider list.

The remaining builtins register provider-specific hooks or login callbacks.
Azure checks whether `az` exists on the fixed PATH during initialization; CLI
token acquisition happens inside its OAuth callback/fetch hook. Modal model
discovery requires its token and endpoint. Copilot, DigitalOcean, Snowflake and
xAI fetch/refresh paths depend on their provider/auth state. Cloudflare registers
API-key prompts. Cerebras changes params only for its SDK. No such provider,
credential or environment variable is supplied by this worker.

The two bundled npm plugins were inspected as data, without installation or
lifecycle execution, and their tarball SHA-512 integrity matched the exact
source `bun.lock`: `opencode-gitlab-auth@2.1.0` (tarball SHA-256
`1574687b2729fa0a61c1006a79df092d380cc83709ecfacc5aa8ed5c8fb89bb3`) and
`opencode-poe-auth@0.0.1` (`b9254082be81266d45e3c43692726141c0e1b9ea40d245fe68ebc1c5ea5fb2ab`).
Their entrypoints register auth hooks. GitLab can write its debug log/auth file
and refresh tokens from its auth callbacks; Poe starts browser OAuth through its
authorize callback. The selected run invokes neither login path. This inspection
does not constitute a recursive audit of every transitive import's initialization.

Configuration is also independent of PURE. The pinned config loader merges
global, explicit, project and inline sources and can schedule dependency installs
for discovered config directories. The adapter stages a disposable repository,
rejects ambient repository entries and active Git hooks, supplies only its explicit
environment, disables project config/external skills/Claude discovery/model fetch/
LSP download/updates/compaction/file watching, and sets empty MCP/plugins/
instructions, disabled sharing/formatting/LSP/telemetry. Ambient plugin and dummy
OAuth canaries are rejected before starting the binary. Inherited dummy provider,
proxy and telemetry variables are absent from the actual child environment.

The enforceable backstop is the measured OS topology: network none; read-only
client/runtime mounts; private tmpfs; nonroot/no capabilities/no-new-privileges;
only the scoped inference UDS; no router source, private controls, host home or
credentials in the worker. Actual containment and transport negatives cover
direct router/provider access, management paths, altered headers and absent
control mounts. Successful runs account for all allowed original sends and use
no external provider. They do not measure every rejected startup network attempt,
and this offline evidence does not prove a future live deployment's egress rules.
