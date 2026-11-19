# Node oracle for console/routes/generator

`generator.js` runs the Node console's generator routes
(`routes/generator.routes.js` with the real `dbAdapter`, template module,
build queue and bundle service) on a scratch database and records every
answer. `TestNodeAnswers` replays the requests on the same rows and must
get the same status and body, byte for byte, and for downloads the same
headers.

This folder is a test fixture for the time both consoles exist. The route
file it loads is gone from `web-nodejs` since the routes moved to Go, so the
script runs only on a tree that still has it, named by `WEB_NODEJS`. The
files were last recorded on upstream 3.5.264 (relay addresses with a port).
The recorded files (`../node_sqlite.json`, `../node_postgres.json`) are
plain data and keep pinning the Go code to what Node answered.

What is recorded: the module installed from an uploaded template archive
(made by the script: six portable templates with stub binaries), bundles
created, listed, changed, rebuilt, revoked and deleted, every validation
error, the download portal with a ready build (with ranges and HEAD),
previews of odd profiles, the old toml generator with relay addresses of
every form. The signing seed and the
server key are made up. Bundle ids count up from 000000000001 (the script
replaces `crypto.randomBytes` for six bytes; the Go test gives the
generator the same source). Creation times are fixed by statements between
the requests, so the newest-first lists have one order. The paths of the
scratch folder are written as `<DATA>`.

`lib.js` is the harness of the tenants group with three additions: binary
request bodies (`bodyBase64`), `<ID:slug>` in a path for the id of the
bundle with that slug, and `scrub` for the recorded answers. It also has the
console's language and no-store middleware.

    tools/dev.sh "cd betterdesk-server/console/routes/generator/testdata && node nodeoracle/generator.js"
    DEV_DOCKER_ARGS="--network bdgo" tools/dev.sh "cd betterdesk-server/console/routes/generator/testdata && \
      ORACLE_PG_DSN='postgres://betterdesk:bdgo-local@bdgo-testpg:5432/betterdesk_test?sslmode=disable' node nodeoracle/generator.js"

For a tree outside the worktree, mount it and its node_modules and name it:

    DEV_DOCKER_ARGS="--network bdgo -v C:/path/to/tree/web-nodejs:/spec/web-nodejs \
      -v bdgo-node-nm:/spec/web-nodejs/node_modules -e WEB_NODEJS=/spec/web-nodejs" tools/dev.sh "..."
