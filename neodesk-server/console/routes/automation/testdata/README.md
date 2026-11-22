# Recordings of the old console for console/routes/automation

The files below are fixed records of what the Node console answered, made
while it still existed. They can't be recorded again. The Go tests replay
them and need no Node. Don't edit them: when a fix changes an answer on
purpose, its step goes into `panelFixes` or `deviceFixes` in
`fixed_test.go` with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `node_sqlite.json`, `node_postgres.json` | the ten panel routes of `routes/automation.routes.js` (alert rules, alert history, remote commands) | `TestNodeAnswers` (`replay_test.go`) |
| `node_device_sqlite.json`, `node_device_postgres.json` | the two routes of the device agent, `GET /commands` and `POST /commands/:id(\d+)/result` | `TestNodeDeviceAnswers` (`device_test.go`) |

## What is recorded

The panel routes ran with the real `services/dbAdapter.js` on a scratch
database: 2,976 steps on SQLite and 2,973 on PostgreSQL.

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

The harness was an Express app with the console's body parsers, the JSON
content-type rule, a session set from a header, and the route file. It had
no panel access guard. The console's guard refuses the writes of a viewer,
a guest issuer and a server admin under `/api/automation`; the recording's
writes for those roles change nothing, and the Go test takes the guard's
answer there.

Rows written during the run got their `created_at` from their id by a
statement between the requests, so that lists ordered by time have one
order. Times the database fills in itself are compared by their form (text
of the second on SQLite, ISO with milliseconds on PostgreSQL). All other
times are fixed and lie in 2001 or in March 2019.

The device routes ran behind the real `requireDeviceToken` of
`middleware/deviceAuth.js` with the real adapter, the router mounted at
`/api/automation` and `/api/bd` as `routes/index.js` had it: 226 steps per
database.

- tokens in every state at both mounts
- the pending commands of a device, oldest first, with what the request
  names (query, `X-Device-Id`) not counting
- every status, before the command and its device are looked at
- results of every JSON type, String() of them included where it fails
- ids in every spelling, commands of other devices and of devices whose
  id differs only in case or a space, and every status after every other
- after each result the command's row
