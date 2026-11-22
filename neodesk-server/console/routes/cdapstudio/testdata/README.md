# Recordings of the old console for console/routes/cdapstudio

`flows.json` is a fixed record of what the Node console answered, made
while it still existed. It can't be recorded again. The Go test replays it
and needs no Node. Don't edit it: when a fix changes an answer on purpose,
its step goes into `stepDifferences` in `routes_test.go` with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `flows.json` | about 250 requests to `routes/cdap-studio.routes.js` by six accounts, one JSON object per line | `TestNodeSteps` (`routes_test.go`) |
| `../templates.json` | the answer of the templates route, which the Go package serves as it is | embedded by `routes.go` |

## What is recorded

The routes ran on a new SQLite database, with the body parsers of
`server.js` and a session per request. Among the requests are flows whose
nodes and wires are not what the editor writes. Each line holds:

- `name`, `as` (the account, `null` for signed out), `method`, `path`
- `body` and `bodyType`: the request body as it was sent (JSON unless
  `bodyType` says otherwise)
- `save` and `id`: the name a later path uses for the flow this step made
  (`{name}`), and the id Node gave it
- `status`, `type` (Content-Type), `disposition` (Content-Disposition)
- `text`: the body of the answer, or `length` for the one answer that is
  too long to keep

`TestNodeSteps` replays the steps on SQLite and on PostgreSQL. Ids and
times differ from run to run: an id is compared by the step that made it,
a time by its form.
