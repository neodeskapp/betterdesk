# Recordings of the old console for console/routes/registration

The `node_*.json` files below are fixed records of what the Node console
answered, made while it still existed. They can't be recorded again. The
Go tests replay them and need no Node. Don't edit them: when a fix changes
an answer on purpose, it goes into `fixes` (`replay_test.go`) with the row
id, and a run with `REPLAY_FIX=<row id>` writes the steps that differ into
`fixed_<database>.json`.

| File | What it holds | Replayed by |
|---|---|---|
| `node_sqlite.json`, `node_postgres.json` | 658 steps per database on the registration routes | `TestNodeAnswers` (`replay_test.go`) |
| `fixed_sqlite.json`, `fixed_postgres.json` | what the Go console answers since the fixes they name, for the steps a fix changed | `TestNodeAnswers` |

The Go test replays the requests on a database of the same kind and must
get the same status and body, byte for byte after the times the database
fills in and the random access tokens, the same rows, audit entries and
calls to the API server, and the same events on `/ws/panel-events`. The
recordings were made with `TZ=UTC`, which the Go test checks.

## What is recorded

The recorder ran the Node console's registration routes
(`routes/registration.routes.js`, mounted at the root and at `/api/bd`)
with the real `services/dbAdapter.js`, the real permission loading, panel
access policy and guard, translations, JSON content-type rule, API client,
device layer (`services/serverBackend.js`), device group service and key
service, against a made-up API server. Per database:

- every route at both mounts as every role and signed out;
- the device request with bodies of every kind (ids of every length in
  UTF-16 units, white space around them, numbers, lists, objects, fields
  cut in the middle of a pair, raw JSON that is no object, forms, text),
  the address from `X-Forwarded-For`, `X-Real-IP` and the connection, and
  a device asking again while pending, approved and rejected;
- the status a device polls, with ids given once, twice, as a list, as an
  object, in other letter case, cut in the middle of an escape;
- the list with every status and search filter, ids in every spelling;
- the pending count with the API server's count of every JSON type;
- the enrollment UI flags with every setting value and every mode answer;
- approvals with folders, device groups (folder groups, tag groups, the
  groups an operator may see, more than 100 guids), display names and tags
  of every type, Host headers with an IPv6 address and without a name;
- rejections with reasons of every type and length, deletions;
- the enrollment queue, its history and the approve, reject and
  clear-rejection proxies with the API server answering in every way;
- help requests: the list with entries of every shape, names from the
  console's audit log and its memory of accepts at both mounts, accept and
  resolve with every kind of answer;
- `listRegistrationNotifications` on the same database at three points.

The made-up API server answered by method and path from a table, saved in
the recording as `api`; a step can override answers (`api` of the step).
Role permission lookups were answered from the built-in role map and are
left out of the comparison. Rows the run created got fixed times before
lists were read, so the order by time is one order. The panel events came
from a stand-in for `services/goConsoleBridge.js`.
