# Node oracle for console/routes/devices

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

`routes.js` runs the Node console's device routes (`routes/devices.routes.js`
with the real `services/dbAdapter.js`, the real API client and the real
i18n middleware) on a scratch database, in front of an API server that
answers from a table, and records every answer and every call the routes
made to the API server. `TestNodeAnswers` in the Go package replays the
requests on the same rows with the same API answers and must get the same
status and body, byte for byte, make the same calls, and leave the same
rows.

This folder is a test fixture from the time both servers existed. The route
handlers it loads are gone from `web-nodejs` since the routes moved to Go,
so the scripts run on a tree that still has them, named by `WEB_NODEJS`
(default `/repo/web-nodejs`): the recordings are from the specification
tree of the upstream 3.5.264 merge. The recorded files stay:
`../node_sqlite.json` and `../node_postgres.json` are plain data and keep
pinning the Go code to what Node answered on each database.

## What is recorded

464 requests per database: the list with every filter and sort parameter
(also as lists and objects), one device with its system information,
metrics, telemetry, connection mode in every shape the server can give,
who banned it; availability and sessions; telemetry commands; remote
targets; tags (list, rename, delete, set); device groups (create, change,
delete, the folder's group), members; every write on a device with values
of every JSON type; delete, restore, bulk delete, ban, a new id in 20
spellings; the access policy, the connect password, the connection mode;
the rules for RustDesk clients with their assignments. The device group
writes go to the API server's panel grouping (`/api/panel/device-groups`)
with every answer it can give, including none (`hangup`), and fall back to
the console's own writer where Node did. The device scope check in front of
every address below `/api/devices/:id` is sent every route, a deleted
device, an unknown one, `bulk-delete`, other spellings and paths no route
has, the folder route of `routes/folders.routes.js` (mounted after the
device routes, as in `routes/index.js`), and at the end a check that fails
(a dropped table). The roles are admin, operator (who may not see three of
the devices), viewer, guest_issuer, the three other unrestricted roles and
signed out, for reads and writes the panel access guard lets through. A
request that reaches the end of the oracle's app (`statusOnly`) is compared
by status and calls only, since that 404 is the oracle's own. The API
server answers success, refusals, 500 without a body, text and JSON of the
wrong type. The rows of the member, folder, assignment, guid and audit
tables are recorded on the way.

Random guids and the times the database fills in are compared by their
form. A tag cut in the middle of a character outside the Basic
Multilingual Plane is compared as U+FFFD (BUGS DGR2).

## Running

In the dev image, with the specification tree's `web-nodejs` mounted at
`/spec/web-nodejs` (and its dependencies), on SQLite and on PostgreSQL:

    DEV_DOCKER_ARGS="--network bdgo -v <spec tree>/web-nodejs:/spec/web-nodejs -v bdgo-node-nm:/spec/web-nodejs/node_modules -e WEB_NODEJS=/spec/web-nodejs" \
      tools/dev.sh "cd betterdesk-server/console/routes/devices/testdata && node nodeoracle/routes.js && \
      ORACLE_PG_DSN='postgres://betterdesk:bdgo-local@bdgo-testpg:5432/betterdesk_test?sslmode=disable' node nodeoracle/routes.js"

## The routes that reach into an agent (agent.js)

`agent.js` runs the routes of `routes/devices.routes.js` that ask a
running agent (services, processes, events, activity, the file routes,
clipboard, screenshot, mouse, key, text, terminal) with the real
`services/bdRelay.js`, the real database adapter and the API server table,
and a made-up agent of device A1001 connected to `/ws/bd-signal` that
answers each request the way the step says: data of every JSON type, an
error of every type (`agent_offline` and `agent_timeout` included), or
nothing (the route's timeout). B1002 has no agent, so its steps show the
server's snapshot, a queued collection, 503; the API server's telemetry and
queue answers vary. C1003 is out of e2e_operator's scope. Bodies and
queries are odd on purpose. Every route is also sent for C1003 as
e2e_operator, where the device scope check of the device routes answers
first. It records each answer, the calls to the API
server, the requests the agent got (request id as `<id>`) and the audit
rows: 157 steps per database in `../node_agent_sqlite.json` and
`../node_agent_postgres.json`. `TestNodeAgentAnswers` replays them through
the whole console with a Go made-up agent (`console/bdrelay/fakeagent`)
that has a real access token of A1001.

The oracle's app has no panel access guard and ends with a stand-in error
handler, so its steps stay within what the guard lets through, and for an
answer of that handler (a body that is not JSON, one over the size limit)
only the status is compared.

Not in the recording, because it ends the Node process: a body value
`String()` throws on in an async route (BUGS WS9);
`TestAgentRequestThatWouldEndNode` covers the Go side.

The same way as `routes.js`, with `node nodeoracle/agent.js`.

After a run, `node ../../../ws/wstest/squeeze.js node_agent_sqlite.json node_agent_postgres.json` (from `testdata`) writes the megabyte bodies of the size limits as markers; the test reads the files through `wstest.Expand`.
