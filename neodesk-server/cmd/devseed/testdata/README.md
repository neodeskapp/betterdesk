# Recordings of the old console for cmd/devseed

`rows.json.gz` is a fixed record of what the Node console's demo seed
(`web-nodejs/scripts/dev/seed-ui-data.js`) wrote straight into the
database, made while it still existed. It can't be recorded again. The Go
tests replay it and need no Node. Don't edit it: when a change to the demo
data differs from the Node seed on purpose, the test says so with the row
id.

| File | What it holds | Replayed by |
|---|---|---|
| `rows.json.gz` | the seed's rows at six fixed times, and fingerprints of its rows at 200 more | `oracle_test.go` |

## What is recorded

The seed script ran unchanged on a recording stand-in for better-sqlite3,
from a copy of the old console in which the old project's names were
replaced by NeoDesk's, so the rows hold NeoDesk's names.

- Six cases at fixed times, two of them with the demo accounts already
  there: every INSERT as `{table, row}` and the UPDATE of the contract
  counters. `TestDirectRowsAreTheNodeSeedsOnSQLite` and
  `TestDirectRowsAreTheNodeSeedsInPostgresForms` seed fresh databases at
  the same times and compare the tables.
- 200 more times: a SHA-256 per table of the rows the seed's builders
  made, each table's rows as lists of values in the order of the INSERT's
  columns, as JSON. `TestBuildersMatchTheNodeSeedAtManyTimes` compares
  them without a database.
