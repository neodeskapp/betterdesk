# The Node console as the reference for `console/routes/remote`

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

This folder is a test fixture. It stays now that `web-nodejs` is gone:
once the Node console is deleted there is nothing left to compare with, and
the files next to this folder stay as the record of what it did.

| Script | Writes | Replayed by |
|---|---|---|
| `run.js` | `vectors.json`: 1,096 requests to the remote routes and to `POST /api/bd/remote-agent-token`, what the Node console answered (status, Content-Type, Cache-Control, Location, cookies, rate limit, body) and what it asked the API server, the device table and the remote relay | `TestNodeVectors`, on SQLite and PostgreSQL |
| `crash.js` | `crashes.json`: 16 requests with a query value that has a member `toString`, and whether the Node process was still there to answer | `TestRequestsThatEndedNode` |

`run.js` runs the real `routes/remote.routes.js` (with `lib/devMockRemote`)
and `routes/bd-api.routes.js` behind the real guards, rate limiter, cookie
parser, request language and guest link helpers, with the device tokens on
a scratch SQLite database and the real API client in front of a scripted
API server that answers by method and path. The services the routes ask
besides the API server are scripted per vector and recorded: the device of
the console's table (`db.getDevice`), the server's key, the connection
mode of the settings, the branding and the remote relay. Those services
have their own recordings in their packages (`console/remoterelay`,
`console/serverinfo`, `console/branding`).

Run them in the dev image while `web-nodejs` still has the files:

    tools/dev.sh "cd betterdesk-server/console/routes/remote/testdata/nodeoracle && node run.js && node crash.js"
