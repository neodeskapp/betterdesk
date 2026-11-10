# The Node console as the reference for `console/routes/resourcecontrol`

This folder is a test fixture. `run.js` wrote `../vectors.json`, which
`routes_test.go` replays: requests to the resource control routes of the admin
panel, the answers the Node console gave, and the calls it made to the API
server meanwhile.

The script needs `web-nodejs/routes/resource-control.routes.js` as it was before those
routes moved to Go (commit 057d54d3). The README of
`console/goapipath/testdata/nodeoracle` says how to run it with that file;
`harness.js` there is what it runs on.
