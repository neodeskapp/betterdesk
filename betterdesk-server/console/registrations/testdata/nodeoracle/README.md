# Node oracle for console/registrations

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

`notifications.js` calls the real `services/registrationNotifications.js`
(`lanRegistrationToNotification`, `enrollmentToNotification`,
`listRegistrationNotifications`) with many inputs and writes what they
returned to `../notifications.json`: LAN rows and enrollment entries with
times in every form (SQLite text, ISO with and without zone and fraction,
dates alone, six digit years, numbers, flags, lists, PostgreSQL Dates) and
fields of every JSON type, and whole lists from both sources, with one of
them failing. The clock is fixed and the time zone is Europe/Berlin, so a
time without a zone and a value that is no date give the same answer in
Go. The Go tests in this package replay every vector.

The database methods of the package (`pending_registrations` in
`services/dbAdapter.js`) are pinned by the route recordings of
`console/routes/registration/testdata`, which run them through the routes
on SQLite and PostgreSQL, and by the package's own tests.

This folder is a test fixture from the time both servers existed. It stays now that `web-nodejs` is gone; the recorded file stays.

## Running

In the dev image:

    tools/dev.sh "cd betterdesk-server/console/registrations/testdata && TZ=Europe/Berlin node nodeoracle/notifications.js"
