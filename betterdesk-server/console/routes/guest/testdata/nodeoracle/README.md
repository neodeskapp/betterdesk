# The Node console as the reference for `console/routes/guest`

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
