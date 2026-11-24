# Recordings of the old console for console/routes/devices

`node_<db>.json` and `node_agent_<db>.json` are fixed records of what the
Node console answered, made while it still existed. They can't be recorded
again. The Go tests replay them and need no Node. Don't edit them: when a
fix changes an answer on purpose, its step goes into `fixedSteps` in
`fixed_test.go` with the row id, and the replay compares that step with the
Go console's answer in `<recording>_<db>_fixed.json` (written by a run with
`DEVICES_RECORD_FIXED=1`).

| File | What it holds | Replayed by |
|---|---|---|
| `node_sqlite.json`, `node_postgres.json` | 464 requests per database to `routes/devices.routes.js` | `TestNodeAnswers` (`node_test.go`) |
| `node_agent_sqlite.json`, `node_agent_postgres.json` | 157 steps per database of the routes that reach into an agent | `TestNodeAgentAnswers` (`agent_test.go`) |
| `node_sqlite_fixed.json`, `node_postgres_fixed.json`, `node_agent_sqlite_fixed.json`, `node_agent_postgres_fixed.json` | the Go console's answers for the steps a bug fix changed | the same tests |

## The device routes

The routes ran with the real `services/dbAdapter.js`, the real API client
and the real i18n middleware on a scratch database, in front of an API
server that answered from a table. The recordings were made from the
specification tree of the upstream 3.5.264 merge. Each step holds the
answer, every call the routes made to the API server, and the rows.

The requests cover the list with every filter and sort parameter (also as
lists and objects), one device with its system information, metrics,
telemetry, connection mode in every shape the server can give, who banned
it; availability and sessions; telemetry commands; remote targets; tags
(list, rename, delete, set); device groups (create, change, delete, the
folder's group), members; every write on a device with values of every
JSON type; delete, restore, bulk delete, ban, a new id in 20 spellings; the
access policy, the connect password, the connection mode; the rules for
RustDesk clients with their assignments.

The device group writes go to the API server's panel grouping
(`/api/panel/device-groups`) with every answer it can give, including none
(`hangup`), and fall back to the console's own writer where Node did. The
device scope check in front of every address below `/api/devices/:id` is
sent every route, a deleted device, an unknown one, `bulk-delete`, other
spellings and paths no route has, the folder route of
`routes/folders.routes.js` (mounted after the device routes, as in
`routes/index.js`), and at the end a check that fails (a dropped table).

The roles are admin, operator (who may not see three of the devices),
viewer, guest_issuer, the three other unrestricted roles and signed out,
for reads and writes the panel access guard lets through. A request that
reached the end of the recorder's app (`statusOnly`) is compared by status
and calls only, since that 404 was the recorder's own. The API server
answers success, refusals, 500 without a body, text and JSON of the wrong
type. The rows of the member, folder, assignment, guid and audit tables
are recorded on the way.

Random guids and the times the database fills in are compared by their
form. A tag cut in the middle of a character outside the Basic
Multilingual Plane is compared as U+FFFD (BUGS DGR2).

## The routes that reach into an agent

These steps cover the routes that ask a running agent (services,
processes, events, activity, the file routes, clipboard, screenshot,
mouse, key, text, terminal). They ran with the real `services/bdRelay.js`,
the real database adapter and the API server table, and a made-up agent
of device A1001 connected to `/ws/nd-signal` that answered each request
the way the step says: data of every JSON type, an error of every type
(`agent_offline` and `agent_timeout` included), or nothing (the route's
timeout). B1002 has no agent, so its steps show the server's snapshot, a
queued collection, 503; the API server's telemetry and queue answers vary.
C1003 is out of e2e_operator's scope, and every route is also sent for it
as e2e_operator, where the device scope check answers first. Bodies and
queries are odd on purpose.

Each step holds the answer, the calls to the API server, the requests the
agent got (request id as `<id>`) and the audit rows. `TestNodeAgentAnswers`
replays them through the whole console with a Go made-up agent
(`console/ndrelay/fakeagent`) that has a real access token of A1001.

The recorder's app had no panel access guard and ended with a stand-in
error handler, so its steps stay within what the guard lets through, and
for an answer of that handler (a body that is not JSON, one over the size
limit) only the status is compared.

Not in the recording, because it ended the Node process: a body value
`String()` throws on in an async route (BUGS WS9).
`TestAgentRequestThatWouldEndNode` covers the Go side.

Long runs of one piece of text (the megabyte bodies of the size limits)
are stored as run markers; the test expands them with `wstest.Expand`.
