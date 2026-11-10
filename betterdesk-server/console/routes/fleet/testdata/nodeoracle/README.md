# The Node console as the reference for `console/routes/fleet`

This folder is a test fixture. `run.js` wrote `../vectors.json`, which
`routes_test.go` replays: requests to the fleet routes of the admin
panel, the answers the Node console gave, and the calls it made to the API
server meanwhile.

The script needs `web-nodejs/routes/fleet.routes.js` as it was before those
routes moved to Go (commit 057d54d3). The README of
`console/goapipath/testdata/nodeoracle` says how to run it with that file;
`harness.js` there is what it runs on.

`device.js` wrote `../device_vectors.json`, which `device_test.go` replays:
the two routes agents report to (`POST /api/bd/fleet/task-result`, `/api/bd/fleet/software`) at both mounts, with the real device token checks of
`middleware/deviceAuth.js` on a scratch SQLite database with four token
rows (the Go test stores the same ones). It needs `web-nodejs/routes/fleet.routes.js`
as it was before these routes moved to Go too, and runs the same way, with
`node device.js`.
