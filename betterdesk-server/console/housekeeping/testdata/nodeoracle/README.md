# Node oracle for console/housekeeping

`housekeeping.js` runs the periodic clean-ups of the Node console's
database adapter (`web-nodejs/services/dbAdapter.js`) on a temporary
SQLite file and on PostgreSQL, and records the seed statements, the rows
left or changed and the timer delays Node used. The Go tests in
`console/housekeeping` replay every record (`housekeeping_test.go`).

This folder is a test fixture for the time both servers exist. It needs
`web-nodejs` and goes away with it. The recordings in the parent folder
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
