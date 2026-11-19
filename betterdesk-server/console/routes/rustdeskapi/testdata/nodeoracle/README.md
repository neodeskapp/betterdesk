# Node oracle for console/routes/rustdeskapi

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

`rdapi.js` runs the Node console's client API (`routes/rustdesk-api.routes.js`
with the real `services/dbAdapter.js`, `services/authService.js`,
`services/rustdeskAddressBookSync.js`, `services/deviceGroupService.js` and
`services/serverBackend.js`) on a scratch database, behind a scripted API
server that lists six devices and takes tag writes, and records every
answer. `TestNodeAnswers` in the Go package replays the requests on the same
rows behind the whole Go pipeline and must get the same status, the same
Content-Type, the same body byte for byte (access tokens and second-step
secrets are random on both sides and compared by their form) and the same
writes to the API server.

This folder is a test fixture from the time both servers existed. The route
file is gone from `web-nodejs` since the routes moved to Go, so the script
runs only on a tree from before the move. The recorded files stay:
`../node_sqlite.json` and `../node_postgres.json` are plain data and keep
pinning the Go code to what Node answered on each database.

## What is recorded

Per database: address books written in every form a client may send (JSON
text, malformed text, a list, an object, numbers, flags, null, the empty
text, keys that JavaScript reorders, numbers it reformats, escapes, control
characters, long and odd tags), each read back as legacy and personal book
and as tags, for a viewer, an operator (whose tags go to the devices), a pro
account, a guest issuer and an admin, with a device group only the admin
may see; heartbeats and system information of known, banned, deleted and
unknown devices (also devices only the signal server's table has, which the
lookup copies), with the rows they leave; audit uploads with values of every
JSON type and the lists of them; sign-ins with client ids and UUIDs of every
JSON type, the token rows they leave, sign-outs, and the audit entries.

`lib.js` is the harness: an Express app with the console's body parsers, the
route file, and a scripted API server. Requests come from 203.0.113.7, the
address the Go test uses.

## Running

From a worktree that still has the route file, in the dev image:

    tools/dev.sh "cd betterdesk-server/console/routes/rustdeskapi/testdata && node nodeoracle/rdapi.js"
    DEV_DOCKER_ARGS="--network bdgo" tools/dev.sh "cd betterdesk-server/console/routes/rustdeskapi/testdata && \
      ORACLE_PG_DSN='postgres://betterdesk:bdgo-local@bdgo-testpg:5432/betterdesk_test?sslmode=disable' node nodeoracle/rdapi.js"
