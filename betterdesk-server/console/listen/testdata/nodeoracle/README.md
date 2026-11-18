# Node oracle for the console's listeners

`record.js` starts the Node console's real `server.js` again and again with
different settings and talks to its listeners over real sockets, plain and
TLS: the client API port (`startRustDeskApiServer`) in proxy and local mode
and with TLS, the console on HTTPS with its redirect listener, the HTTPS
fallback without certificates, start failures and ports in use, and slow
clients. A scripted API server (`lib.js`, `startUpstream`) stands in for the
Go API and echoes what reached it. Certificates are made with `openssl` for
the run and deleted after it. Everything sent and received is written to
`../node_listeners.json`; the tests of package console
(`listeners_replay_test.go`, `listeners_tls_test.go`) start the Go console
with the same settings and send the same bytes.

This folder is a fixture for the time both servers exist. The client API
listener is gone from `web-nodejs/server.js` since it moved to Go, so the
script runs only on a tree from before that (2925a114). The recording stays
and keeps pinning the Go listeners to what Node answered.

## Running

From a worktree that still has `startRustDeskApiServer`, in the dev image
(about ten minutes; the slow clients wait up to 50 seconds):

    tools/dev.sh "cd betterdesk-server/console/listen/testdata && node nodeoracle/record.js"
