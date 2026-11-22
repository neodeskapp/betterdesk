# Recordings of the old console for console/routes/reports

The files below are fixed records of what the Node console answered, made
while it still existed. They can't be recorded again. The Go tests replay
them and need no Node. Don't edit them: when a fix changes an answer on
purpose, its step goes into `reportFixes` or `answerFixes`
(`replay_test.go`) with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `node_sqlite.json`, `node_postgres.json` | the answers of `routes/reports.routes.js` on each database | `TestNodeAnswers` (`replay_test.go`) |

The Go test replays the requests on the same rows behind the Go console's
whole pipeline and must get the same status, the same body byte for byte,
and for the CSV export the same `Content-Type` and `Content-Disposition`.

## What is recorded

The recorder ran the route file with the real `services/reportEngine.js`
and `services/dbAdapter.js` on a scratch database, in an Express app with
the console's body parsers, the JSON content-type rule and a session set
from a header. The recordings were made with `TZ=UTC`.

Every route as the accounts whose answer the route decides (signed out,
super_admin and admin everywhere; pro, viewer, operator, server_admin and
global_admin on the reads, where the panel access guard lets them through):
the types, every report type and many wrong ones, date ranges and filters of
every JSON type, the CSV of every part of every report, saved reports whose
payload is JSON, no JSON, NULL, `0` or a JSON string, ids in many spellings,
saves with titles and types of every JSON type, and deletes of every row.

Seeded times lie before 2020 and are compared as they are. Times the run
made itself (`generated_at`, a saved report's `created_at`, the default
title and the date in the file name of an export) are compared by their
form.
