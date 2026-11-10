# Comparison with the Node console's CDAP studio routes

This folder is a test fixture. It goes away together with `web-nodejs`:
once the Node console is deleted there is nothing left to compare with.

`flows.js` starts the Node console's routes
(`web-nodejs/routes/cdap-studio.routes.js`) on a new SQLite database, with
the body parsers of `server.js` and a session per request, and sends them
the steps listed in the script: about 250 requests by six accounts, among
them flows whose nodes and wires are not what the editor writes. It writes
what each step answered to `../flows.json`, one JSON object per line:

- `name`, `as` (the account, `null` for signed out), `method`, `path`
- `body` and `bodyType`: the request body as it was sent (JSON unless
  `bodyType` says otherwise)
- `save` and `id`: the name a later path uses for the flow this step made
  (`{name}`), and the id Node gave it
- `status`, `type` (Content-Type), `disposition` (Content-Disposition)
- `text`: the body of the answer, or `length` for the one answer that is
  too long to keep

It also writes the answer of the templates route to `../../templates.json`,
which the Go package serves as it is.

`TestNodeSteps` in `routes_test.go` replays the steps on SQLite and on
PostgreSQL. Ids and times differ from run to run: an id is compared by the
step that made it, a time by its form.

Run it in the dev image, from the repository root:

    node betterdesk-server/console/routes/cdapstudio/testdata/nodeoracle/flows.js
