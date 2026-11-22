# Recordings of the old console for console/store

The files below are fixed records of the databases and values the Node
console made, taken while it still existed. They can't be recorded again.
The Go tests replay them and need no Node. Don't edit them: the shared
dumps got the signal server's `credentials_changed_at` column and trigger
by hand on 2026-11-21 (see `../schema.md`), and a change like that is the
only kind they take.

| File | What it holds | Replayed by |
|---|---|---|
| `../storetest/dumps/node-sqlite.sql`, `node-postgres.sql` | the schema the Node console created on an empty database | `storetest.NodeSchema`, `TestMigrateFreshEqualsNodeDump` (`schema_test.go`) |
| `dumps/shared-*.sql` | what a real Node console and signal server left behind together, in both start orders, on both databases | `TestSharedDatabaseStartOrders` (`shared_test.go`), `TestShapeOfSharedDumps` (`shape_test.go`) |
| `old/v3.0.0-*.sql`, `old/v3.4.0-*.sql`, `old/v3.5.200-*.sql` | the schemas those releases made with their own start code, with rows | `TestMigrateOldInstalls` (`old_test.go`) |
| `vectors/values-sqlite.json` | what better-sqlite3 returned for each column type, as JSON | `TestValuesMatchNode` (`values_test.go`) |
| `vectors/values-postgres-utc.json`, `vectors/values-postgres-berlin.json` | what node-postgres returned for each column type, as JSON, with the console running in that time zone | `TestValuesMatchNode`, `TestLocalWallClockMatchesNode` |
| `vectors/backfills.json` | slugs the console's migration gave a table of agent bundles, slugs of single names, token hashes, `toISOString` of some instants | `TestSlugAndTokenHashMatchNode` (`steps_test.go`), `TestTimeFormats` |
| `vectors/fractions.json` | the milliseconds node-postgres made of 4000 fractions of a second | `TestISOTimeCutsToMilliseconds` |

## What is recorded

The value files came from pg 8.23.1 with pg-types 2.2.0 and
better-sqlite3 13.0.3. `../schema.md` ("What the drivers return") sums
them up next to what the Go drivers return.

The value tests pick the PostgreSQL file by the time zone of the test
process. For the Berlin file run them with `TZ=Europe/Berlin`:

    DEV_DOCKER_ARGS="-e TZ=Europe/Berlin" tools/gotest.sh "./console/store/ -run 'TestValues|TestSelect|TestDriver' -count=1"
