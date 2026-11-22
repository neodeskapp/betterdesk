# Recordings of the old console for console/netmonitor

The files below are fixed records of what the Node console's guard and
network checks (`lib/ssrfGuard.js`, `services/networkMonitor.js`) did,
made while the console still existed. They can't be recorded again. The
Go tests replay them and need no Node. Don't edit them: when a fix changes
an answer on purpose, it goes into `probeDifferences` in `probes_test.go`
with the row id, or the guard test lists the record as an intended
difference.

| File | What it holds | Replayed by |
|---|---|---|
| `guard.json.gz` | the guard's checks on addresses, names and URLs | the `TestNode...` tests of `guard_test.go` |
| `probes.json` | the HTTP, TCP, ping and name checks against the recording's own servers, and the monitor's scheduling | `TestNodeProbes`, `TestNodeIsDue`, `TestNodeCheckTarget` (`probes_test.go`) |

## What is recorded

The recordings ran in a container without a network, so no check could
leave it and an address outside was unreachable at once. Name lookups
answered from a table of names that the files carry (`dns`): a name not in
the table was not found, an address was its own answer, and a text of
digits, dots and hex letters went to the system resolver, which reads the
old spellings of an IPv4 address (`127.1`, `0x7f.1`, `2130706433`) without
asking a server.

`guard.json.gz`:

| List | What it holds |
|---|---|
| `blocked_ip` | `isBlockedIp` for about 5,200 addresses and texts, with and without `allowPrivate` |
| `hostname` | `assertSafeHostname` for names, addresses, brackets and white space |
| `resolved` | `assertSafeResolvedHost` for the same hosts with the table's lookups |
| `url` | `assertSafeMonitoringUrl` for about 3,200 URLs: the parts of the parsed URL, or the refusal |
| `resolve` | `resolveHost` of the monitor, which also shows which hosts the tools take as valid |

`probes.json` came from servers on 0.0.0.0 (HTTP, two with TLS, one that
answered with something else than HTTP, one that said nothing, and a
closed port) and a ping command of the recording's own, first in the path:

| List | What it holds |
|---|---|
| `records` | `checkHttp`, `checkTcpPort`, `pingHost` and `resolveHost` in about 170 situations: the result, the requests the servers saw (line and headers), the names that were looked up, the arguments ping got |
| `due` | `isDue` for combinations of poll interval, target interval and time of the last check |
| `checks` | `checkTarget` for targets of every kind, with what it handed to `insertNetworkCheck` and `updateNetworkTarget` |
| `pings` | what the recording's ping command printed for which host, so the Go test can play the same command |

The second TLS server had a certificate authority of its own, trusted
through `NODE_EXTRA_CA_CERTS`. The Go test makes its own authority and
certificates with the same names.

Ports are written as `{http}`, `{tls}`, `{tlsca}`, `{garbage}`, `{silent}`
and `{closed}`, round trip times as `"number"`. Records with `inject` name
the system error a connection to an address outside the container ended
with; the Go test's dialer returns that error.

## Where Go answers something else

`probes_test.go` lists them in `probeDifferences`, each with its reason: a
connection that breaks inside the body (Node never answered, BUGS.md NM4),
the text of Node's TLS library after `write EPROTO`, cases the guard fixes
changed, and two cases where the test's Go server behaves differently from
the recording's Node server.
