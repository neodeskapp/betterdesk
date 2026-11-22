# Recordings of the old console for console/listen

The files below are fixed records of what the Node console's listeners
did, made while it still existed. They can't be recorded again. The Go
tests replay them and need no Node. Don't edit them: when a fix changes an
answer on purpose, the test lists that exchange as an intended difference
with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `node_listeners.json` | every byte sent to and received from the listeners of the real `server.js`, plain and TLS | the `...AgainstNode` tests of package console (`listeners_replay_test.go`, `listeners_tls_test.go`) |
| `node_redirect.json` | the redirect of the plain port: `lib/redirectHost.js` on generated input, and the exchanges with `server.js` in HTTPS mode | `TestRedirectHostMatchesNode` (`redirecthost_test.go`) for the function vectors, `TestRedirectAgainstNode` (package console) for the exchanges |

## What is recorded

`node_listeners.json` came from the Node console's real `server.js`,
started again and again with different settings and spoken to over real
sockets: the client API port (`startRustDeskApiServer`) in proxy and local
mode and with TLS, the console on HTTPS, the HTTPS fallback without
certificates, start failures, ports in use, and slow clients. A scripted
API server stood in for the Go API and echoed what reached it; the tests
of package console have it again in Go (`echoAPI`). The certificates were
made with `openssl` for the run and deleted after it. The tests start the
Go console with the same settings and send the same bytes.

`node_redirect.json` holds the redirect of the plain port as upstream
3.5.264 had it (audit H5: the host comes from `PANEL_PUBLIC_HOST` or the
bound address, never from the request). It has `lib/redirectHost.js` on
generated input (4,148 configured values, 1,300 pairs of configured value
and listen address, 3,159 addresses built from host, port and request
target), and 242 exchanges with `server.js` started in HTTPS mode eleven
times with different public hosts, from the environment and from the
public endpoints file. The redirect exchanges of the first recording,
which took the host from the request, were removed from
`node_listeners.json`.
