# Node oracle for console/routes/users

`users.js` runs the Node console's account routes (`routes/users.routes.js`)
with the real `services/dbAdapter.js`, the real panel access policy and
guard, the translations, the password limiter and the API client, against a
made-up API server, and records every answer. `TestNodeAnswers` in the Go
package replays the requests on a database of the same shape and must get
the same status and body, byte for byte after the times the database fills
in and random guids, the same rows and audit entries, and the same calls to
the API server.

This folder is a test fixture for the time both servers exist. The route
file it loads is gone from `web-nodejs` since the routes moved to Go, so the
script runs only on a tree that still has it (the commit before the move).
The recorded files stay: `../node_*.json` are plain data and keep pinning
the Go code to what Node answered.

## What is recorded

About 470 requests and statements per database shape:

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
  writes for 64-bit ids), which never counts as the caller's own account.

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
