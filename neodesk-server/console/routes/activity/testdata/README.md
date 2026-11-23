# Recordings of the old console for console/routes/activity

The files below are fixed records of what the Node console answered, made
while it still existed. They can't be recorded again. The Go tests replay
them and need no Node. Don't edit them: when a fix changes an answer on
purpose, its step goes into `activityFixes` in `fixes_test.go` with the row
id.

| File | What it holds | Replayed by |
|---|---|---|
| `node_sqlite.json`, `node_postgres.json` | the three read routes of `routes/activity.routes.js` | `TestNodeAnswers` (`replay_test.go`) |
| `node_device_sqlite.json`, `node_device_postgres.json` | the desktop agent's route, `POST /activity` at `/api/nd` and `/api/activity` | `TestNodeDeviceAnswers` (`device_test.go`) |

## What is recorded

The read routes ran with the real `services/dbAdapter.js` on a scratch
database: 684 steps on SQLite and 675 on PostgreSQL.

- every route for every role that can sign in, and for nobody
- devices whose note and info hold every kind of value (an info that is
  an object, a list, a number, a flag, null, text, JSON inside a text, and
  on SQLite text that is no JSON), with the name each is listed under
- the address in other spellings: letter case, slashes, escapes
- `from` and `to` in every form: dates and times in several notations,
  empty, not a time, given twice, in brackets, by index, as an object, both
  mixed
- `limit` in every form: whole numbers, fractions, signs, hexadecimal,
  text, numbers beyond every integer type, lists and objects
- a device with more sessions and more apps than a request without a limit
  shows, and a session that began before its day's report
- rows only SQLite can hold: fractions and text in number columns, numbers
  too large for JavaScript, times in other forms, one app in two categories
- devices that only the signal server's table (`peers`) has: before the
  table exists, then with rows in it, and what the lookups copy into the
  console's own table (`peer`), read back with a SELECT

The harness was an Express app with the console's body parsers, the JSON
content-type rule, a session set from a header, and the route file. It had
no panel access guard, so a guest issuer gets the route's own refusal in
the recording and the guard's in the console. All times in the rows are
fixed and lie in March 2019.

The device route ran behind the real device guards of
`middleware/deviceAuth.js`, with the real alert rules engine (its mail
stubbed, so no rule mails) and the real adapter: 1,421 steps per database.

- tokens in every state (unbound, a client id that is no device id, too
  long, expired, revoked, stored before the hash column, a numeric device
  id) and Authorization headers in several forms
- the device named in the body, the query and `X-Device-Id`, with values
  of every JSON type: the guard, then the route's own comparison
- sessions in every form, each member of a session with values of every
  type, 501 sessions, the total of the durations as `+` makes it
- the idle time and the time of the report with values of every type,
  and the idle alerts they raise (one rule that fires for every number)
- after each report the sessions, summaries and alerts it left, which are
  then deleted

The harness waited for the background evaluations of the alert rules
before each step; the Go test waits for the engine the same way. Times the
route fills in itself are checked by their form in a statement of their
own.
