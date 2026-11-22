# Recordings of the old console for console/routes/crossplatform

`crossplatform.json` is a fixed record of what the Node console answered,
made while it still existed. It can't be recorded again. The Go tests
replay it and need no Node. Don't edit it: when a fix changes an answer on
purpose, its scenario goes into `changedByFixes` in `routes_test.go` with
the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `crossplatform.json` | scenarios of `routes/cross-platform.routes.js`: an answer of the API server and what the route made of it | `TestRoutesMatchNode` (`routes_test.go`) |

## What is recorded

The routes ran with a made-up API client. Each scenario holds the route,
the device, the API server's answer (or its failure), the calls the route
made to the API server, and the status and body it answered.
