# The Node console as the reference for `console/routes/filetransfer`

This folder is a test fixture. `run.js` wrote `../vectors.json`, which
`routes_test.go` replays: requests to the file transfer routes people use,
at both mounts, with the status, the headers and the body the Node console
answered. Transfers are completed the way the Node script completed them:
it gave the chunks to `services/fileTransferService.js` directly, as a
device's route would, and noted each as a step of the file.

The script needs `web-nodejs/routes/fileTransfer.routes.js` as it was
before the routes for people moved to Go (commit b65d26ca). The README of
`console/goapipath/testdata/nodeoracle` says how to run a group's script
with old files (`overlay.sh`). It writes the files of its transfers to
`web-nodejs/data/transfers` and removes them again.
