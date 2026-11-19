# Comparison with the Node console's query handling

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

This folder is a test fixture. It stays now that `web-nodejs` is gone:
once the Node console is deleted there is nothing left to compare with.

`query.js` runs the expressions of `web-nodejs/routes/meshcentral.routes.js`
that read `req.query` on about 6,000 query strings, with the query parser
Express uses (`qs` with the console's limits), and writes the results to
`../query.json.gz`, one JSON object per line:

- `q`: the raw query string
- `search`: `new URLSearchParams(req.query).toString()`, what goes to the
  API server
- `share`: `String(req.query.mesh_share || '').trim()`
- `guest`: `String(req.query.guest || req.query.t || '').trim()`
- `viewOnly`: `!!req.query.view_only`
- `watch`: the search text after a guest link that only allows watching
  set `view_only=1`

A value is `{"throws": "<message>"}` where JavaScript raised an error.

`TestQueryVectors` in `query_test.go` replays every line.

Run it in the dev image, from the repository root:

    node betterdesk-server/console/routes/meshcentral/testdata/nodeoracle/query.js
