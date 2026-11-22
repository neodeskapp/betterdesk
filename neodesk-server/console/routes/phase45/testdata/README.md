# Recordings of the old console for console/routes/phase45

The files below are fixed records of what the Node console answered, made
while it still existed. They can't be recorded again. The Go tests replay
them and need no Node. Don't edit them: when a fix changes an answer on
purpose, the replay in `replay_test.go` says how that step differs, with
the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `node_sqlite.json`, `node_postgres.json` | the answers of `routes/phase4_5.routes.js` on each database | `TestNodeAnswers` (`replay_test.go`) |
| `releases.json` | the release cache the routes answered from; the test writes it into the data folder before the replay | `TestNodeAnswers` |

The Go test replays the requests on the same rows and the same release
cache and must get the same status and body, byte for byte, and for the
downloads the same `Content-Type`, `Content-Disposition`, `Cache-Control`
and `Accept-Ranges`.

## What is recorded

The recorder ran the route file with the real `services/dbAdapter.js`,
avatar service and release service on a scratch database and data folder.
Per database: the profile and the consent popup's data for accounts with
full, partial, NULL and (SQLite) numeric profile columns; saves with values
of every JSON type, long and non-ASCII values, the e-mail limit, values
that cannot be made text and picture addresses that are no web address (no
picture is fetched); the audit log and the stored pictures afterwards;
templates and enrollment with every kind of name, id and token; the
release list, the signing key, the portal's installers and downloads from
the cache in `releases.json` (sorting, manifests that are no object,
cannot be read or are null, folders without a manifest, a file at the top
of the cache, names with dots and spaces).

On PostgreSQL the recorder added the six profile columns to `users` first,
as the Go console's schema has them on every database.

Pairing (`POST /api/agent-pairing/activate`) and the release import
(`POST /api/agent-releases/sync`) call the API server and GitHub, so they
are not recorded; their tests in the Go package use made-up ones.
