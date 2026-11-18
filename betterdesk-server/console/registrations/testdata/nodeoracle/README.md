# Node oracle for console/registrations

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

This folder is a test fixture for the time both servers exist. It goes away
with `web-nodejs`; the recorded file stays.

## Running

In the dev image:

    tools/dev.sh "cd betterdesk-server/console/registrations/testdata && TZ=Europe/Berlin node nodeoracle/notifications.js"
