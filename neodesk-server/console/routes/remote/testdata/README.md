# Recordings of the old console for console/routes/remote

The files below are fixed records of what the Node console did, made while
it still existed. They can't be recorded again. The Go tests replay them
and need no Node. Don't edit them: when a fix changes an answer on purpose,
`fixed_test.go` says which vectors it changes and what they are held to,
with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `vectors.json` | 1,096 requests to the remote routes and to `POST /api/nd/remote-agent-token`, what the Node console answered (status, Content-Type, Cache-Control, Location, cookies, rate limit, body) and what it asked the API server, the device table and the remote relay | `TestNodeVectors` (`routes_test.go`), on SQLite and PostgreSQL |
| `crashes.json` | 16 requests with a query value that has a member `toString`, and whether the Node process was still there to answer | `TestRequestsThatEndedNode` (`units_test.go`) |

## What is recorded

The recorder ran the real `routes/remote.routes.js` (with
`lib/devMockRemote`) and `routes/bd-api.routes.js` behind the real guards,
rate limiter, cookie parser, request language and guest link helpers, with
the device tokens on a scratch SQLite database and the real API client in
front of a scripted API server that answered by method and path. The
services the routes ask besides the API server were scripted per vector
and are recorded with it: the device of the console's table
(`db.getDevice`), the server's key, the connection mode of the settings,
the branding and the remote relay. Those services have their own
recordings in their packages (`console/remoterelay`, `console/serverinfo`,
`console/branding`).
