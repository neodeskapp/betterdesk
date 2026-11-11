# Node oracle for console/routes/inventory

`inventory.js` runs the two read routes of the Node console's
`routes/inventory.routes.js` with the real `services/dbAdapter.js` on a
scratch database and records every answer. `TestNodeAnswers` in the Go
package replays the requests on the same rows and must get the same status
and the same body, byte for byte.

This folder is a test fixture for the time both servers exist. The read
routes are gone from the route file since they moved to Go, so the script
runs only on a tree that still has them (the commit before the move), or
with `ORACLE_ROUTES` naming a copy of the old route file below
`web-nodejs` (`ORACLE_ROUTES=routes/inventory.old.routes`). The recorded
files stay: `../node_sqlite.json` and `../node_postgres.json` are plain
data and keep pinning the Go code to what Node answered on each database.

## What is recorded

1,528 steps on SQLite and 1,489 on PostgreSQL, on 1,214 and 1,191 reports:

- both routes for every role that can sign in, and for nobody
- every member the list reads (`hostname`, `os_name`, `os_version`, `cpu`
  and its three members, `memory` and its two, `disks`, `software`,
  `software.apps`, and the hardware document as a whole) with 49 values in
  it: null, flags, numbers at the edges, texts that are almost numbers,
  lists, objects, an object with a `length`
- disks: each of the six sizes with 22 values, `removable` with 44, the
  three names with 49 each, things that are no disk, the rounding of the
  used part and the free bytes, mount names cut at 120 characters in the
  middle of a sign, and reports with several disks
- what only one database can hold: on SQLite NULL and empty text, text
  with spaces, a key given twice, keys that are numbers, halves of
  surrogate pairs, every escape, numbers in every notation, deep nesting;
  on PostgreSQL NULL and JSON values that are text holding JSON
- telemetry with what each database can hold in a number column (text and
  fractions on SQLite; NULL, NaN, infinity and the largest bigint on
  PostgreSQL)
- the list page by page, and `page` and `limit` in 42 forms each plus 29
  mixed queries
- the address in other spellings
- every report in full
- 27 reports (24 on PostgreSQL) that are put in and taken out again one at
  a time: 13 the list cannot make text or a number of (an object with a
  member named `toString`), which fail the whole list while the report
  itself reads fine; 7 whose text is no JSON (4 on PostgreSQL, where it is
  a JSON text holding no JSON); and 7 that look like one of those and read
  fine
- the list without any report

`lib.js` is the harness: an Express app with the console's body parsers,
the JSON content-type rule, a session set from a header, and the route
file. The API server's client is replaced by one that knows no devices, so
the list without reports is empty; `TestTheListFallsBackToThePeerList`
covers the devices it shows there.

All times in the rows are fixed and lie in March 2019. `inventory.js` has
no backslash in it: every special character is made from its number.

## Running

In the dev image:

    tools/dev.sh "cd betterdesk-server/console/routes/inventory/testdata && node nodeoracle/inventory.js"
    DEV_DOCKER_ARGS="--network bdgo" tools/dev.sh "cd betterdesk-server/console/routes/inventory/testdata && \
      ORACLE_PG_DSN='postgres://betterdesk:bdgo-local@bdgo-testpg:5432/betterdesk_test?sslmode=disable' node nodeoracle/inventory.js"
