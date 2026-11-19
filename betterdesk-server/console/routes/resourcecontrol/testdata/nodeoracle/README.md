# The Node console as the reference for `console/routes/resourcecontrol`

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

This folder is a test fixture. `run.js` wrote `../vectors.json`, which
`routes_test.go` replays: requests to the resource control routes of the admin
panel, the answers the Node console gave, and the calls it made to the API
server meanwhile.

The script needs `web-nodejs/routes/resource-control.routes.js` as it was before those
routes moved to Go (commit 057d54d3). The README of
`console/goapipath/testdata/nodeoracle` says how to run it with that file;
`harness.js` there is what it runs on.

`device.js` wrote `../device_vectors.json`, which `device_test.go` replays:
the route devices call (`GET /api/bd/resource-policy`) at both mounts, with the real device token checks of
`middleware/deviceAuth.js` on a scratch SQLite database with four token
rows (the Go test stores the same ones). It needs `web-nodejs/routes/resource-control.routes.js`
as it was before these routes moved to Go too, and runs the same way, with
`node device.js`.
