# Node oracle for console/routes/reports

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

`reports.js` runs the Node console's report routes (`routes/reports.routes.js`
with the real `services/reportEngine.js` and `services/dbAdapter.js`) on a
scratch database and records every answer. `TestNodeAnswers` in the Go
package replays the requests on the same rows behind the Go console's whole
pipeline and must get the same status, the same body byte for byte, and for
the CSV export the same `Content-Type` and `Content-Disposition`.

This folder is a test fixture from the time both consoles existed. The route
file it loads is gone from `web-nodejs` since the routes moved to Go, so the
script runs only on a tree that still has it (the commit before the move).
The recorded files stay: `../node_sqlite.json` and `../node_postgres.json`
are plain data and keep pinning the Go code to what Node answered on each
database.

## What is recorded

Every route as the accounts whose answer the route decides (signed out,
super_admin and admin everywhere; pro, viewer, operator, server_admin and
global_admin on the reads, where the panel access guard lets them through):
the types, every report type and many wrong ones, date ranges and filters of
every JSON type, the CSV of every part of every report, saved reports whose
payload is JSON, no JSON, NULL, `0` or a JSON string, ids in many spellings,
saves with titles and types of every JSON type, and deletes of every row.
`lib.js` is the harness: an Express app with the console's body parsers, the
JSON content-type rule, a session set from a header, and the route file.

Seeded times lie before 2020 and are compared as they are. Times the run
makes itself (`generated_at`, a saved report's `created_at`, the default
title and the date in the file name of an export) are compared by their
form.

## Running

From a worktree that still has the route file, in the dev image:

    tools/dev.sh "cd betterdesk-server/console/routes/reports/testdata && TZ=UTC node nodeoracle/reports.js"
    DEV_DOCKER_ARGS="--network bdgo" tools/dev.sh "cd betterdesk-server/console/routes/reports/testdata && \
      TZ=UTC ORACLE_PG_DSN='postgres://betterdesk:bdgo-local@bdgo-testpg:5432/betterdesk_test?sslmode=disable' node nodeoracle/reports.js"
