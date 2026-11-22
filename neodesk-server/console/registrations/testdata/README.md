# Recordings of the old console for console/registrations

The file below is a fixed record of what the Node console's
`services/registrationNotifications.js` returned, made while the console
still existed. It can't be recorded again. The Go tests replay it and need
no Node. Don't edit it: when a fix changes a result on purpose, the test
lists that vector as an intended difference with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `notifications.json` | `lanRegistrationToNotification`, `enrollmentToNotification` and `listRegistrationNotifications` on many inputs | `TestLANNotificationVectors`, `TestEnrollmentNotificationVectors`, `TestNotificationListVectors` (`notifications_test.go`) |

## What is recorded

LAN rows and enrollment entries with times in every form (SQLite text, ISO
with and without zone and fraction, dates alone, six digit years, numbers,
flags, lists, PostgreSQL Dates) and fields of every JSON type, and whole
lists from both sources, with one of them failing. The clock was fixed and
the time zone was Europe/Berlin, so a time without a zone and a value that
is no date give the same answer in Go. `{"$undefined": true}` stands for
`undefined`, `{"$date": text}` for a Date.

The database methods of the package (`pending_registrations` in
`services/dbAdapter.js`) are pinned by the route recordings of
`console/routes/registration/testdata`, which ran them through the routes
on SQLite and PostgreSQL, and by the package's own tests.
