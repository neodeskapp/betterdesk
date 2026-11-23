# Recordings of the old console for console/routes/resourcecontrol

The vector files below are fixed records of what the Node console answered,
made while it still existed. They can't be recorded again. The Go tests
replay them with `goapipath/proxytest` and need no Node. Don't edit them:
when a fix changes an answer on purpose, the vector's number goes into
`fixes` (`routes_test.go`) or `deviceFixes` (`device_test.go`) with the row
id, and a run with `PROXYTEST_RECORD_FIXED=1` writes the Go console's
answer into the `_fixed.json` file next to the recording.

| File | What it holds | Replayed by |
|---|---|---|
| `vectors.json` | requests to the resource control routes of the admin panel, the answers the Node console gave, and the calls it made to the API server meanwhile | `TestVectors` (`routes_test.go`) |
| `vectors_fixed.json` | the Go console's answers to the vectors in `fixes` | `TestVectors` |
| `device_vectors.json` | the route devices call (`GET /api/nd/resource-policy`) at both mounts | `TestDeviceVectors` (`device_test.go`) |
| `device_vectors_fixed.json` | the Go console's answers to the vectors in `deviceFixes` | `TestDeviceVectors` |

## What is recorded

The recorder ran `web-nodejs/routes/resource-control.routes.js` as it was
before those routes moved to Go (commit 057d54d3), with the real API client
in front of a scripted API server, and wrote one vector per request: what
the client got and what the API server was asked. In a vector file a
character of a request path stands for one byte, so a path can hold bytes
that are no UTF-8.

The device vectors ran behind the real device token checks of
`middleware/deviceAuth.js` on a scratch SQLite database with four token
rows; the Go test stores the same ones.
