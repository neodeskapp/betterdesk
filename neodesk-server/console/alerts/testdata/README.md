# Recordings of the old console for console/alerts

The files here are fixed records of what the Node console's alert rules
engine (`web-nodejs/services/alertRulesEngine.js`) did, made while it still
existed. They can't be recorded again. The Go tests replay every record
and need no Node. Don't edit them: when a fix changes a result on purpose,
the test lists that record as an intended difference with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `cond.json.gz` | `evalCondition` for 64,896 combinations of value, operator and threshold: numbers, texts, lists, objects, the six operators, unknown ones, and the names every JavaScript object has | `TestEvalConditionAsNode` |
| `mem.json.gz` | 10,940 calls of `evaluateRules`, `evaluateTelemetry` and `periodicCheck` with the adapter's `getAlertRules`, `getRecentAlert` and `createAlert` replaced by functions that hand out rules with members of every type and write down their arguments; three runs of `evaluateInBackground` | `TestEngineAsNodeWithoutDatabase` |
| `db_sqlite.json.gz`, `db_postgres.json.gz` | the same three functions on the real adapter: 1,076 cases on SQLite and 856 on PostgreSQL, each with its rule rows (limits and cooldowns as each database keeps them), alerts raised some seconds ago, the rows written, the mails and the log | `TestEngineAsNodeOnRows` |
| `dates_utc.json.gz`, `dates_berlin.json.gz`, `dates_stjohns.json.gz` | `Date.parse` for 38,875 texts per time zone | `TestDateParseAsNode` |

## What is recorded

The engine ran with the mail service and the device list replaced by fakes
that recorded their calls. Values of any JavaScript type are written as the
recorder's `dump` wrote them, which `dumpBind` in `oracle_test.go` repeats.
The random sources were seeded.

The cases on real rows depend on the clock where a cooldown is involved:
alerts were seeded "N seconds ago" at run time, with at least two seconds
between N and the cooldown.

The `Date.parse` texts are ISO dates in every variation, the older forms
(`Nov 9 2026 10:00 PM PST`, `11/9/26`, numbers alone), random sequences of
numbers, month names, zone names and punctuation, and damaged copies of all
of them, parsed with the time zone set to UTC, Europe/Berlin and
America/St_Johns. The offline sweep reads a device's `last_online` with
`Date.parse`; the Go function that does the same is checked against these.
