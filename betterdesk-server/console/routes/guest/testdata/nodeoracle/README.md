# The Node console as the reference for `console/routes/guest`

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
`routes_test.go` replays: requests to the guest link routes, the answers
the Node console gave, the guest cookie it set, the calls it made to the
API server, what it asked the device scope for, and which links it told
the socket relay were gone.

The script runs the real `routes/guest.routes.js`, `middleware/guestAccess.js`,
`middleware/auth.js`, the cookie parser and the API client. The device
scope (`services/deviceGroupService.js`), the database module and the
socket relay (`services/wsRelay.js`) are fakes that each vector sets or
reads; the head of the script says how.

It needs `web-nodejs/routes/guest.routes.js` as it was before the routes
moved to Go (commit b65d26ca). The README of
`console/goapipath/testdata/nodeoracle` says how to run a group's script
with old files (`overlay.sh`); `harness.js` there is what it runs on.
