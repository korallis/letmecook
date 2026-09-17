# Gaffer owner CLI (provisional skeleton)

Build with `go build ./cmd/gaffer`. The command uses only the Go standard library.
This slice implements `help`, `version`, `identity keygen` and `identity self`;
no execution, publication or merge authority follows.

Globals work before or after the subcommand: `--endpoint`, `--cert`, `--key`,
`--daemon-fingerprint`, `--json`, `--message-id`, `--timeout` (30 seconds by default).
Corresponding environment defaults are `GAFFER_ENDPOINT`, `GAFFER_CERT`,
`GAFFER_KEY`, `GAFFER_DAEMON_FINGERPRINT`, `GAFFER_JSON`, `GAFFER_MESSAGE_ID` and
`GAFFER_TIMEOUT`. Explicit flags override environment defaults. Keep credentials
outside repositories and worker sandboxes.

```sh
gaffer identity keygen --cert owner.crt --key owner.key
gaffer identity keygen --server --host 127.0.0.1 --cert daemon.crt --key daemon.key
gaffer identity self --endpoint https://127.0.0.1:7443 \
  --cert owner.crt --key owner.key --daemon-fingerprint "$daemon_pin" --json
```

Keygen creates an Ed25519 PKCS#8 key (0600) and a self-signed leaf (0644) and prints
its SHA-256 DER fingerprint. It never overwrites either file. Default validity is
30 days (`--days 1..365`); `--name` is only a display name. Client leaves have
CA:false, digitalSignature and clientAuth. `--server` uses serverAuth instead and
requires one or more `--host` DNS/IP SANs. Bootstrap the owner separately using
[gafferd's local identity command](../gafferd/IDENTITY.md).

`identity self` uses TLS 1.3, the exact operator-supplied daemon leaf pin, certificate
validity/serverAuth and endpoint hostname verification. It never follows redirects,
uses an environment proxy, or falls back to unverified TLS. A fingerprint is an
explicit trust anchor, not automatic trust-on-first-use.

`--json` emits one stdout envelope with `version: "gaffer-cli-v1"`, `command`,
`message_id` and `result`; errors go only to stderr as `{version,error:{status,code,detail}}`.
An omitted message ID is generated once for the invocation. Exit codes are 0 success,
1 refused/conflict, 2 usage, 3 unavailable and 4 reconciliation required.

## Planned commands

Later slices populate the `commands` map from their own files:

- `identity invite|enroll|update`
- `repo register|validate|show`, `runner facts import|show`
- `task create|approve|dispatch|inspect|watch|stop|retry|list`
- `attempt cancel|inspect|stream|artifacts`
- `verify run|show`, `review accept|reject|show`
- `daemon status|pause|resume|stop-all`, `reconcile status|release`
- `backup create|verify`, offline `restore`, and `flow run`

These planned commands are not implemented or advertised as accepted operations.
