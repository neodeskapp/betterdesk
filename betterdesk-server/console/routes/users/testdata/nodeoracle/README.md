# Node oracle for console/routes/users

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

`users.js` runs the Node console's account routes (`routes/users.routes.js`)
with the real `services/dbAdapter.js`, the real panel access policy and
guard, the translations, the password limiter and the API client, against a
made-up API server, and records every answer. `TestNodeAnswers` in the Go
package replays the requests on a database of the same shape and must get
the same status and body, byte for byte after the times the database fills
in and random guids, the same rows and audit entries, and the same calls to
the API server.

This folder is a test fixture from the time both servers existed. The route
file it loads is gone from `web-nodejs` since the routes moved to Go, so the
script runs on a tree that still has it: by default the spec tree of
upstream 3.5.264 mounted at `/spec/web-nodejs`.
The recorded files stay: `../node_*.json` are plain data and keep pinning
the Go code to what Node answered.

## What is recorded

About 570 requests and statements per database shape:

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

The made-up API server answers by method and path from the table in the
script, which is saved in the recording (`api`), so the Go test answers the
same. It answers the role permission lookups of the panel access policy with
the built-in role map. Calls the Go console does not make are left out of
the comparison: the user list Node's user sync reads on PostgreSQL to copy
roles back (not ported, DECISIONS A17, BUGS D13).

## Running

From a worktree that still has the route file, in the dev image:

    tools/dev.sh "cd betterdesk-server/console/routes/users/testdata && node nodeoracle/users.js"
    tools/dev.sh "cd betterdesk-server/console/routes/users/testdata && ORACLE_SHAPE=go node nodeoracle/users.js"
    DEV_DOCKER_ARGS="--network bdgo" tools/dev.sh "cd betterdesk-server/console/routes/users/testdata && \
      ORACLE_PG_DSN='postgres://betterdesk:bdgo-local@bdgo-testpg:5432/betterdesk_test?sslmode=disable' node nodeoracle/users.js"
    (the same with ORACLE_SHAPE=go)

On PostgreSQL the script creates a database of its own and drops it at the
end: the Node console looks for existing columns in every schema of a
database, so a schema next to other tests' tables would miss columns.

`WEB_NODEJS` names another tree. The recordings in `..` were made from the
spec tree (`C:\Users\info\worktrees\betterdesk-go-spec264\web-nodejs`):

    DEV_DOCKER_ARGS="--network bdgo -v <spec tree>/web-nodejs:/spec/web-nodejs -v bdgo-node-nm:/spec/web-nodejs/node_modules" \
      tools/dev.sh "cd betterdesk-server/console/routes/users/testdata && node nodeoracle/users.js"

On PostgreSQL the spec tree's rename cascade sends its statements without
waiting for them and numbers their placeholders on from one statement to
the next: the rename answers success, changes nothing, and the rejected
promises end the process (BUGS.md UM22). The script records PostgreSQL
with the cascade done as meant (`fixCascadeOnPostgres`), which is what the
Go console does. On SQLite the real cascade runs: where the Node console
made `address_books` (keyed by user id) every rename answers 500, as it
does in Node.
