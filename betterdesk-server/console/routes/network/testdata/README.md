# Recordings of the old console for console/routes/network

The files below are fixed records of what the Node console's network
routes answered, made while it still existed. They can't be recorded
again. The Go tests replay them and need no Node. Don't edit them: when a
fix changes an answer on purpose, its step goes into `stepDifferences`
(`routes_test.go`) with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `routes.sqlite.json`, `routes.postgres.json` | about 720 requests by eight accounts and by nobody, one JSON object per line | `TestNodeSteps` (`routes_test.go`) |

The two databases have a file each, since they answer differently where
values of the wrong type reach them (BUGS.md NM1). Times differ from run
to run and are compared by their form.

## What is recorded

The recorder started the Node console's network routes
(`web-nodejs/routes/network.routes.js`) on a new database, with the body
parsers of `server.js`, the rule that a write to `/api/` has to be JSON,
and a session per request. The first line of each file is the table of
names the lookups knew. Every other line holds:

- `name`, `as` (the account, `null` for signed out), `method`, `path`
- `body` and `bodyType`: the request body as it was sent (JSON unless
  `bodyType` says otherwise)
- `status`, `type` (Content-Type), `text` (the body of the answer)

Two kinds of line are no request. `WAIT` lets the monitor's poll finish.
`SPREAD` gives the checks stored so far times a minute apart in the order
they were made: a check's time has seconds only on SQLite, and checks of one
second come in no order of their own (BUGS.md NM8).

Nothing left the machine: no port of 0.0.0.0 that the steps use was open,
the image had no `ping` command, and names resolved from the table.
