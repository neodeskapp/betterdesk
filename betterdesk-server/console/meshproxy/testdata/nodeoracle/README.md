# Node oracle for console/meshproxy

`proxy.js` runs the old console's `services/meshAshxProxy.js` on a real
HTTP server, in front of a made-up API server (a websocket server in the
same script, reached through `BETTERDESK_API_URL`) whose sockets do what
each scenario's plan says: echo, greet, hold the handshake, refuse it.
The session is a stub (the JSON in `X-Oracle-Session`). Steps open
sockets at `/agent.ashx`, `/meshrelay.ashx` and `/control.ashx` with raw
upgrade requests, send on them, and act on the API server's side.

After each step it records what every client socket got and what the API
server's side saw: the handshake's path and query, the subprotocol, the
`X-Forwarded-For` header, each message with its kind, the close.
`../node_proxy.json` holds 9 scenarios with 102 steps.
`TestNodeScenarios` plays them on the Go proxy against the same made-up
API server (`wstest.FakeAPI`) and compares every stream.

    tools/dev.sh "cd betterdesk-server/console/meshproxy/testdata && node nodeoracle/proxy.js"

This folder is a test fixture for the time both servers exist. The
service is gone from `web-nodejs` since it moved; run the script on a
tree before that.
