# Recordings of the old console for console/access

The files here are fixed records of what the Node console's access code
answered, made while it still existed. They can't be recorded again. The
Go tests replay every record and need no Node. Don't edit them: when a fix
changes an answer on purpose, the test lists that record as an intended
difference with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `role_has_permission.json.gz` | `roleHasPermission` for every role and every permission name found in the code | `TestOracleRoleHasPermission` |
| `find_rule.json.gz` | `findRule` for about 1,100 paths, and for a table with nested rules | `TestOracleFindRule` |
| `guard.json.gz` | `panelAccessGuard` for every path, method, kind of session and policy state | `TestOracleGuard` |
| `request_has_permission.json.gz` | `requestHasPermission` over role, section, policy level, permission, effective set and load state | `TestOracleRequestHasPermission` |
| `normalize.json.gz` | level normalising, role ceilings, policy row normalising, subject validation, the catalog | `TestOracleNormalize` |
| `access_allows.json.gz` | `accessAllows` over policy shapes and levels | `TestOracleAccessAllows` |
| `policy_merge.json.gz` | `resolveForUser` and `resolveForGroup` for generated rows | `TestOraclePolicyMerge` |
| `require_guards.json.gz` | `requireAuth`, `requireAdmin`, `requireRole`, `requirePermission`, `requireRdClientAuth` with a recording response | `TestOracleRequireGuards` |
| `rdclient.json.gz` | `normalizeRdClientReturnUrl` | `TestOracleRdClientReturnURL` |
| `bootstrap.json.gz` | `permissions`, `access` and `nav` of the bootstrap, and `buildNav` | `TestOracleBootstrap` |
| `scope.json.gz` | the scope normalisers, `groupAllowedForUser`, and `getDeviceScopeForUser` over generated groups, grants and devices | `TestOracleScope` |
| `role_service.json.gz` | the role permission cache and `loadEffectivePermissions` | `TestOracleRoleService` |
| `store_sqlite.json.gz`, `store_postgres.json.gz` | every call of a scenario on a scratch database, per database | `TestOracleStore` |
| `counts.json` | how many records each file above holds | none |

## What is recorded

The vector files came from `middleware/auth.js`, `middleware/panelAccess.js`,
`lib/panelAccessCatalog.js`, `lib/consoleNav.js`,
`services/panelAccessPolicyService.js`, `services/rolePermissionService.js`,
`services/deviceGroupService.js`, `services/userScopeService.js` and the
bootstrap handler of `routes/console.routes.js`. The database and the Go API
client were in-memory fakes. The generators were seeded, so every run wrote
the same files.

The store files came from the real `services/database`, policy and scope
services on a scratch database with fixed rows, on SQLite and on
PostgreSQL (in a schema of its own).
