# Passive 9Router status experiment

Implements issue #4; consumes the accepted
[9Router contract](../../docs/contracts/9router.md) from issue #2. This is a
standalone backend experiment, outside the undecided application core. It does
not enroll a host, configure accounts, select a provider or claim a ready route.

## Run

Node.js 24 or newer:

```sh
npm --prefix experiments/router-status ci --ignore-scripts
npm --prefix experiments/router-status run check
npm --prefix experiments/router-status test
npm --prefix experiments/router-status run demo
```

The demo uses an in-memory synthetic response. Tests additionally use disposable
loopback HTTP servers for method/path, redirect, error, chunked-size and timeout
checks. They do not connect to a live router or read environment credentials.

## Boundary and compatibility

`createStatusAdapter(config).snapshot(routeId)` returns one object conforming to
the closed status schema. Configure router origin, deployment source commit,
protected management credential, local connection/provider references and route
membership on the backend. Clone configuration on creation; changing it requires
creating a new adapter. Never ship this module or credential to a browser/worker.
Read credentials from protected storage in the integrating backend, not command
line arguments. The constructor accepts a value to keep storage policy outside
this experiment; there is no bundled secret, environment scanner or live CLI.

The only upstream operation is **GET `/api/providers`**, with redirects disabled
and explicit request time/response byte limits. Use a private authenticated ingress
whose credential authorizes this read only. A raw dashboard session, merely sending
a bearer header or passing synthetic tests does not establish that ingress. HTTPS
is required; the explicit synthetic-loopback option permits local HTTP test servers.
No API is exposed for caller-selected methods, paths, origins or active refresh.
In particular, `/api/usage/{connectionId}`, which can refresh credentials, is never
called; raw usage aggregates and API-key maps are not read.

Two source profiles are deliberately distinct:

- `69724d86d4486fa5d722e63ebb6c9700e71aebff` (reference fork 0.3.98): inspect only
  configured identities and canonical scalar status/time fields. Missing, future
  or stale provenance cannot admit work; conflicting negative states are retained
  conservatively. Freshness is at most 30 seconds and may be shortened.
- `17c4cc76877bd1755030a8414f8d0083f48dcccf` (npm 0.5.75 source): the canonical
  fields above are not that version's status interface. Active configuration and
  legacy `testStatus=active` remain unknown. Explicit disabled configuration is
  observed at authenticated snapshot retrieval time. Fresh `error`/`unavailable`
  observations with valid `lastErrorAt` project blocked, never quota exhaustion.
  Inspected enum/time fields must be valid even for disabled configuration; old
  observations keep their source timestamp and become unknown on expiry.

Other source pins are refused before network access. The configured source commit
must come from inspected deployment evidence; this adapter does not remotely
attest a server's identity. Reinspect changed/patched deployments before selecting
one of these profiles. No hostname or particular private network is required.

All output objects are newly constructed and validated by the actual JSON-schema
consumer. Upstream names, emails, IDs, arbitrary errors, URLs, nested provider data
and unknown fields never pass through. Diagnostics expose only a fixed reason enum,
including for parsing, transport and logging failures. Invalid projections fail
closed to a schema-valid unknown result. Malformed connection fields invalidate
that connection and preserve valid siblings; malformed response containers and
ambiguous identities invalidate the whole response. Output contains only locally configured
opaque references, enums and normalized timestamps; no quota estimates or totals.
Aliases share connection references instead of manufacturing additional capacity.

When every configured alternative is freshly blocked, exhausted or disabled,
route readiness is **not_ready**, with the intersection of those observations'
lifetimes. A missing, malformed or stale alternative leaves readiness unknown;
an empty connection set cannot create a denial. Capabilities always remain unknown.
A later integration must supply the separate deployment, epoch,
capability and bounded-inference evidence before claiming ready. It must invalidate
or repoll observations after their `validity_until`; polling does not refresh old
router timestamps. The output is not execution authority.

## Observed verification and limits

The automated suite exercises both version profiles, synthetic nested credentials
and raw key aggregates, duplicate aliases and distinct subscriptions, malformed and
stale status, immutable configuration, unsupported-version refusal, exact HTTP
reads, redirects, server/parser/logger failures, chunked response bounds and abort
of a stalled server connection, including bodies rejected on their headers.
All 36 tests pass, including direct execution of 18 applicable provider fixtures
from the accepted contract corpus. Four broader corpus cases require absent-read,
model-discovery or bounded-inference/capability inputs not exposed by this passive
adapter; their definitions are validated separately, not claimed as adapter runs.
TypeScript checking and CI use the same commands.

No authenticated deployed-router status call, private-ingress scope proof, provider
quota measurement, account migration or live readiness probe has been performed by
this experiment. Issue #2 is accepted; deployment authentication remains a separate
gate. This experiment does not claim production compatibility from fixtures alone.
