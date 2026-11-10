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
