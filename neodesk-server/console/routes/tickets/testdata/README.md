# Recordings of the old console for console/routes/tickets

The files below are fixed records of what the Node console answered, made
while it still existed. They can't be recorded again. The Go tests replay
them and need no Node. Don't edit them: when a fix changes an answer on
purpose, its step goes into `ticketFixes` or `deviceFixes`
(`fixed_test.go`) with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `node_sqlite.json`, `node_postgres.json` | 606 steps per database on the ticket routes | `TestNodeAnswers` (`replay_test.go`) |
| `node_device_sqlite.json`, `node_device_postgres.json` | the two device routes (`POST` and `GET /api/tickets/nd`) | `TestNodeDeviceAnswers` (`replay_test.go`) |

The Go tests replay the requests on the same rows and must get the same
status and the same body, byte for byte, and for downloads the same bytes
and headers.

## What is recorded

The recorder ran `routes/tickets.routes.js` with the real
`services/dbAdapter.js`, `lib/safePath.js` and Express's file sender on a
scratch database, in an Express app with the console's body parsers, the
JSON content-type rule, a session set from a header, and the console's
error answer. It had no panel access guard; the Go test says where that
matters.

Per database: every route as every role that can sign in and as nobody;
the list with every filter, the search with its wildcards and query
parameters given as lists; creating and updating with values of every JSON
type in every field; comments; uploads with file names, types and data in
every form `Buffer.from` takes or refuses; every attachment as a download
with its bytes, `Content-Type`, `Content-Disposition`, `Accept-Ranges` and
`Content-Range`, with range and conditional requests; deletes; the audit log
as rows; and the files left in the attachment folder at the end.

Statements between the requests gave new rows fixed times, so that lists
ordered by time have one order. Times the database fills in itself are
compared by their form. In the recording `<UPLOADS>` stands for the folder
the route wrote attachments to.

The device recordings ran the two device routes the same way, behind the
real `requireDeviceToken`, with token rows in every state in the seed.
