# Recordings of the old console for console/routes/cdap

`vectors.json` is a fixed record of what the Node console did, made while
it still existed. It can't be recorded again. The Go test replays it and
needs no Node. Don't edit it: when a fix changes an answer on purpose, the
fix goes into `fixes` in `routes_test.go` under the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `vectors.json` | about 1,980 requests to the device gateway routes of `routes/cdap.routes.js` | `TestVectors` (`routes_test.go`) |

## What is recorded

Each vector holds a request, the answer the Node console gave, the calls it
made to the API server meanwhile, and for the gateway switch the restart
transaction file the request left behind. The recorder ran
`routes/cdap.routes.js` and `services/restartCoordinator.js` as they were
before the routes moved to Go (commit b65d26ca), on the harness described
in `console/goapipath/testdata/README.md`.
