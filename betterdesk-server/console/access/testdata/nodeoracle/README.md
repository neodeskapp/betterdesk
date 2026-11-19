# Node oracle for console/access

`web-nodejs` is gone from this tree. These scripts stay as the record of
how the recordings in this package were made; the Go tests replay the
recordings and need no Node. To run a script again, use a tree that still
has `web-nodejs`: the spec worktree
`C:\Users\info\worktrees\betterdesk-go-spec264`. Mount its `web-nodejs`,
with its modules, over `/repo/web-nodejs` of this tree:

    DEV_DOCKER_ARGS="-v C:/Users/info/worktrees/betterdesk-go-spec264/web-nodejs:/repo/web-nodejs -v bdgo-node-nm:/repo/web-nodejs/node_modules" \
      tools/dev.sh "cd betterdesk-server/<this folder> && node <script>"

Its Node code is upstream's release there, so a script that loads a file
the port deleted or changed on the way can fail or record other answers.

These scripts run the console's Node modules (`web-nodejs`) and record what
they answer. The Go tests in `console/access` replay every record and must
answer the same (`oracle_test.go`, `store_oracle_test.go`).

This folder is a test fixture from the time both servers existed. It needs
`web-nodejs`, which this tree no longer has. The recorded files in the parent folder
(`*.json.gz`, `counts.json`) stay: they are plain data and keep pinning the
Go code to what Node did.

## Scripts

`vectors.js` loads `middleware/auth.js`, `middleware/panelAccess.js`,
`lib/panelAccessCatalog.js`, `lib/consoleNav.js`,
`services/panelAccessPolicyService.js`, `services/rolePermissionService.js`,
`services/deviceGroupService.js`, `services/userScopeService.js` and the
bootstrap handler of `routes/console.routes.js`. The database and the Go API
client are replaced by in-memory fakes. It writes:

| File | What it holds |
|---|---|
| `role_has_permission` | `roleHasPermission` for every role and every permission name found in the code |
| `find_rule` | `findRule` for about 1,100 paths, and for a table with nested rules |
| `guard` | `panelAccessGuard` for every path, method, kind of session and policy state |
| `request_has_permission` | `requestHasPermission` over role, section, policy level, permission, effective set and load state |
| `normalize` | level normalising, role ceilings, policy row normalising, subject validation, the catalog |
| `access_allows` | `accessAllows` over policy shapes and levels |
| `policy_merge` | `resolveForUser` and `resolveForGroup` for generated rows |
| `require_guards` | `requireAuth`, `requireAdmin`, `requireRole`, `requirePermission`, `requireRdClientAuth` with a recording response |
| `rdclient` | `normalizeRdClientReturnUrl` |
| `bootstrap` | `permissions`, `access` and `nav` of the bootstrap, and `buildNav` |
| `scope` | the scope normalisers, `groupAllowedForUser`, and `getDeviceScopeForUser` over generated groups, grants and devices |
| `role_service` | the role permission cache and `loadEffectivePermissions` |

`store.js` runs the real `services/database`, policy and scope services on
a scratch database with fixed rows and records every call of a scenario:
`store_sqlite` on SQLite, and `store_postgres` when `ORACLE_PG_DSN` names a
PostgreSQL server (it works in a schema of its own and drops it).

## Running

From the worktree, in the dev image:

    tools/dev.sh "cd betterdesk-server/console/access/testdata && node nodeoracle/vectors.js && node nodeoracle/store.js"
    DEV_DOCKER_ARGS="--network bdgo" tools/dev.sh "cd betterdesk-server/console/access/testdata && \
      ORACLE_PG_DSN='postgres://betterdesk:bdgo-local@bdgo-testpg:5432/betterdesk_test?sslmode=disable' node nodeoracle/store.js"

The output is the same on every run (the generators are seeded), so a diff
in the recorded files after a run means Node's behaviour changed.
