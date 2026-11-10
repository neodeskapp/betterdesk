# Node oracle for console/routes/tickets

`tickets.js` runs the Node console's ticket routes (`routes/tickets.routes.js`
with the real `services/dbAdapter.js`, `lib/safePath.js` and Express's file
sender) on a scratch database and records every answer. `TestNodeAnswers` in
the Go package replays the requests on the same rows and must get the same
status and the same body, byte for byte, and for downloads the same bytes
and headers.

This folder is a test fixture for the time both servers exist. The routes it
records are gone from `web-nodejs` since they moved to Go (the route file
there keeps the two device routes only), so the script runs only on a tree
from before the move. The recorded files stay: `../node_sqlite.json` and
`../node_postgres.json` are plain data and keep pinning the Go code to what
Node answered on each database.

## What is recorded

606 steps per database: every route as every role that can sign in and as
nobody; the list with every filter, the search with its wildcards and query
parameters given as lists; creating and updating with values of every JSON
type in every field; comments; uploads with file names, types and data in
every form `Buffer.from` takes or refuses; every attachment as a download
with its bytes, `Content-Type`, `Content-Disposition`, `Accept-Ranges` and
`Content-Range`, with range and conditional requests; deletes; the audit log
as rows; and the files left in the attachment folder at the end.

`lib.js` is the harness: an Express app with the console's body parsers, the
JSON content-type rule, a session set from a header, the console's error
answer, and the route file. It has no panel access guard; the Go test says
where that matters. Statements between the requests give new rows fixed
times, so that lists ordered by time have one order. Times the database
fills in itself are compared by their form.

The route writes attachments to `web-nodejs/data/attachments`. The script
removes what it wrote there. In the recording `<UPLOADS>` stands for that
folder.

## Running

From a worktree that still has the routes, in the dev image:

    tools/dev.sh "cd betterdesk-server/console/routes/tickets/testdata && node nodeoracle/tickets.js"
    DEV_DOCKER_ARGS="--network bdgo" tools/dev.sh "cd betterdesk-server/console/routes/tickets/testdata && \
      ORACLE_PG_DSN='postgres://betterdesk:bdgo-local@bdgo-testpg:5432/betterdesk_test?sslmode=disable' node nodeoracle/tickets.js"
