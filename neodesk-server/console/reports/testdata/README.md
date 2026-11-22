# Recordings of the old console for console/reports

The files below are fixed records of what the Node console's report engine
(`services/reportEngine.js` with the real `services/dbAdapter.js`) made on
a scratch database, made while the console still existed. They can't be
recorded again. The Go test replays them and needs no Node. Don't edit
them: when a fix changes a report on purpose, the case goes into
`engineFixes`, `engineRewrites` or `engineRules` in `engine_test.go` with
the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `engine_sqlite_Europe-Berlin.json` | the reports on SQLite, the process in Europe/Berlin | `TestNodeReports` (`engine_test.go`) |
| `engine_postgres_Europe-Berlin.json`, `engine_postgres_UTC.json` | the reports on PostgreSQL, the process in Europe/Berlin and in UTC | `TestNodeReports` |

The test runs the same statements on a fresh database and must make the
same reports, byte for byte: the JSON of every report, the CSV of every
part of it, and `toCsv` of rows written by hand.

## What is recorded

Worlds of rows in every table the engine reads (`peer`,
`activity_summaries`, `activity_sessions`, `dlp_events`, `audit_log` with
`users`, `tickets`, `device_inventory`, `alert_rules`, `alert_history`,
`network_targets`): empty tables, ordinary rows, NULLs, unicode, values
that need CSV quoting, names every JavaScript object inherits
(`constructor`, `__proto__`), names that are numbers, more rows than the
reports keep, and rows that make a part of a report fail. For every world
each report type was generated with many options, the activity report with
date ranges at day, month and daylight saving borders, and each part of
each report was written as CSV the way the export route picks it.

The clock was fixed (`new Date()` was 2026-11-10T12:34:56.789Z) and every
time column got a value of its own. Rows that sort equal (the same time,
two apps of one device with the same total) were seeded on SQLite only:
PostgreSQL gives them in no fixed order, so Node's own answer for them
changed from run to run. Times on PostgreSQL go into a CSV in the zone of
the process, hence the two PostgreSQL files.
