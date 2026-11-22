# Recordings of the old console for console/routes/filetransfer

The files below are fixed records of what the Node console answered, made
while it still existed. They can't be recorded again. The Go tests replay
them and need no Node. Don't edit them: when a fix changes an answer on
purpose, the test changes those vectors into the answer the console gives
now and fails if the fix changes none (as `TestVectors` does for FT5, FT6,
FT7, FT10, RV42 and RV48), with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `vectors.json` | requests to the file transfer routes people use, at both mounts | `TestVectors` (`routes_test.go`) |
| `device_vectors.json` | the two routes a device calls with its token, at both mounts, and the routes for people of the same files | `TestDeviceVectors` (`device_test.go`) |

## What is recorded

`vectors.json` holds the status, the headers and the body the Node console
answered. The recorder ran `routes/fileTransfer.routes.js` as it was
before the routes for people moved to Go (commit b65d26ca), on the harness
described in `console/goapipath/testdata/README.md`. It completed transfers
by giving the chunks to `services/fileTransferService.js` directly, as a
device's route would, and noted each as a step of the file; the Go test
completes them the same way.

`device_vectors.json` holds `POST /file-transfer` and
`GET /file-transfer/:id` behind the real `middleware/deviceAuth.js`, with
six token rows on a scratch SQLite database (the Go test stores the same
ones, `deviceTokens`), and the routes for people of the same file on the
same `services/fileTransferService.js`, so a transfer goes from start to
download in one process. `TestDeviceVectors` replays them through the
whole console on SQLite and PostgreSQL.
