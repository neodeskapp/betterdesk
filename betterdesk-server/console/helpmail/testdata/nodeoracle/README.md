# The Node console as the reference for `console/helpmail`

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

This folder is a test fixture. It stays now that `web-nodejs` is gone.

`helpmail.js` loads `web-nodejs/services/helpRequestEmailService.js` with
its neighbours stubbed the way `tests/helpRequestEmailService.test.js`
stubs them (the database, the mail service, the folder lookups), runs
`parseCommercializationEmailConfig` on stored values of every kind and
`handleHelpRequestEvent` on events in seven scenarios under each stored
configuration, and records every call and log line. It writes
`../helpmail.json`, which `helpmail_test.go` replays on SQLite and
PostgreSQL.

    tools/dev.sh "cd betterdesk-server/console/helpmail/testdata/nodeoracle && node helpmail.js"
