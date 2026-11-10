# Node oracle for console/netmonitor

These scripts run the Node console's guard and network checks
(`web-nodejs/lib/ssrfGuard.js`, `web-nodejs/services/networkMonitor.js`) and
record what they answer. The Go tests in `console/netmonitor` replay every
record and must answer the same.

This folder is a test fixture for the time both servers exist. It needs
`web-nodejs` and goes away with it; `services/networkMonitor.js` is already
gone from the tree, so the scripts run on a checkout from before the move
(pass its `web-nodejs` folder as the first argument). The recorded files in
the parent folder stay: they are plain data and keep pinning the Go code to
what Node did.

## Running

In the dev container, without a network, so that no check can leave it and
an address outside is unreachable at once:

    DEV_DOCKER_ARGS="--network none" tools/dev.sh \
      "cd betterdesk-server/console/netmonitor/testdata/nodeoracle && node guard.js && node probes.js"

## Scripts

`lib.js` has the table of names the lookups know and puts lookups that
answer from it in place of Node's (`dns.lookup` and `dns.promises.lookup`).
A name that is not in the table is not found. An address is its own answer,
and a text of digits, dots and hex letters goes to the system resolver, which
reads the old spellings of an IPv4 address (`127.1`, `0x7f.1`, `2130706433`)
without asking a server.

`guard.js` writes `../guard.json.gz`:

| List | What it holds |
|---|---|
| `blocked_ip` | `isBlockedIp` for about 5,200 addresses and texts, with and without `allowPrivate` |
| `hostname` | `assertSafeHostname` for names, addresses, brackets and white space |
| `resolved` | `assertSafeResolvedHost` for the same hosts with the table's lookups |
| `url` | `assertSafeMonitoringUrl` for about 3,200 URLs: the parts of the parsed URL, or the refusal |
| `resolve` | `resolveHost` of the monitor, which also shows which hosts the tools take as valid |

`probes.js` starts its own servers on 0.0.0.0 (HTTP, two with TLS, one that
answers with something else than HTTP, one that says nothing, and a closed
port), puts its own `ping` command first in the path, and writes
`../probes.json`:

| List | What it holds |
|---|---|
| `records` | `checkHttp`, `checkTcpPort`, `pingHost` and `resolveHost` in about 170 situations: the result, the requests the servers saw (line and headers), the names that were looked up, the arguments ping got |
| `due` | `isDue` for combinations of poll interval, target interval and time of the last check |
| `checks` | `checkTarget` for targets of every kind, with what it handed to `insertNetworkCheck` and `updateNetworkTarget` |
| `pings` | what the script's ping command prints for which host, so the Go test can play the same command |

The certificate authority of the second TLS server is made by the script,
which then starts itself again with the authority trusted
(`NODE_EXTRA_CA_CERTS`). The Go test makes its own authority and
certificates with the same names.

Ports are written as `{http}`, `{tls}`, `{tlsca}`, `{garbage}`, `{silent}`
and `{closed}`, round trip times as `"number"`. Records with `inject` name
the system error a connection to an address outside the container ended
with; the Go test's dialer returns that error.

## Where Go answers something else

`probes_test.go` lists them in `probeDifferences`, each with its reason:
a connection that breaks inside the body (Node never answers, BUGS.md NM4),
the text of Node's TLS library after `write EPROTO`, and two cases where the
test's Go server behaves differently from the script's Node server.
