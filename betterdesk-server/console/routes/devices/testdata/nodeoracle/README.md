# Node oracle for console/routes/devices

`routes.js` runs the Node console's device routes (`routes/devices.routes.js`
with the real `services/dbAdapter.js`, the real API client and the real
i18n middleware) on a scratch database, in front of an API server that
answers from a table, and records every answer and every call the routes
made to the API server. `TestNodeAnswers` in the Go package replays the
requests on the same rows with the same API answers and must get the same
status and body, byte for byte, make the same calls, and leave the same
rows.

This folder is a test fixture for the time both servers exist. The route
handlers it loads are gone from `web-nodejs` since the routes moved to Go
(only the routes that reach into an agent are left there), so the script
runs only on a tree that still has them (the commit before the move). The
recorded files stay: `../node_sqlite.json` and `../node_postgres.json` are
plain data and keep pinning the Go code to what Node answered on each
database.

## What is recorded

318 requests per database: the list with every filter and sort parameter
(also as lists and objects), one device with its system information,
metrics, telemetry, connection mode in every shape the server can give,
who banned it; availability and sessions; telemetry commands; remote
targets; tags (list, rename, delete, set); device groups (create, change,
delete, the folder's group), members; every write on a device with values
of every JSON type; delete, restore, bulk delete, ban, a new id in 20
spellings; the access policy, the connect password, the connection mode;
the rules for RustDesk clients with their assignments. The roles are
admin, operator (who may not see two of the devices), viewer and
guest_issuer, for reads and writes the panel access guard lets through.
The API server answers success, refusals, 500 without a body, text and
JSON of the wrong type. At the end the rows of the member, assignment,
guid and audit tables are recorded.

Random guids and the times the database fills in are compared by their
form. A tag cut in the middle of a character outside the Basic
Multilingual Plane is compared as U+FFFD (BUGS DGR2).

## Running

From a worktree that still has the route handlers, in the dev image:

    tools/dev.sh "cd betterdesk-server/console/routes/devices/testdata && node nodeoracle/routes.js"
    DEV_DOCKER_ARGS="--network bdgo" tools/dev.sh "cd betterdesk-server/console/routes/devices/testdata && \
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
queries are odd on purpose. It records each answer, the calls to the API
server, the requests the agent got (request id as `<id>`) and the audit
rows: 134 steps per database in `../node_agent_sqlite.json` and
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

    tools/dev.sh "cd betterdesk-server/console/routes/devices/testdata && node nodeoracle/agent.js"
    DEV_DOCKER_ARGS="--network bdgo" tools/dev.sh "cd betterdesk-server/console/routes/devices/testdata && \
      ORACLE_PG_DSN='postgres://betterdesk:bdgo-local@bdgo-testpg:5432/betterdesk_test?sslmode=disable' node nodeoracle/agent.js"

After a run, `node ../../../ws/wstest/squeeze.js node_agent_sqlite.json node_agent_postgres.json` (from `testdata`) writes the megabyte bodies of the size limits as markers; the test reads the files through `wstest.Expand`.
