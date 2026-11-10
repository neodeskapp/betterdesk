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

`device.js` wrote `../device_vectors.json`, which `device_test.go` replays
through the whole console on SQLite and PostgreSQL: the two routes a
device calls with its token (`POST /file-transfer`, `GET
/file-transfer/:id`) at both mounts, behind the real
`middleware/deviceAuth.js` with six token rows on a scratch SQLite database
(the Go test stores the same ones), and the routes for people of the same
file on the same `services/fileTransferService.js`, so a transfer goes from
start to download in one process. It needs the route file of commit
b65d26ca too (its device routes are the lines the later file kept) and
services/fileTransferService.js, which was deleted with the device routes:
run it in a tree from before that deletion, with `overlay.sh` as above,
and `node device.js`.
