# Node oracle for the console's listeners

`record.js` starts the Node console's real `server.js` again and again with
different settings and talks to its listeners over real sockets, plain and
TLS: the client API port (`startRustDeskApiServer`) in proxy and local mode
and with TLS, the console on HTTPS, the HTTPS fallback without
certificates, start failures and ports in use, and slow clients. A
scripted API server (`lib.js`, `startUpstream`) stands in for the Go API
and echoes what reached it. Certificates are made with `openssl` for the
run and deleted after it. Everything sent and received is written to
`../node_listeners.json`; the tests of package console
(`listeners_replay_test.go`, `listeners_tls_test.go`) start the Go console
with the same settings and send the same bytes.

`redirect.js` records the redirect of the plain port as upstream 3.5.264
has it (audit H5: the host comes from `PANEL_PUBLIC_HOST` or the bound
address, never from the request). It runs `lib/redirectHost.js` on
generated input (4,148 configured values, 1,300 pairs of configured value
and listen address, 3,159 addresses built from host, port and request
target) and starts `server.js` in HTTPS mode eleven times with different
public hosts, from the environment and from the public endpoints file,
sending the requests the old recording sent and a few more (242 exchanges).
It writes `../node_redirect.json`; `TestRedirectHostMatchesNode` (package
listen) replays the function vectors, `TestRedirectAgainstNode` (package
console) the exchanges. The redirect exchanges of the first recording,
which took the host from the request, are removed from
`node_listeners.json`.

This folder is a fixture for the time both servers exist. The client API
listener is gone from `web-nodejs/server.js` since it moved to Go, so
`record.js` runs only on a tree from before that (2925a114), and
`redirect.js` on the spec tree of the upstream merge. The recordings stay
and keep pinning the Go listeners to what Node answered.

## Running

From a worktree that still has `startRustDeskApiServer`, in the dev image
(about ten minutes; the slow clients wait up to 50 seconds):

    tools/dev.sh "cd betterdesk-server/console/listen/testdata && node nodeoracle/record.js"

The redirect, with the spec tree mounted at `/spec/web-nodejs` (two
minutes):

    DEV_DOCKER_ARGS="-v <spec tree>/web-nodejs:/spec/web-nodejs:ro -v bdgo-node-nm:/spec/web-nodejs/node_modules" \
      tools/dev.sh "cd betterdesk-server/console/listen/testdata && REPO=/spec node nodeoracle/redirect.js"
