# The Node console as the reference for `console/routes/cdap`

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
`routes_test.go` replays: requests to the device gateway routes, the
answers the Node console gave, the calls it made to the API server
meanwhile, and for the gateway switch the restart transaction file each
request left behind.

The script needs `web-nodejs/routes/cdap.routes.js` and
`web-nodejs/services/restartCoordinator.js` as they were before the routes
moved to Go (commit b65d26ca). The README of
`console/goapipath/testdata/nodeoracle` says how
to run a group's script with old files (`overlay.sh`); `harness.js` there
is what it runs on.
