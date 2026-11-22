# Recordings of the old console for console/helpmail

`helpmail.json` is a fixed record of what the Node console's
`services/helpRequestEmailService.js` did, made while it still existed. It
can't be recorded again. The Go tests replay it on SQLite and PostgreSQL
and need no Node. Don't edit it: when a fix changes a result on purpose,
the test lists that record as an intended difference with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `helpmail.json` | `parseCommercializationEmailConfig` on stored values of every kind, and `handleHelpRequestEvent` on events in seven scenarios under each stored configuration, with every call and log line | `TestConfigVectors`, `TestEventVectors` (`helpmail_test.go`) |

The service's neighbours were stubbed the way the Node console's own test
stubbed them (the database, the mail service, the folder lookups).
