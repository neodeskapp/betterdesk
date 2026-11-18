# The Node console as the reference for `console/routes/commercialization`

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
