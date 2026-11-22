# Recordings of the old console for console/routes/rustdeskapi

The `node_*.json` and `flow_*.json` files below are fixed records of what
the Node console answered, made while it still existed. They can't be
recorded again. The Go tests replay them and need no Node. Don't edit
them: when a fix changes an answer on purpose, its step goes into
`fixedSteps` (`differences_test.go`) with the row id, and a run with
`RDAPI_RECORD_FIXED=1` writes the Go console's answer into the
`_fixed.json` file of that recording.

| File | What it holds | Replayed by |
|---|---|---|
| `node_sqlite.json`, `node_postgres.json` | the client API (`routes/rustdesk-api.routes.js`) on each database | `TestNodeAnswers` (`oracle_test.go`) |
| `node_sqlite_fixed.json`, `node_postgres_fixed.json` | the Go console's answers to the steps in `fixedSteps` | `TestNodeAnswers` |
| `flow_sqlite.json`, `flow_postgres.json` | the client API spoken the way a desktop client does, against a test instance of the Node console | `TestClientAPIFlow` (`flow_test.go`, build tag `rdflow`) |
| `flow_sqlite_fixed.json`, `flow_postgres_fixed.json` | the Go console's answers to the flow steps in `fixedSteps` | `TestClientAPIFlow` |

`TestNodeAnswers` replays the requests on the same rows behind the whole
Go pipeline and must get the same status, the same Content-Type, the same
body byte for byte (access tokens and second-step secrets are random on
both sides and compared by their form) and the same writes to the API
server.

## What is recorded

For the `node_*.json` files the recorder ran the route file with the real
`services/dbAdapter.js`, `services/authService.js`,
`services/rustdeskAddressBookSync.js`, `services/deviceGroupService.js` and
`services/serverBackend.js` on a scratch database, in an Express app with
the console's body parsers, behind a scripted API server that listed six
devices and took tag writes (`apiStub` in `oracle_test.go` is the same).
Requests came from 203.0.113.7, the address the Go test uses.

Per database: address books written in every form a client may send (JSON
text, malformed text, a list, an object, numbers, flags, null, the empty
text, keys that JavaScript reorders, numbers it reformats, escapes, control
characters, long and odd tags), each read back as legacy and personal book
and as tags, for a viewer, an operator (whose tags go to the devices), a pro
account, a guest issuer and an admin, with a device group only the admin
may see; heartbeats and system information of known, banned, deleted and
unknown devices (also devices only the signal server's table has, which the
lookup copies), with the rows they leave; audit uploads with values of every
JSON type and the lists of them; sign-ins with client ids and UUIDs of every
JSON type, the token rows they leave, sign-outs, and the audit entries.

The `flow_*.json` files hold the method and path of each request and the
answer with every token, secret and random id masked; no request header
and no request body. `flow_test.go` says what the flow covers.
