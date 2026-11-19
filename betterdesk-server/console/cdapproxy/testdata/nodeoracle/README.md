# Node oracle for console/cdapproxy

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

`proxy.js` runs the old console's `services/cdapTerminalProxy.js` and
`services/cdapMediaProxy.js` on a real HTTP server, in front of a made-up
API server (a websocket server in the same script, reached through
`BETTERDESK_API_URL`) whose sockets do what each scenario's plan says:
echo every message, greet with a first message, hold the handshake until
a step lets it go, or refuse it with a status. The permission lookup
(admin and global_admin have `cdap.terminal`, `cdap.files` and
`cdap.command`, operator `cdap.command`, `throw` fails) and the session
(the JSON in `X-Oracle-Session`) are stubs. Steps open browser sockets
with raw upgrade requests, send on them (text, binary, text that is not
UTF-8, a close, a dropped connection), and act on the API server's side.

After each step it records what every browser socket got, what the API
server's side saw (the handshake's path, subprotocol, `X-Username` and
`X-Role` as Latin-1, each message with its kind, the close) and every
permission lookup. `../node_proxy.json` holds 15 scenarios with 180
steps. `TestNodeScenarios` plays them on the Go proxy against the same
made-up API server (`wstest.FakeAPI`) and compares every stream.

Two scenarios (`crash: ...`) open a socket as an account whose name the
HTTP client refuses in a header; the process ended there (BUGS SK2). The
Go test checks that the Go console drops that connection without an
answer and keeps every other socket.

    tools/dev.sh "cd betterdesk-server/console/cdapproxy/testdata && node nodeoracle/proxy.js"

This folder is a test fixture from the time both servers existed. The
services are gone from `web-nodejs` since they moved; run the script on a
tree before that.
