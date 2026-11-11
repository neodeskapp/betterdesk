# Node oracle for console/routes/activity

`activity.js` runs the three read routes of the Node console's
`routes/activity.routes.js` with the real `services/dbAdapter.js` on a
scratch database and records every answer. `TestNodeAnswers` in the Go
package replays the requests on the same rows and must get the same status
and the same body, byte for byte.

This folder is a test fixture for the time both servers exist. The read
routes are gone from the route file since they moved to Go, so the script
runs only on a tree that still has them (the commit before the move), or
with `ORACLE_ROUTES` naming a copy of the old route file below
`web-nodejs` (`ORACLE_ROUTES=routes/activity.old.routes`). The recorded
files stay: `../node_sqlite.json` and `../node_postgres.json` are
plain data and keep pinning the Go code to what Node answered on each
database.

## What is recorded

684 steps on SQLite and 675 on PostgreSQL:

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

`lib.js` is the harness: an Express app with the console's body parsers,
the JSON content-type rule, a session set from a header, and the route
file. It has no panel access guard, so a guest issuer gets the route's own
refusal in the recording and the guard's in the console.

All times in the rows are fixed and lie in March 2019.

## Running

From a worktree that still has the read routes, in the dev image:

    tools/dev.sh "cd betterdesk-server/console/routes/activity/testdata && node nodeoracle/activity.js"
    DEV_DOCKER_ARGS="--network bdgo" tools/dev.sh "cd betterdesk-server/console/routes/activity/testdata && \
      ORACLE_PG_DSN='postgres://betterdesk:bdgo-local@bdgo-testpg:5432/betterdesk_test?sslmode=disable' node nodeoracle/activity.js"
