# Node oracle for console/housekeeping

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

`housekeeping.js` runs the periodic clean-ups of the Node console's
database adapter (`web-nodejs/services/dbAdapter.js`) on a temporary
SQLite file and on PostgreSQL, and records the seed statements, the rows
left or changed and the timer delays Node used. The Go tests in
`console/housekeeping` replay every record (`housekeeping_test.go`).

This folder is a test fixture from the time both servers existed. It needs
`web-nodejs`, which this tree no longer has. The recordings in the parent folder
(`housekeeping_sqlite.json`, `housekeeping_postgres.json`) stay.

| Part | What it holds |
|---|---|
| `integration` | `runIntegrationHousekeeping` on metrics and client audit rows of many ages, in every time form SQLite can hold (text with a space or a `T`, a date, a number, NULL, garbage); a second run on a database without `audit_files`, which stops there |
| `stale` | `cleanupStaleOnlinePeers` with each recorded `HEARTBEAT_STALE_THRESHOLD`, read as server.js read it, on online and offline devices of many ages |
| `settings` | `STATUS_SYNC_INTERVAL` and `HEARTBEAT_STALE_THRESHOLD` read as `parseInt(value, 10) || default`, and the delay Node's `setInterval` used (one millisecond for what does not fit a timer) |

## Running

In the dev image, from the worktree:

    tools/dev.sh "cd betterdesk-server/console/housekeeping/testdata/nodeoracle && node housekeeping.js"
    DEV_DOCKER_ARGS="--network bdgo" tools/dev.sh "cd betterdesk-server/console/housekeeping/testdata/nodeoracle && ORACLE_PG_DSN='postgres://betterdesk:bdgo-local@bdgo-testpg:5432/betterdesk_test?sslmode=disable' node housekeeping.js"

Rows are placed relative to the time of the run, far from every threshold,
so a slow run lands on the same side.
