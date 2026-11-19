# Node oracle for console/routes/bdapi

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

`bdapi.js` runs the Node console's device API (`routes/bd-api.routes.js`,
mounted at `/api/bd`) with the real `services/dbAdapter.js`, sign-in
service, permission loading, panel access policy and guard, branding
service, registration limiter, `lib/enrollmentProof.js`,
`lib/agentTemplates.js` and API client, against a made-up API server, and
records every answer. `TestNodeAnswers` in the Go package replays the
requests on a database of the same kind and must get the same status and
body, byte for byte after the times the database and the clock fill in
and the random session ids and tokens, the same rows, audit entries and
calls to the API server.

This folder is a test fixture from the time both servers existed. Once
`web-nodejs` loses the route file the script cannot run any more; the
recorded files `../node_sqlite.json` and `../node_postgres.json` stay and
keep pinning the Go code to what Node answered.

## What is recorded

186 steps per database:

- registrations: every way of naming a device (no header, headers too
  short, too long or sent twice, tokens unbound, of no device id, expired,
  revoked, blank, with spaces, a lower-case scheme), claims that differ,
  known devices refreshed, banned and removed devices, key and uuid
  changes with and without Ed25519 proofs (proofs by another key, too old,
  from the future, with an unreadable time, a nonce too long, a short
  signature, signed for another path, replayed), renamed ids (other uuid,
  same uuid in capitals, same address, same key, an id nobody has),
  admission by an approved request (with and without its key), by
  enrollment tokens (fresh, bound to another device, to this one, to
  nobody, unknown, a number, an object) and by proofs (a URL alphabet key,
  a path spelled otherwise), bodies that are lists or forms, and values of
  every type reaching the device table;
- heartbeats, relay sessions (connect with every refusal, the session as
  initiator, target and somebody else, deleting it), the device lookups;
- help requests and chat of a device, with the API server answering in
  every way;
- the operator routes: sign-in, the device list, help requests (list,
  accept, resolve, delete) and the session history with every limit;
- the notification bell for every kind of account, with limits, unread
  only, read marks and read-all.

The made-up API server answers by method and path from the table in the
script (saved in the recording as `api`); a step can override answers.
Role permission lookups are answered from the built-in role map and left
out of the comparison. The agents' relay is a stand-in for
`services/goConsoleBridge.js` with no agent online, which keeps sessions
as the Go console's relay does (a value that is no text reaches it as
""). The two notification writes skip the CSRF check here; the contract
cases pin it. Proofs are made at send time from the fixed key seeds in the
recording (`seeds`), so the Go test signs the same payloads with the same
keys.

`GET /api/bd/chat/history` past its checks ends the Node process (BUGS
BD1) and is not recorded; `TestChatHistoryDropsTheConnection` pins the Go
side.

## Running

From a worktree that still has the route file, in the dev image (the
recordings were made with `TZ=UTC`, which the Go test checks):

    tools/dev.sh "cd betterdesk-server/console/routes/bdapi/testdata && TZ=UTC node nodeoracle/bdapi.js"
    DEV_DOCKER_ARGS="--network bdgo" tools/dev.sh "cd betterdesk-server/console/routes/bdapi/testdata && \
      TZ=UTC ORACLE_PG_DSN='postgres://betterdesk:bdgo-local@bdgo-testpg:5432/betterdesk_test?sslmode=disable' node nodeoracle/bdapi.js"

On PostgreSQL the script creates a database of its own and drops it at the
end.
