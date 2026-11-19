# Node oracle for console/alerts

`web-nodejs` is gone from this tree. These scripts stay as the record of
how the recordings in this package were made; the Go tests replay the
recordings and need no Node. To run a script again, use a tree that still
has `web-nodejs`: the spec worktree
`C:\Users\info\worktrees\betterdesk-go-spec264`. Mount its `web-nodejs`,
with its modules, over `/repo/web-nodejs` of this tree:

    DEV_DOCKER_ARGS="-v C:/Users/info/worktrees/betterdesk-go-spec264/web-nodejs:/repo/web-nodejs -v bdgo-node-nm:/repo/web-nodejs/node_modules" \
      tools/dev.sh "cd betterdesk-server/<this folder> && node <script>"

Its Node code is upstream's release there, so a script that loads a file
the port deleted or changed on the way can fail or record other answers.

These scripts run the Node console's alert rules engine
(`web-nodejs/services/alertRulesEngine.js`) and record what it does. The Go
tests in `console/alerts` replay every record and must do the same.

This folder is a test fixture from the time both servers existed. It needs
`web-nodejs`, which this tree no longer has. The recorded files in the parent folder
(`*.json.gz`) stay: they are plain data and keep pinning the Go code to what
Node did.

## Running

In the dev image, from `betterdesk-server/console/alerts/testdata`:

    tools/dev.sh "cd betterdesk-server/console/alerts/testdata && node nodeoracle/engine.js && \
      TZ=UTC node nodeoracle/dates.js utc && TZ=Europe/Berlin node nodeoracle/dates.js berlin && \
      TZ=America/St_Johns node nodeoracle/dates.js stjohns"
    DEV_DOCKER_ARGS="--network bdgo" tools/dev.sh "cd betterdesk-server/console/alerts/testdata && \
      ORACLE_PG_DSN='postgres://betterdesk:bdgo-local@bdgo-testpg:5432/betterdesk_test?sslmode=disable' node nodeoracle/engine.js"

The scripts use fixed seeds. They work in a scratch folder under the
system's temporary folder (SQLite) or in a schema of their own (PostgreSQL)
and write nothing else outside `testdata`.

## Scripts

`lib.js` is the same helper as in `console/mail/testdata/nodeoracle`: the
scratch database with the real `services/dbAdapter.js`, the seeded random
source, and `dump` for values of any JavaScript type.

`engine.js` loads the engine with the mail service and the device list
replaced by fakes that record their calls. It writes:

| File | What it holds |
|---|---|
| `cond.json.gz` | `evalCondition` for 64,896 combinations of value, operator and threshold: numbers, texts, lists, objects, the six operators, unknown ones, and the names every JavaScript object has |
| `mem.json.gz` | 10,940 calls of `evaluateRules`, `evaluateTelemetry` and `periodicCheck` with the adapter's `getAlertRules`, `getRecentAlert` and `createAlert` replaced by functions that hand out rules with members of every type and write down their arguments; three runs of `evaluateInBackground` |
| `db_sqlite.json.gz`, `db_postgres.json.gz` | the same three functions on the real adapter: 1,076 cases on SQLite and 856 on PostgreSQL, each with its rule rows (limits and cooldowns as each database keeps them), alerts raised some seconds ago, the rows written, the mails and the log |

The cases on real rows depend on the clock where a cooldown is involved:
alerts are seeded "N seconds ago" at run time, with at least two seconds
between N and the cooldown.

`dates.js` records `Date.parse` for 38,875 texts per time zone
(`dates_utc.json.gz`, `dates_berlin.json.gz`, `dates_stjohns.json.gz`): ISO
dates in every variation, the older forms (`Nov 9 2026 10:00 PM PST`,
`11/9/26`, numbers alone), random sequences of numbers, month names, zone
names and punctuation, and damaged copies of all of them. The offline sweep
reads a device's `last_online` with `Date.parse`; the Go function that does
the same is checked against these.
