# Recordings of the old console for console/routes/users

The `node_*.json` files below are fixed records of what the Node console
answered, made while it still existed. They can't be recorded again. The
Go tests replay them and need no Node. Don't edit them: when a fix changes
an answer on purpose, its step goes into `fixes` (`fixes_test.go`) with the
row id.

| File | What it holds | Replayed by |
|---|---|---|
| `node_sqlite.json`, `node_postgres.json` | the account routes on databases the Node console set up itself | `TestNodeAnswers` (`replay_test.go`) |
| `node_sqlite-go.json`, `node_postgres-go.json` | the same on databases the signal server started first | `TestNodeAnswers` |

`TestNodeAnswers` replays the requests on a database of the same shape and
must get the same status and body, byte for byte after the times the
database fills in and random guids, the same rows and audit entries, and
the same calls to the API server.

`lang/en.json` is no recording: it holds the English texts of the
console's language file that the routes answer with in the tests.

## What is recorded

The recorder ran the Node console's account routes
(`routes/users.routes.js`, from the spec tree of upstream 3.5.264) with the
real `services/dbAdapter.js`, the real panel access policy and guard, the
translations, the password limiter and the API client, against a made-up
API server. About 570 requests and statements per database shape:

- every route as every role and signed out, with what is refused after the
  guards;
- user groups: names, notes and team ids of every JSON type and length (in
  UTF-16 units), guids with spaces, slashes, other letter case, too long;
  deleting a group with members, access lists and a panel access policy;
- creating accounts: every check in its order (both given, the username's
  form, taken, the user group guids, the password's strength, the role the
  caller may give), usernames and passwords that are numbers, flags, lists
  and objects, the email checked after the account is written, folders,
  device grants and strategies, every role created by global_admin,
  super_admin, admin and guest_issuer;
- the password limit: three per five minutes per address, shared by
  creating and resetting, counted after the guards;
- changing accounts: ids in every spelling, directory and single sign-on
  accounts, demoting oneself, roles above the caller's own (and the higher
  account global_admin may change anyway), what stays written when a later
  check refuses, the answer after each change;
- a panel access row that gives guest_issuer write access to the users
  section;
- the device scope of accounts, the organization proxy with every kind of
  answer of the API server;
- resetting passwords, deleting accounts, deleting oneself, the last
  administrator, and a session whose userId is text (the form the sign-in
  writes for 64-bit ids), which never counts as the caller's own account;
- the username rule of new accounts (3 to 64 of `A-Za-z0-9_.@-`);
- renaming accounts (`PATCH /api/users/:id {username}`): directory and
  single sign-on accounts, names that are no text, empty, the same after
  trimming, malformed, taken, missing accounts, the rows keyed by the name
  (address books, organization members, strategy assignments, lockouts,
  sign-in attempts), a name the cascade cannot take, and the rest of the
  request (role, folders, strategy, the caller's own demotion) under the
  new name.

The four shapes: `sqlite` and `postgres` are databases the Node console set
up itself; `sqlite-go` and `postgres-go` start from the schema the signal
server and the console leave when the signal server starts first
(`console/store/testdata/dumps/shared-*-go-first.sql`), where PostgreSQL
ids are 64-bit and reach the JSON as text.

The made-up API server answered by method and path from a table, which is
saved in the recording (`api`), so the Go test answers the same. It
answered the role permission lookups of the panel access policy with the
built-in role map. Calls the Go console does not make are left out of the
comparison: the user list Node's user sync read on PostgreSQL to copy
roles back (not ported, DECISIONS A17, BUGS D13).

On PostgreSQL the spec tree's rename cascade sent its statements without
waiting for them and numbered their placeholders on from one statement to
the next: the rename answered success, changed nothing, and the rejected
promises ended the process (BUGS.md UM22). The PostgreSQL recordings have
the cascade done as meant, which is what the Go console does. On SQLite the
real cascade ran: where the Node console made `address_books` (keyed by
user id) every rename answers 500, as it did in Node.
