# Node oracle for console/routes/phase45

`phase45.js` runs the Node console's `routes/phase4_5.routes.js` with the
real `services/dbAdapter.js`, avatar service and release service on a
scratch database and data folder, and records every answer (`lib.js` is the
harness of the tenants group). `TestNodeAnswers` in the Go package replays
the requests on the same rows and the same release cache and must get the
same status and body, byte for byte, and for the downloads the same
`Content-Type`, `Content-Disposition`, `Cache-Control` and `Accept-Ranges`.

What is recorded, per database: the profile and the consent popup's data
for accounts with full, partial, NULL and (SQLite) numeric profile columns;
saves with values of every JSON type, long and non-ASCII values, the e-mail
limit, values that cannot be made text and picture addresses that are no
web address (no picture is fetched); the audit log and the stored pictures
afterwards; templates and enrollment with every kind of name, id and token;
the release list, the signing key, the portal's installers and downloads
from the cache in `releases.json` (sorting, manifests that are no object,
cannot be read or are null, folders without a manifest, a file at the top
of the cache, names with dots and spaces).

On PostgreSQL the script adds the six profile columns to `users` first, as
the Go console's schema has them on every database.

Pairing (`POST /api/agent-pairing/activate`) and the release import
(`POST /api/agent-releases/sync`) call the API server and GitHub; their
tests in the Go package use made-up ones.

This folder is a fixture for the time both servers exist. The route file is
gone from `web-nodejs` since the routes moved to Go, so the script runs only
on a tree that still has it. The recorded files stay.

    tools/dev.sh "cd betterdesk-server/console/routes/phase45/testdata && node nodeoracle/phase45.js"
    DEV_DOCKER_ARGS="--network bdgo" tools/dev.sh "cd betterdesk-server/console/routes/phase45/testdata && \
      ORACLE_PG_DSN='postgres://betterdesk:bdgo-local@bdgo-testpg:5432/betterdesk_test?sslmode=disable' node nodeoracle/phase45.js"
