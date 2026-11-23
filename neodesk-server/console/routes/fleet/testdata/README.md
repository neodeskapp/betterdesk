# Recordings of the old console for console/routes/fleet

`vectors.json` and `device_vectors.json` are fixed records of what the Node
console did, made while it still existed. They can't be recorded again.
The Go tests replay them with `goapipath/proxytest` and need no Node.
Don't edit them: when a fix changes an answer on purpose, its vectors go
into `fixes` or `deviceFixes` with the row id (`proxytest.Fixes`), and the
replay compares them with the Go console's answers in the `_fixed.json`
file next to the recording (written by a run with
`PROXYTEST_RECORD_FIXED=1`).

| File | What it holds | Replayed by |
|---|---|---|
| `vectors.json` | requests to the fleet routes of the admin panel, the answers, and the calls to the API server | `TestVectors` (`routes_test.go`) |
| `device_vectors.json` | the two routes agents report to, `POST /api/nd/fleet/task-result` and `/api/nd/fleet/software`, at both mounts | `TestDeviceVectors` (`device_test.go`) |
| `vectors_fixed.json`, `device_vectors_fixed.json` | the Go console's answers for the vectors a bug fix changed | the same tests |

## What is recorded

The recorder ran `routes/fleet.routes.js` as it was before those routes
moved to Go (commit 057d54d3), on the harness described in
`console/goapipath/testdata/README.md`. The device routes ran with the real
device token checks of `middleware/deviceAuth.js` on a scratch SQLite
database with four token rows; the Go test stores the same ones
(`withDevices` in `device_test.go`).
