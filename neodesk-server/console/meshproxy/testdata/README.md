# Recordings of the old console for console/meshproxy

The file below is a fixed record of what the Node console's mesh proxy
(`services/meshAshxProxy.js`) did, made while the console still existed.
It can't be recorded again. The Go test replays it and needs no Node.
Don't edit it: when a fix changes a stream on purpose, the test lists that
step as an intended difference with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `node_proxy.json` | 9 scenarios with 102 steps on the sockets `/agent.ashx`, `/meshrelay.ashx` and `/control.ashx` | `TestNodeScenarios` (`node_test.go`) |

## What is recorded

The old service ran on a real HTTP server, in front of a made-up API
server whose sockets did what each scenario's plan said: echo, greet, hold
the handshake, refuse it. The session was a stub (the JSON in
`X-Oracle-Session`). Steps opened sockets with raw upgrade requests, sent
on them, and acted on the API server's side.

After each step the recording holds what every client socket got and what
the API server's side saw: the handshake's path and query, the
subprotocol, the `X-Forwarded-For` header, each message with its kind, the
close. The test plays the scenarios on the Go proxy against the same
made-up API server (`wstest.FakeAPI`) and compares every stream.
