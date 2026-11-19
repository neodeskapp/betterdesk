# Comparison with the Node console's network routes

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

This folder is a test fixture. It stays now that `web-nodejs` is gone; the
route file it runs (`web-nodejs/routes/network.routes.js`) is already gone
from the tree, so the script runs on a checkout from before the move (pass
its `web-nodejs` folder as the second argument).

`routes.js` starts the Node console's network routes on a new database, with
the body parsers of `server.js`, the rule that a write to `/api/` has to be
JSON, and a session per request, and sends them the steps listed in the
script: about 720 requests by eight accounts and by nobody. It writes what
each step answered to `../routes.sqlite.json` or `../routes.postgres.json`,
one JSON object per line. The first line is the table of names the lookups
know (`console/netmonitor/testdata/nodeoracle/lib.js`).

- `name`, `as` (the account, `null` for signed out), `method`, `path`
- `body` and `bodyType`: the request body as it was sent (JSON unless
  `bodyType` says otherwise)
- `status`, `type` (Content-Type), `text` (the body of the answer)

Two kinds of line are no request. `WAIT` lets the monitor's poll finish.
`SPREAD` gives the checks stored so far times a minute apart in the order
they were made: a check's time has seconds only on SQLite, and checks of one
second come in no order of their own (BUGS.md NM8).

Nothing leaves the machine: no port of 0.0.0.0 that the steps use is open,
the image has no `ping` command, and names resolve from the table.

`TestNodeSteps` in `routes_test.go` replays the steps on SQLite and on
PostgreSQL, each against its own file, since the two databases answer
differently where values of the wrong type reach them (BUGS.md NM1). Times
differ from run to run and are compared by their form.

Run it in the dev container, from the repository root:

    node betterdesk-server/console/routes/network/testdata/nodeoracle/routes.js sqlite
    node betterdesk-server/console/routes/network/testdata/nodeoracle/routes.js postgres

The second needs `BETTERDESK_TEST_POSTGRES_DSN` (see `tools/gotest.sh`); it
works in a schema of its own, `oracle_network`, and drops it at the end.
