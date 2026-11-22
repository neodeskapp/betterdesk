# Recordings of the old console for console/routes/tenants

The files below are fixed records of what the Node console answered, made
while it still existed. They can't be recorded again. The Go tests replay
them and need no Node. Don't edit them: when a fix changes an answer on
purpose, its step goes into `tenantFixes` (`fixed_test.go`) with the row
id.

| File | What it holds | Replayed by |
|---|---|---|
| `node_sqlite.json`, `node_postgres.json` | 828 requests per database to `routes/tenants.routes.js` | `TestNodeAnswers` (`replay_test.go`) |

The Go test replays the requests on the same rows and must get the same
status and the same body, byte for byte.

## What is recorded

The recorder ran the route file with the real `services/dbAdapter.js` on a
scratch database, in an Express app with the console's body parsers, the
JSON content-type rule and a session set from a header.

Per database: every route as every role and signed out, names and the
slugs made from them, every field with values of every JSON type (text,
numbers, flags, null, lists, objects), ids in 34 spellings on every route
that takes one, assignments of devices and users, statistics, deletes.

Two statements between the requests gave assignments fixed times, so that
lists ordered by time have one order. Times the database fills in itself
are compared by their form (text of the second on SQLite, ISO with
milliseconds on PostgreSQL).
