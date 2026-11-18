# The Node console as the reference for `console/helpmail`

This folder is a test fixture. It goes away together with `web-nodejs`.

`helpmail.js` loads `web-nodejs/services/helpRequestEmailService.js` with
its neighbours stubbed the way `tests/helpRequestEmailService.test.js`
stubs them (the database, the mail service, the folder lookups), runs
`parseCommercializationEmailConfig` on stored values of every kind and
`handleHelpRequestEvent` on events in seven scenarios under each stored
configuration, and records every call and log line. It writes
`../helpmail.json`, which `helpmail_test.go` replays on SQLite and
PostgreSQL.

    tools/dev.sh "cd betterdesk-server/console/helpmail/testdata/nodeoracle && node helpmail.js"
