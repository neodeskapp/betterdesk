# Node oracle for console/meshproxy

`web-nodejs` is gone from this tree. These scripts stay as the record of
how the recordings in this package were made; the Go tests replay the
recordings and need no Node. To run a script again, use a tree that still
has `web-nodejs`: the spec worktree
`C:\Users\info\worktrees\betterdesk-go-spec264`. Mount its `web-nodejs`,
with its modules, over `/repo/web-nodejs` of this tree:

    DEV_DOCKER_ARGS="-v C:/Users/info/worktrees/betterdesk-go-spec264/web-nodejs:/repo/web-nodejs -v bdgo-node-nm:/repo/web-nodejs/node_modules" \
      tools/dev.sh "cd betterdesk-server/<this folder> && node <script>"

Its Node code is upstream's release there, so a script that loads a file
the port deleted or changed on the way can fail or record other answers.

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

This folder is a test fixture from the time both servers existed. The
service is gone from `web-nodejs` since it moved; run the script on a
tree before that.
