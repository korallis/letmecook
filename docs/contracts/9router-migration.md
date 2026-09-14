# Optional 9Router configuration migration

Gaffer accepts an operator-selected router on infrastructure they control. It does
not require a particular host, tailnet, path or a new router installation. Keeping
an existing compatible instance is a valid choice. This source-backed procedure is
for an operator who separately chooses to move configuration; **no export, import,
credential copy or migration was performed for issue #2**.

## What the pinned release transfers

At `decolua/9router@17c4cc76877bd1755030a8414f8d0083f48dcccf` (npm 0.5.75),
`GET /api/settings/database` exports and `POST /api/settings/database` imports
router configuration. The middleware requires dashboard authentication or the
router's local CLI authority. The route also checks the dashboard password for
non-CLI requests. These are powerful router-management operations, excluded from
Gaffer's passive status adapter. [API implementation](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/src/app/api/settings/database/route.js),
[management guard](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/src/dashboardGuard.js).

The JSON includes raw saved settings, full provider-connection records, provider
nodes, proxy pools, router API keys, combos, model aliases, custom models, MITM
aliases and pricing. It contains provider tokens, router keys and potentially
credentials inside settings/proxy objects. It belongs exclusively in the operator's
private 9Router administration and backup storage. Never put it in a Gaffer store,
worker sandbox, repository, transcript, debug log or shared artifact.

Import **replaces** the listed configuration tables in a transaction. It is not a
merge; omitted sections can empty destination tables. It does not constitute a
complete data-directory backup: runtime dependencies, machine/JWT identity, usage,
request history and logs are outside this JSON contract. Cross-version schema
compatibility, machine-bound API keys and provider OAuth portability are not
established by source inspection. Destination login/refresh may still be needed.
[Export/import implementation](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/src/lib/db/index.js).

## Operator procedure

1. Explicitly select source and destination router identities and pin both builds.
   Prefer a compatible destination build; inspect release/schema changes before
   moving across versions. Validate its private encrypted management access and
   inference authentication. Keep Gaffer admission closed for affected routes.
2. Preserve a recoverable destination backup before replacement. Use the router's
   documented administrator export/backup tools; Gaffer must not couple to private
   SQLite paths. Coordinate quiescence with the router's other clients and account
   refresh activity before taking a consistent backup.
3. Export through the router's authenticated management interface over encrypted
   transport. Keep the secret-bearing payload in operator-owned storage with
   restrictive permissions and encryption. Transfer it only to the selected
   destination administrator; do not display or paste it into an agent session.
4. Review replacement scope and import through the destination router. Verify
   identities, configured provider/model/billing paths, aliases, capability-adapter
   defaults, combo strategies, endpoints and proxy destinations. Verify authentication
   and account refresh using the destination router's own controls.
5. Run the [integration contract](9router.md) conformance gates at the destination.
   A successful import or catalog response does not establish model readiness.
   Invalidate old Gaffer grants, advance policy revision/epoch and update Gaffer's
   configurable origin/credential **references** only after the destination is
   verified and the old route has drained.
6. Retain a rollback path until verification finishes. Deliberately retire redundant
   endpoint credentials and secret backup copies under the operator's retention
   policy. Provider account selection remains entirely inside 9Router.

The pinned router also makes limited SQLite safety backups before migrations; it
keeps three and excludes request details. Those backups do not replace a tested
portable recovery plan. [Backup implementation](https://github.com/decolua/9router/blob/17c4cc76877bd1755030a8414f8d0083f48dcccf/src/lib/db/backup.js).

This procedure documents source-supported capabilities and required checks. It does
not certify a migration between arbitrary versions or transfer any account access.
