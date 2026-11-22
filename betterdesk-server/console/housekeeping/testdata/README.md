# Recordings of the old console for console/housekeeping

The two files here are fixed records of what the periodic clean-ups of the
Node console's database adapter (`web-nodejs/services/dbAdapter.js`) did,
made while it still existed: the seed statements, the rows left or changed
and the timer delays Node used. They can't be recorded again. The Go tests
replay every record and need no Node. Don't edit them: when a fix changes
a result on purpose, the test lists that record as an intended difference
with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `housekeeping_sqlite.json`, `housekeeping_postgres.json` | the parts below, per database | `housekeeping_test.go` |

| Part | What it holds |
|---|---|
| `integration` | `runIntegrationHousekeeping` on metrics and client audit rows of many ages, in every time form SQLite can hold (text with a space or a `T`, a date, a number, NULL, garbage); a second run on a database without `audit_files`, which stops there |
| `stale` | `cleanupStaleOnlinePeers` with each recorded `HEARTBEAT_STALE_THRESHOLD`, read as server.js read it, on online and offline devices of many ages |
| `settings` | `STATUS_SYNC_INTERVAL` and `HEARTBEAT_STALE_THRESHOLD` read as `parseInt(value, 10) \|\| default`, and the delay Node's `setInterval` used (one millisecond for what does not fit a timer) |

Rows were placed relative to the time of the run, far from every
threshold, so the replay lands on the same side.
