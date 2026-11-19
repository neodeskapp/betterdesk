# Node oracle for console/routes/dataguard

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

`dataguard.js` runs the policy and event routes of the Node console
(`routes/dataguard.routes.js` with the real `services/dbAdapter.js`) at both
of its mounts, `/api/dataguard` and `/api/bd`, on a scratch database and
records every answer. `TestNodeAnswers` in the Go package replays the
requests on the same rows and must get the same status and the same body,
byte for byte.

This folder is a test fixture from the time both servers existed. The routes it
records are gone from `web-nodejs` since they moved to Go (the route file
there keeps the two device routes only), so the script runs only on a tree
from before the move. The recorded files stay: `../node_sqlite.json` and
`../node_postgres.json` are plain data and keep pinning the Go code to what
Node answered on each database.

## What is recorded

401 steps per database: every route at both mounts as every role that can
sign in and as nobody; policies created and updated with values of every
JSON type in every field, `enabled` in every spelling PostgreSQL reads as a
boolean or refuses, rules as lists of every shape; ids in every spelling;
events (inserted as rows, the way devices report them) with every filter,
limits that are no whole numbers, time ranges and parameters given as lists;
the counts; and rows whose rules or details the routes cannot parse, which
make a list fail.

`lib.js` is the harness: an Express app with the console's body parsers, the
JSON content-type rule, a session set from a header, the console's error
answer, and the route file. It has no panel access guard; the Go test says
where that matters. Statements between the requests give rows fixed times.
Times the database fills in itself are compared by their form.

## Running

From a worktree that still has the routes, in the dev image:

    tools/dev.sh "cd betterdesk-server/console/routes/dataguard/testdata && node nodeoracle/dataguard.js"
    DEV_DOCKER_ARGS="--network bdgo" tools/dev.sh "cd betterdesk-server/console/routes/dataguard/testdata && \
      ORACLE_PG_DSN='postgres://betterdesk:bdgo-local@bdgo-testpg:5432/betterdesk_test?sslmode=disable' node nodeoracle/dataguard.js"

## The device routes

`device.js` records the two device routes (`GET dlp-policies`, `POST dlp-events`) at both mounts in the same way, behind the real
`requireDeviceToken` with token rows in every state in its seed, into
`../node_device_sqlite.json` and `../node_device_postgres.json`.
`TestNodeDeviceAnswers` replays them. It runs like `dataguard.js`, from a tree that
still has the device routes in Node: `node nodeoracle/device.js`.
