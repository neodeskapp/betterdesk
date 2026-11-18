# Node oracle for console/discovery

Two scripts record what the Node console's LAN discovery and mDNS
announcement send and answer. The Go tests in `console/discovery` replay
every record (`lan_test.go`, `mdns_test.go`).

This folder is a test fixture for the time both servers exist. It needs
`web-nodejs` and goes away with it. The recordings in the parent folder
(`lan.json.gz`, `mdns.json.gz`) stay.

| Script | What it records |
|---|---|
| `lan.js` | `services/lanDiscovery.js`: the announcement for 153 combinations of settings, host names, interface lists and public key files (with broken UTF-8, white space, a folder in its place); the order of interface names as a JavaScript object lists them; whether each of 6,098 datagrams gets an answer (valid probes, broken JSON, nesting up to 32,000 levels, bytes that are no UTF-8, thousands of random changes); one real exchange over UDP; the log line and the bound port for `DISCOVERY_PORT` values that are no port |
| `mdns.js` | `services/panelDiscovery.js` with the optional package `bonjour-service` 1.4.4 (over `multicast-dns` 7.2.5 and `dns-packet` 5.6.1): with the socket and the timers replaced, every datagram sent and when, for 16 settings (off, broken address, port 0, schemes, long and odd host names); the goodbye after a name conflict while probing; and for 10,646 arriving datagrams how `dns-packet` decodes them and what the service answers |

## Running

In the dev image, from the worktree:

    tools/dev.sh "cd betterdesk-server/console/discovery/testdata/nodeoracle && node lan.js"

`bonjour-service` is not in the console's package.json (an operator had
to install it by hand), so `mdns.js` loads it from a folder of its own.
Fetch it into an empty folder outside the worktree and mount it there:

    docker run --rm -v <empty folder>:/npmb bdgo-dev bash -c "npm install --prefix /npmb --ignore-scripts --no-audit --no-fund bonjour-service@1.4.4"
    DEV_DOCKER_ARGS="-v <that folder>:/npmb:ro" tools/dev.sh "cd betterdesk-server/console/discovery/testdata/nodeoracle && BONJOUR_MODULES=/npmb/node_modules node mdns.js"

The generators are seeded, so a second run writes the same files.

## What the Go tests leave out or change

- Node's log line for a name conflict is `console.log(new Error(...))`,
  which prints the stack trace; Go logs its first line.
- A bind error other than "port in use" is logged with Go's error text,
  not Node's (`bind EACCES 0.0.0.0:1`).
- The scripts replace `os.hostname()` and `os.networkInterfaces()`; the Go
  tests hand the same values in. How Go reads the interfaces of the
  machine (`SystemInterfaces`: up and running, the first six bytes of the
  hardware address) follows libuv and is not compared with Node.
