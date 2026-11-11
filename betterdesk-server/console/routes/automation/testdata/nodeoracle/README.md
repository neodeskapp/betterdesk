# Node oracle for console/routes/automation

`automation.js` runs the ten panel routes of the Node console's
`routes/automation.routes.js` (alert rules, alert history, remote commands)
with the real `services/dbAdapter.js` on a scratch database and records every
answer. `TestNodeAnswers` in the Go package replays the requests on the same
rows and must get the same status and the same body, byte for byte.

This folder is a test fixture for the time both servers exist. The ten
routes are gone from the route file since they moved to Go, so the script
runs only on a tree that still has them (the commit before the move), or
with `ORACLE_ROUTES` naming a copy of the old route file below `web-nodejs`
(`ORACLE_ROUTES=routes/automation.old.routes`). The recorded files stay:
`../node_sqlite.json` and `../node_postgres.json` are plain data and keep
pinning the Go code to what Node answered on each database.

## What is recorded

2,976 steps on SQLite and 2,973 on PostgreSQL:

- every route for every role that can sign in, and for nobody, with writes
  that change nothing, then writes that go through as admin and super_admin
- the addresses under `/api/bd`, where none of the ten answers, and the
  addresses in other spellings: letter case, slashes, escapes, other methods
- the alert history with `device_id`, `severity` and `acknowledged` in every
  form a query string can give them (text, empty, given twice, in brackets,
  by index, as an object, an object beside a list), and `limit` as whole
  numbers, fractions, signs, hexadecimal, text, numbers beyond every integer
  type, lists and objects
- acknowledging alerts: by several accounts, twice, alerts that do not
  exist, with bodies nobody reads
- new rules: the name, the type and the comparison with values of every JSON
  type, then `description`, `condition_value`, `severity`, `scope_device_id`,
  `cooldown_secs`, `notify_emails` and `enabled` with 67 values each (text,
  numbers, flags, null, lists, objects), numbers no double holds, keys given
  twice, half a surrogate pair, a NUL character, lists and objects that
  together fill the statement on SQLite
- updates of one rule: every field with the same 67 values, the rule read
  back after each, and the checks in their order
- new commands: the device, the payload and the type with values of every
  JSON type, among them devices JavaScript cannot make text of
- the command list with `device_id`, `status` and `limit` in every form
- ids in 42 spellings on every route that takes one, among them escaped
  digits, digits of other scripts and numbers beyond every integer type
- deleting rules, with the alerts that go with them
- rows only SQLite can hold: text and fractions in number columns, flags that
  are neither 0 nor 1, times in other forms, ids no JavaScript number holds
- at the end, read with a SELECT: who acknowledged what, the rules, alerts
  and commands that are left, and the audit log

`lib.js` is the harness: an Express app with the console's body parsers,
the JSON content-type rule, a session set from a header, and the route
file. It has no panel access guard. The console's guard refuses the writes
of a viewer, a guest issuer and a server admin under `/api/automation`; the
recording's writes for those roles change nothing, and the Go test takes the
guard's answer there.

Rows written during the run get their `created_at` from their id by a
statement between the requests, so that lists ordered by time have one
order. Times the database fills in itself are compared by their form (text
of the second on SQLite, ISO with milliseconds on PostgreSQL). All other
times are fixed and lie in 2001 or in March 2019.

## Running

From a worktree that still has the ten routes, in the dev image:

    tools/dev.sh "cd betterdesk-server/console/routes/automation/testdata && node nodeoracle/automation.js"
    DEV_DOCKER_ARGS="--network bdgo" tools/dev.sh "cd betterdesk-server/console/routes/automation/testdata && \
      ORACLE_PG_DSN='postgres://betterdesk:bdgo-local@bdgo-testpg:5432/betterdesk_test?sslmode=disable' node nodeoracle/automation.js"
