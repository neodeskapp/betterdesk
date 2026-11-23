# Recordings of the old console for console/routes/inventory

The files below are fixed records of what the Node console answered, made
while it still existed. They can't be recorded again. The Go tests replay
them and need no Node. Don't edit them: when a fix changes an answer on
purpose, its step goes into `readFixes` (`fixes_test.go`) or `deviceFixes`
(`fixed_test.go`) with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `node_sqlite.json`, `node_postgres.json` | the two read routes of `routes/inventory.routes.js` on each database | `TestNodeAnswers` (`replay_test.go`) |
| `node_device_sqlite.json`, `node_device_postgres.json` | the three routes of the desktop agent on each database | `TestNodeDeviceAnswers` (`device_test.go`) |

The Go tests replay the requests on the same rows and must get the same
status and the same body, byte for byte.

## What is recorded

### The read routes

The recorder ran the two read routes with the real `services/dbAdapter.js`
on a scratch database: 1,528 steps on SQLite and 1,489 on PostgreSQL, on
1,214 and 1,191 reports:

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

The harness was an Express app with the console's body parsers, the JSON
content-type rule, a session set from a header, and the route file. The API
server's client was replaced by one that knows no devices, so the list
without reports is empty; `TestTheListFallsBackToThePeerList` covers the
devices it shows there. All times in the rows are fixed and lie in March
2019.

### The device routes

The recorder ran the three routes of the desktop agent, `POST /inventory`,
`POST /telemetry` and `GET /inventory/:id` at `/api/nd` and
`/api/inventory`, behind the route file's own token check, with the real
`services/database.js` and adapter and the real alert rules engine (its
mail stubbed; no rule mails): 1,727 steps on SQLite and 1,723 on
PostgreSQL.

- tokens in every state (unbound, a client id of any form, expired,
  revoked, stored before the hash column, a numeric device id) and
  Authorization headers in many forms
- the body checked before the device it names, with device ids of every
  type
- reports with the hardware, the software and the time of collection of
  every JSON type, and bodies given as text (keys twice, numbers in every
  notation, escapes, deep nesting)
- the device's info in the console's table: each member the upload writes
  with values of every type, the info in every form a database holds it,
  devices only the signal server knows (before and after its table
  exists) and devices deleted in the console
- disks of every kind with the disk alert they raise, the count of apps
  that the log line makes text of, and telemetry with values of every type
  with its CPU and memory alerts (one rule per type that fires for every
  number)
- after a request the rows it left: report, info, telemetry, alerts

The recorder waited for the background evaluations of the alert rules
before each step; the Go test waits for the engine the same way. A time
the route filled in itself is shown as "the time of the run" in the
statements.
