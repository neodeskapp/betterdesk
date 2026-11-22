# Recordings of the old console for console/routes/guest

`vectors.json` is a fixed record of what the Node console did, made while
it still existed. It can't be recorded again. The Go test replays it and
needs no Node. Don't edit it: when a fix changes an answer on purpose, its
vector goes into `fixedVectors` in `routes_test.go` with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `vectors.json` | requests to the guest link routes of `routes/guest.routes.js` | `TestVectors` (`routes_test.go`) |

## What is recorded

Each vector holds the answer the Node console gave, the guest cookie it
set, the calls it made to the API server, what it asked the device scope
for, and which links it told the socket relay were gone.

The recorder ran the real `routes/guest.routes.js` as it was before the
routes moved to Go (commit b65d26ca), `middleware/guestAccess.js`,
`middleware/auth.js`, the cookie parser and the API client, on the harness
described in `console/goapipath/testdata/README.md`. The device scope
(`services/deviceGroupService.js`), the database module and the socket
relay (`services/wsRelay.js`) were fakes that each vector set or read.
