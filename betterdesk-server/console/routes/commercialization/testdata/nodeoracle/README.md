# The Node console as the reference for `console/routes/commercialization`

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
`routes_test.go` replays with `goapipath/proxytest`: the routes that pass
a call on to the API server, with the real guards and the real API client
in front of a scripted API server, for every role, every kind of answer of
the API server (amounts, times across a day, month and year border and a
change of summer time, CSV with formula cells, PDF bytes), path
parameters, queries and bodies.

The script needs `web-nodejs/routes/commercialization.routes.js` as it was
before the routes moved to Go (commit dd2da222). `overlay.sh` in
`console/goapipath/testdata/nodeoracle` builds a copy of the console with
it; from the notes folder, with the old file in a folder `orig/routes` of
your own:

    git -C <worktree> show dd2da222:web-nodejs/routes/commercialization.routes.js > orig/routes/commercialization.routes.js
    DEV_DOCKER_ARGS="-v <orig>:/orig:ro" tools/dev.sh "bash betterdesk-server/console/goapipath/testdata/nodeoracle/overlay.sh && cd betterdesk-server/console/routes/commercialization/testdata/nodeoracle && BD_REPO=/tmp/oracle node run.js"
