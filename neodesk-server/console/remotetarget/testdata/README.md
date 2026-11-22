# Recordings of the old console for console/remotetarget

The file below is a fixed record of what the Node console's
`services/remoteTargetGateway.js` did, made while the console still
existed. It can't be recorded again. The Go tests replay it and need no
Node. Don't edit it: when a fix changes a step on purpose, the step goes
into the list `fixes` in `node_test.go` with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `node_gateway.json` | 6 scenarios with 59 steps of the gateway | `TestNodeScenarios`, `TestFixesNameRecordedSteps` (`node_test.go`) |

## What is recorded

The old service ran on a real HTTP server in front of a made-up API
server, whose tunnel echoed messages, closed with 1000 or 1011, dropped
the connection, refused with 503, answered after 300 ms, or sent a
greeting or a message over the 16 MiB limit, by the target's id. Some
accounts had a name Node's HTTP client could not put into a header.

Stubbed were the session middleware (the session is the JSON in
`X-Oracle-Session`), the permission lookup (admin and operator have
`remote_target.connect`, the role `throw` makes it fail), the target
lookup and the scope check (the account `scoped` sees `rt_echo-0001` only,
`scopethrow` fails). Each step holds what every browser socket got, what
every tunnel got (the path and the headers of its handshake, messages, the
close), and the outside calls. A message over 4 KiB is noted by its length
and SHA-256.

The test replays every step against a made-up API handler in Go that
behaves the same, reached in process as the console reaches the API
server. The tunnel's handshake has no X-API-Key (the in-process handler
carries the console's identity) and no X-Username or X-Role (BUGS RM11).
