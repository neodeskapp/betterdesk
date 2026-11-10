# Node oracle for console/routes/tenants

`tenants.js` runs the Node console's tenant routes (`routes/tenants.routes.js`
with the real `services/dbAdapter.js`) on a scratch database and records every
answer. `TestNodeAnswers` in the Go package replays the requests on the same
rows and must get the same status and the same body, byte for byte.

This folder is a test fixture for the time both servers exist. The route file
it loads is gone from `web-nodejs` since the routes moved to Go, so the script
runs only on a tree that still has it (the commit before the move). The
recorded files stay: `../node_sqlite.json` and `../node_postgres.json` are
plain data and keep pinning the Go code to what Node answered on each
database.

## What is recorded

828 requests per database: every route as every role and signed out, names
and the slugs made from them, every field with values of every JSON type
(text, numbers, flags, null, lists, objects), ids in 34 spellings on every
route that takes one, assignments of devices and users, statistics, deletes.
`lib.js` is the harness: an Express app with the console's body parsers, the
JSON content-type rule, a session set from a header, and the route file.

Two statements between the requests give assignments fixed times, so that
lists ordered by time have one order. Times the database fills in itself are
compared by their form (text of the second on SQLite, ISO with milliseconds
on PostgreSQL).

## Running

From a worktree that still has the route file, in the dev image:

    tools/dev.sh "cd betterdesk-server/console/routes/tenants/testdata && node nodeoracle/tenants.js"
    DEV_DOCKER_ARGS="--network bdgo" tools/dev.sh "cd betterdesk-server/console/routes/tenants/testdata && \
      ORACLE_PG_DSN='postgres://betterdesk:bdgo-local@bdgo-testpg:5432/betterdesk_test?sslmode=disable' node nodeoracle/tenants.js"
