# The Node console as the reference for `console/goapipath`

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
the vector files next to this folder stay as the record of what it did.

The scripts load the Node console's real code from `/repo/web-nodejs`
(`lib/goApiPath.js`, `lib/goApiProxy.js`, `services/betterdeskApi.js`, and
Express with its query parser), call it with many inputs and write down
what it answered. The Go tests replay the files.

| Script | Writes | Replayed by |
|---|---|---|
| `pure.js` | `paths.json` (the path checks), `query.json` (`req.query` for raw query strings), `js.json` (`parseInt`, the text of a number, `slice`, `URLSearchParams`) | `vectors_test.go` |
| `proxy.js` | `proxy.json`: `proxyToGo` and `proxyBinaryToGo` behind three small routes, with the real API client in front of a scripted API server | `proxy_test.go` |

`lib.js` has what the scripts share. `harness.js` runs route files against
the scripted API server and records one vector per request: what the client
got and what the API server was asked. The route groups that only pass
calls on use it for their own vectors
(`console/routes/fleet`, `policies`, `resourcecontrol`, each with a
`testdata/nodeoracle/run.js`), and package `goapipath/proxytest` replays
such vectors against the Go routes.

In a vector file a character of a request path stands for one byte, so a
path can hold bytes that are no UTF-8.

## Running them

In the dev image, from the notes folder:

    tools/dev.sh "cd betterdesk-server/console/goapipath/testdata/nodeoracle && node pure.js && node proxy.js"

The scripts of the route groups need the route files as they were before
the routes moved to Go (commit 057d54d3). `overlay.sh` builds a copy of the
Node console with those files in `/tmp/oracle`. From the notes folder, with
the old files in a folder `orig/routes` of your own:

    git -C <worktree> show 057d54d3:web-nodejs/routes/fleet.routes.js > orig/routes/fleet.routes.js
    (the same for policies.routes.js and resource-control.routes.js)

    DEV_DOCKER_ARGS="-v <orig>:/orig:ro" tools/dev.sh "bash betterdesk-server/console/goapipath/testdata/nodeoracle/overlay.sh && cd betterdesk-server/console/routes/fleet/testdata/nodeoracle && BD_REPO=/tmp/oracle node run.js"
