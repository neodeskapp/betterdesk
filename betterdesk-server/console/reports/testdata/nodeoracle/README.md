# Node oracle for console/reports

`engine.js` runs the Node console's report engine (`services/reportEngine.js`
with the real `services/dbAdapter.js`) on a scratch database and records what
it makes. `TestNodeReports` in the Go package runs the same statements on a
fresh database and must make the same reports, byte for byte: the JSON of
every report, the CSV of every part of it, and `toCsv` of rows written by
hand.

This folder is a test fixture for the time both consoles exist. The engine
it loads is gone from `web-nodejs` since the reports moved to Go, so the
script runs only on a tree that still has it (the commit before the move).
The recorded files stay: `../engine_*.json` are plain data and keep pinning
the Go code to what Node made on each database.

## What is recorded

Worlds of rows in every table the engine reads (`peer`, `activity_summaries`,
`activity_sessions`, `dlp_events`, `audit_log` with `users`, `tickets`,
`device_inventory`, `alert_rules`, `alert_history`, `network_targets`):
empty tables, ordinary rows, NULLs, unicode, values that need CSV quoting,
names every JavaScript object inherits (`constructor`, `__proto__`), names
that are numbers, more rows than the reports keep, and rows that make a part
of a report fail. For every world each report type is generated with many
options, the activity report with date ranges at day, month and daylight
saving borders, and each part of each report is written as CSV the way the
export route picks it.

The clock is fixed (`new Date()` is 2026-11-10T12:34:56.789Z) and every time
column gets a value of its own, so a run records the same bytes every time.
Rows that sort equal (the same time, two apps of one device with the same
total) are seeded on SQLite only: PostgreSQL gives them in no fixed order, so
Node's own answer for them changes from run to run.
Times on PostgreSQL go into a CSV in the zone of the process: the script runs
with `TZ=Europe/Berlin` on both databases and with `TZ=UTC` on PostgreSQL.

## Running

From a worktree that still has the engine (or with `services/reportEngine.js`
of the commit before the move put back for the run), in the dev image:

    tools/dev.sh "cd betterdesk-server/console/reports/testdata && TZ=Europe/Berlin node nodeoracle/engine.js"
    DEV_DOCKER_ARGS="--network bdgo" tools/dev.sh "cd betterdesk-server/console/reports/testdata && \
      TZ=Europe/Berlin ORACLE_PG_DSN='postgres://betterdesk:bdgo-local@bdgo-testpg:5432/betterdesk_test?sslmode=disable' node nodeoracle/engine.js"
    DEV_DOCKER_ARGS="--network bdgo" tools/dev.sh "cd betterdesk-server/console/reports/testdata && \
      TZ=UTC ORACLE_PG_DSN='postgres://betterdesk:bdgo-local@bdgo-testpg:5432/betterdesk_test?sslmode=disable' node nodeoracle/engine.js"
