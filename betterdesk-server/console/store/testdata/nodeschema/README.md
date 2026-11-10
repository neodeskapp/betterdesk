# Node fixtures for the store tests

Everything in this folder runs code of the Node console (`web-nodejs`). It is
there to prove that the Go console creates and reads the database the way the
Node console did. Delete the folder, `nodeschema_test.go` and the build tag
when `web-nodejs` is removed. The vectors in `../vectors`, the dumps in
`../dumps`, `../old` and `storetest/dumps` stay: they are recorded output and
need no Node.

## init.js

Runs the Node console's own start code, `adapter.init()` of
`web-nodejs/services/dbAdapter.js`, against a database, and with `snapshot`
also writes what the console then reads: the answers of its list methods and
every row of every table as the driver returns it.

The tests in `nodeschema_test.go` use it. They are behind the build tag
`nodeschema` because they need `node` and the console's `node_modules`:

    tools/gotest.sh "-tags nodeschema ./console/store/ -run TestNode -count=1"

They compare, on SQLite and on PostgreSQL:

- a database made by `Migrate` with one made by `init()` (the whole catalog),
- the same with the signal server's tables, in both start orders,
- old installs upgraded by `Migrate` and by `init()`,
- what the Node console reads before and after `Migrate` ran on its database,
- that the Node console starts on a database only `Migrate` ever touched.

On PostgreSQL each run gets a database of its own, not a schema: the Node
console looks for existing columns in every schema of a database.

## vectors.js

Writes the files in `../vectors`. Run it again when the Node console's drivers
or its start code change:

    cd betterdesk-server/console/store/testdata/nodeschema
    DATABASE_URL=postgres://... TZ=UTC node vectors.js
    DATABASE_URL=postgres://... TZ=Europe/Berlin node vectors.js

| File | Content |
|---|---|
| `values-sqlite.json` | what better-sqlite3 returns for each column type, as JSON |
| `values-postgres-utc.json`, `values-postgres-berlin.json` | what node-postgres returns for each column type, as JSON, with the console running in that time zone |
| `backfills.json` | slugs the console's migration gave a table of agent bundles, slugs of single names, token hashes, `toISOString` of some instants |
| `fractions.json` | the milliseconds node-postgres made of 4000 fractions of a second |

The value tests replay the files on both databases. For the Berlin file run
them with `TZ=Europe/Berlin`:

    DEV_DOCKER_ARGS="-e TZ=Europe/Berlin" tools/gotest.sh "./console/store/ -run 'TestValues|TestSelect|TestDriver' -count=1"
