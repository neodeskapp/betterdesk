# Recordings of the old console for console/routes/meshcentral

The file below is a fixed record of what the Node console did with query
strings on its MeshCentral routes, made while it still existed. It can't
be recorded again. The Go tests replay it and need no Node. Don't edit it:
when a fix changes a result on purpose, the test lists that query as an
intended difference with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `query.json.gz` | about 6,000 query strings and what the route expressions made of them, one JSON object per line | `TestQueryVectors` (`query_test.go`) |

## What is recorded

The recorder ran the expressions of `web-nodejs/routes/meshcentral.routes.js`
that read `req.query`, with the query parser Express used (`qs` with the
console's limits). Each line holds:

- `q`: the raw query string
- `search`: `new URLSearchParams(req.query).toString()`, what went to the
  API server
- `share`: `String(req.query.mesh_share || '').trim()`
- `guest`: `String(req.query.guest || req.query.t || '').trim()`
- `viewOnly`: `!!req.query.view_only`
- `watch`: the search text after a guest link that only allows watching
  set `view_only=1`

A value is `{"throws": "<message>"}` where JavaScript raised an error.
