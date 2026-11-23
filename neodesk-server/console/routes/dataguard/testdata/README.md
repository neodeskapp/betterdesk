# Recordings of the old console for console/routes/dataguard

The files below are fixed records of what the Node console answered, made
while it still existed. They can't be recorded again. The Go tests replay
them and need no Node. Don't edit them: when a fix changes an answer on
purpose, its step goes into `policyFixes` or `deviceFixes` in
`fixed_test.go` with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `node_sqlite.json`, `node_postgres.json` | the policy and event routes of `routes/dataguard.routes.js` at `/api/dataguard` and `/api/nd` | `TestNodeAnswers` (`replay_test.go`) |
| `node_device_sqlite.json`, `node_device_postgres.json` | the two device routes, `GET dlp-policies` and `POST dlp-events`, at both mounts | `TestNodeDeviceAnswers` (`replay_test.go`) |

## What is recorded

The routes ran with the real `services/dbAdapter.js` on a scratch database.
The policy and event recordings hold 401 steps per database: every route at
both mounts as every role that can sign in and as nobody; policies created
and updated with values of every JSON type in every field, `enabled` in
every spelling PostgreSQL reads as a boolean or refuses, rules as lists of
every shape; ids in every spelling; events (inserted as rows, the way
devices report them) with every filter, limits that are no whole numbers,
time ranges and parameters given as lists; the counts; and rows whose
rules or details the routes can't parse, which make a list fail.

The harness was an Express app with the console's body parsers, the JSON
content-type rule, a session set from a header, the console's error
answer, and the route file. It had no panel access guard; the Go test says
where that matters. Statements between the requests gave rows fixed times.
Times the database fills in itself are compared by their form.

The device routes were recorded the same way, behind the real
`requireDeviceToken` with token rows in every state in the seed.
