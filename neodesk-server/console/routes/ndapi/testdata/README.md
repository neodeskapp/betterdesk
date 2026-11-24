# Recordings of the old console for console/routes/ndapi

`node_sqlite.json` and `node_postgres.json` are fixed records of what the
Node console's device API answered, made while it still existed. They
can't be recorded again. The Go tests replay them and need no Node. Don't
edit them: when a fix changes an answer on purpose, `fixed_sqlite.json` and
`fixed_postgres.json` hold what the Go console gives since that fix, under
the row id (written by a run with `REPLAY_FIX=<row id>`, see `fixedStep`
in `replay_test.go`).

| File | What it holds | Replayed by |
|---|---|---|
| `node_sqlite.json`, `node_postgres.json` | 186 steps per database of `routes/bd-api.routes.js` at `/api/nd` | `TestNodeAnswers` (`replay_test.go`) |
| `fixed_sqlite.json`, `fixed_postgres.json` | the Go console's answers for the steps a bug fix changed | `TestNodeAnswers` |

## What is recorded

The device API ran with the real `services/dbAdapter.js`, sign-in service,
permission loading, panel access policy and guard, branding service,
registration limiter, `lib/enrollmentProof.js`, `lib/agentTemplates.js`
and API client, against a made-up API server. The test compares status and
body byte for byte after the times the database and the clock fill in and
the random session ids and tokens, and also the rows, audit entries and
calls to the API server.

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
  every type reaching the device table
- heartbeats, relay sessions (connect with every refusal, the session as
  initiator, target and somebody else, deleting it), the device lookups
- help requests and chat of a device, with the API server answering in
  every way
- the operator routes: sign-in, the device list, help requests (list,
  accept, resolve, delete) and the session history with every limit
- the notification bell for every kind of account, with limits, unread
  only, read marks and read-all

The made-up API server answered by method and path from a table saved in
the recording as `api`; a step can override answers. Role permission
lookups were answered from the built-in role map and are left out of the
comparison. The agents' relay was a stand-in for
`services/goConsoleBridge.js` with no agent online, which kept sessions as
the Go console's relay does (a value that is no text reaches it as ""). The
two notification writes skipped the CSRF check; the contract cases pin it.
Proofs were made at send time from the fixed key seeds in the recording
(`seeds`), so the Go test signs the same payloads with the same keys.

The recordings were made with `TZ=UTC`, and `TestNodeAnswers` refuses to
run in another zone.

`GET /api/nd/chat/history` past its checks ended the Node process (BUGS
BD1) and is not recorded; `TestChatGoesThroughTheAPIServer` pins the Go
side.
