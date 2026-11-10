# The Node console as the reference for `console/routes/cdap`

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
