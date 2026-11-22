# Recordings of the old console for console/routes/commercialization

`vectors.json` is a fixed record of what the Node console did, made while
it still existed. It can't be recorded again. The Go tests replay it with
`goapipath/proxytest` and need no Node. Don't edit it: when a fix changes
an answer on purpose, the test rewrites those vectors into the answer the
console gives now, under the row id (`twoDots` and `fixed` in
`routes_test.go` for FL4).

| File | What it holds | Replayed by |
|---|---|---|
| `vectors.json` | the routes of `routes/commercialization.routes.js` that pass a call on to the API server | `TestVectorsAnswered`, `TestVectorsUnanswered`, `TestVectorsQuick` (`routes_test.go`) |

## What is recorded

The routes ran with the real guards and the real API client in front of a
scripted API server, as they were before they moved to Go (commit
dd2da222), on the harness described in
`console/goapipath/testdata/README.md`. The vectors hold every role, every
kind of answer of the API server (amounts, times across a day, month and
year border and a change of summer time, CSV with formula cells, PDF
bytes), path parameters, queries and bodies, and the calls each request
made to the API server.
