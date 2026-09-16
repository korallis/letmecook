# Provisional owner and runner identity (#13)

Operator-authorized reversible work on the merged #11 store. Related to #13;
closure is deferred until #9/#10 are accepted/locked and reconciled. This supplies
neither foundation lock, supported execution runtime, execution readiness nor any
lease, grant, scheduler, process, model or artifact authority. Enabling a runner is
not eligibility: see [repository profiles](../../docs/operations/repositories.md)
for candidate project/root allowlists and the
[admission service](../../internal/scheduler/README.md#trust-and-eligibility) for
placement checks and their enforcement limits. Grants belong to the
[authority service](../../internal/authority/README.md).
No browser sessions, SSO or multi-user roles.

## Trust and local setup

`internal/identity/` owns certificate validation, pins and secret generation;
`internal/store/identity.go` owns schema 3 and transactional identities;
`internal/httpapi/identity.go` owns the closed HTTPS control API. Existing
`schemas/readapi/` retains the read projection. No parallel daemon/store exists.

Each operator-selected machine generates its own client key and self-signed leaf
certificate. Client certificates need digitalSignature key usage, clientAuth EKU,
CA:false, current validity and one leaf (no chain). Use Ed25519 or modern RSA/ECDSA.
An exact SHA-256 DER certificate pin identifies a credential, not the certificate
subject, IP address, network membership or claimed UUID. mTLS CertificateVerify
proves private-key possession. Unknown pins can only redeem a one-use invitation
already bound to that same pin. Every other request needs a non-revoked stored pin.
The client separately verifies the configured daemon certificate chain and hostname;
never disable verification, use `curl -k`, or follow redirects with credentials.

The server uses TLS 1.3, disables session tickets, requires a client leaf and checks
validity/EKU on each handshake and request. Authorization and revocation are checked
on every request, including existing keep-alive connections. No TLS termination
proxy/forwarding exception exists. This is HTTPS `http/1.1`, **not** the execution
contract's `execution-provisional-v2` ALPN; O2/O6 execution reconciliation remains open.

1. Generate the owner key outside any repository/worker sandbox in a private
   directory. With OpenSSL 3, for example (repeat on each selected runner using
   runner-specific filenames; do not copy private keys between machines):

   ```sh
   umask 077
   openssl req -x509 -newkey ed25519 -nodes -days 30 \
     -subj /CN=local-owner -keyout owner.key -out owner.crt \
     -addext basicConstraints=critical,CA:FALSE \
     -addext keyUsage=critical,digitalSignature \
     -addext extendedKeyUsage=clientAuth
   ```

2. Stop only your daemon. Bootstrap once, locally, with its existing private state
   paths and the public owner certificate. This takes the same exclusive store lock,
   commits the owner and exits without a network listener. It stores only the pin;
   the private key is never read by the daemon. Repeated bootstrap fails, including
   after revocation/restart. The private state directory's OS owner is the local
   bootstrap authority; same-UID compromise is outside this boundary.

   ```sh
   .local/gafferd --state-dir "$state" --artifacts-dir "$artifacts" \
     --bootstrap-owner-cert "$credentials/owner.crt"
   ```

3. Supply a server certificate/key from your PKI, with serverAuth EKU and SAN matching
   your configured endpoint. A dedicated self-signed certificate trusted explicitly
   by each client also works. No built-in CA, host, maintainer identity, discovered
   machine, HOME/environment configuration or private-network trust exists. Keep the
   key mode 0600 and its directory private. Start HTTPS explicitly:

   ```sh
   .local/gafferd --state-dir "$state" --artifacts-dir "$artifacts" \
     --listen "$selected_ip:$port" --endpoint "https://$selected_name:$port" \
     --tls-cert "$credentials/server.crt" --tls-key "$credentials/server.key"
   ```

   All TLS flags are required together. Endpoint is an HTTPS origin without userinfo,
   path, query or fragment; its hostname must match the server certificate. HTTP Host
   must equal that origin's authority. The endpoint must reach the chosen listener;
   no DNS discovery/port remapping is supplied. Authenticated stores refuse plaintext
   startup. Historical unbootstrapped loopback demos remain synthetic-data-only.

4. Verify owner identity with an explicit trust file and client credential:

   ```sh
   curl --fail --cacert "$credentials/server-trust.pem" \
     --cert "$credentials/owner.crt" --key "$credentials/owner.key" \
     "$endpoint/api/v1/identity/self"
   ```

   A wrong server CA/name, missing client key, expired certificate, wrong/revoked
   client pin or old TLS version fails closed. There is no authentication fallback.

## Bounded control API

Responses under `/api/v1/identity/` carry `version: "identity-provisional-v1"`.
Mutation bodies require that version, a canonical UUIDv4 `message_id` and exactly
the fields below; unknown, duplicate or missing fields, null, invalid UTF-8,
trailing values and bodies over 2048 bytes reject.
Content-Type must be `application/json`. No query parameters,
encoded paths, Origin, Cookie, Authorization, forwarding or cross-site headers.
Existing request concurrency/time/header bounds apply. All responses are no-store;
errors use fixed codes without reflected input or driver/path detail.

| Interface | Scope and fields beyond `version`, `message_id` |
| --- | --- |
| `GET /api/v1/identity/self` | Any current owner/runner pin. No body. Returns `id`, `role`, `enabled`, `revoked`, `revision`. Disabled runners may identify themselves, not execute or mutate. |
| `POST /api/v1/identity/enrollments` | Owner only: `fingerprint` (64 lowercase hex SHA-256 DER of selected runner certificate). Returns `id` (message ID), `runner_id`, `token`, `expires` (Unix seconds). |
| `POST /api/v1/identity/enroll` | Selected runner's mTLS leaf plus `token`. Atomically consumes invitation and creates disabled runner bound to its preallocated stable ID. |
| `POST /api/v1/identity/update` | Owner only: `id`, `revision`, `action`, `fingerprint`. Actions: `enable`, `disable`, `revoke`, `rotate`. Fingerprint is empty except for rotation. |

Store read routes retain their separate [read contract](README.md#read-contract-for-95),
including response version, query parameters and HTTPS owner authorization.

For example, an invitation body (substitute a real fresh UUID and public pin):

```json
{"version":"identity-provisional-v1","message_id":"00000000-0000-4000-8000-000000000001","fingerprint":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}
```

Obtain a runner certificate pin locally with:

```sh
openssl x509 -in runner.crt -outform DER | openssl dgst -sha256
```

Verify this public pin over an operator-controlled channel before issuing the invite.
Transfer the secret invitation only to that chosen machine through a protected
channel. Submit JSON via `curl -H 'Content-Type: application/json' --data-binary @request.json`
or protected stdin, with explicit `--cacert`, `--cert`, `--key`, without verbose/trace
flags; do not put secrets in arguments, URLs, shell history, logs, model prompts,
repository files or artifacts.
Capture the invitation response into a private 0600 file outside repositories rather
than terminal/session logs. Enrollment token is 256 random bits, expires after ten
minutes, and is stored only as SHA-256. It is never a reusable login password. Only
the intended enrollment response contains plaintext; daemon logs/errors/artifacts
never do. Default Go formatting of token-bearing values redacts tokens.

Invite `message_id`, runner ID, credential pin and consumed rows are retained as
replay tombstones. Duplicate invitation issuance returns conflict, not the secret.
Wrong certificate/token does not consume an invitation. Concurrent double enrollment
has exactly one winner. Expired/consumed tokens fail. Fresh enrollment is disabled;
owner must explicitly enable using current revision. No pin may be reassigned to a
different identity, even after revocation. Certificate renewal is explicit rotation.

Update uses revision CAS, incremented once in the same transaction as credential
revocation/rotation. An old revision conflicts, so replay cannot reapply mutation.
No generic message-response cache retains secrets. Lost invitation response requires
a fresh certificate/pin and message ID after abandoning that invite; lost enrollment
response can be reconciled via runner `self`. Lost update response can be reconciled
by target `self` with its current credential (or fails closed if revoked). There is
no runner inventory UI/API in this slice; retain safe IDs/revisions in local operator
records. Expired unused invitations remain tombstones; pruning is deferred.

## Rotation, revocation and recovery

- **Runner rotation:** generate a new key/certificate on that runner, securely verify
  its pin, then owner posts `rotate` with stable runner ID/current revision/new pin.
  Old credential stops working immediately after commit, including on open TLS
  connections; new pin retains the same ID and enabled flag. The new pin must never
  have been used or reserved by another invitation. Do not export keys to workers.
- **Runner revoke:** owner posts `revoke` with empty fingerprint. Identity becomes
  revoked/disabled permanently; no later enable resurrects it. This does not prove
  process termination or remote work quiescence. Replacement enrollment gets a new
  identity and starts disabled. `disable` is reversible and does not revoke login.
- **Owner rotation/revocation:** same CAS actions on owner ID. Rotation invalidates
  the old owner key immediately. Revoking the only owner deliberately requires local
  recovery; no runner can become owner over HTTP. Enable/disable apply only to runners.
- **Lost owner, unknown commit outcome, or suspected compromise:** stop only your
  daemon, generate a new never-used owner key/certificate, then run the local command
  below. Recovery keeps owner ID but revokes **all** old owner/runner credentials,
  disables/revokes runners and consumes every pending invite in one transaction.
  Re-enroll selected runners with fresh certificates; enable each explicitly.

  ```sh
  .local/gafferd --state-dir "$state" --artifacts-dir "$artifacts" \
    --recover-owner-cert "$credentials/replacement-owner.crt"
  ```

- **Server key rotation:** stop owned daemon, replace server cert/key, securely
  distribute the new explicit trust material, restart and verify hostname/CA.
  There is no insecure retry or unattended trust-on-first-use.
- **State loss/restore:** copying an old DB can resurrect old authorization state.
  Backups/restores remain unsupported (#23); do not serve restored copies. Keep
  offline, rotate server/owner trust and fence all previous identities before any
  future qualified restore procedure. Local identity recovery alone proves no
  execution-generation fencing or process death. DB files contain sensitive public
  identity metadata/hashes and must remain private even though private keys/tokens
  are not persisted. No provider/publication credentials belong in this store.

## Observed local checks

`go test -race -count=1 ./...` executes disposable Ed25519 mTLS peers, real HTTP,
SQLite transactions and the CLI: bootstrap replay, double enrollment, pin/token
mismatch, expired invitation, revoked/rotated credentials on keep-alive connections,
TLS CA/hostname/client/version failures, owner/runner scope, default-disabled/CAS,
malformed bodies, redaction, restart persistence, migration, fixture refusal and
transaction rollback. Tests also inspect actual persisted output for token leakage.
No live inference, real repositories, private networks, production credentials,
external machine enrollment, physical power-loss or supported worker runtime tested.
