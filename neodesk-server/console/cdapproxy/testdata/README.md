# Recordings of the old console for console/cdapproxy

`node_proxy.json` is a fixed record of what the Node console's
`services/cdapTerminalProxy.js` and `services/cdapMediaProxy.js` did, made
while they still existed. It can't be recorded again. The Go test replays
it and needs no Node. Don't edit it: when a fix changes a step on purpose,
`fixedRecording` in `node_test.go` rewrites that step with the row id, as
it does for SK2.

| File | What it holds | Replayed by |
|---|---|---|
| `node_proxy.json` | 15 scenarios with 180 steps against the two proxies on a real HTTP server | `TestNodeScenarios` (`node_test.go`) |

## What is recorded

The proxies ran in front of a made-up API server (a websocket server
reached through `NEODESK_API_URL`) whose sockets did what each
scenario's plan said: echo every message, greet with a first message, hold
the handshake until a step let it go, or refuse it with a status. The
permission lookup (admin and global_admin have `cdap.terminal`,
`cdap.files` and `cdap.command`, operator `cdap.command`, `throw` fails)
and the session (the JSON in `X-Oracle-Session`) were stubs. Steps open
browser sockets with raw upgrade requests, send on them (text, binary,
text that is not UTF-8, a close, a dropped connection), and act on the API
server's side.

After each step the recording holds what every browser socket got, what
the API server's side saw (the handshake's path, subprotocol, `X-Username`
and `X-Role` as Latin-1, each message with its kind, the close) and every
permission lookup. The Go test plays the scenarios on the Go proxy against
the same made-up API server (`wstest.FakeAPI`) and compares every stream.

Two scenarios (`crash: ...`) open a socket as an account whose name the
HTTP client refused in a header; the Node process ended there (BUGS SK2).
The Go test checks that the Go console opens those sockets since the fix.
