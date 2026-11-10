# Comparison with the Node console's query handling

This folder is a test fixture. It goes away together with `web-nodejs`:
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
