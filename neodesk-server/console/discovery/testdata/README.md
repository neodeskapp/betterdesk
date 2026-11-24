# Recordings of the old console for console/discovery

The two files here are fixed records of what the Node console's LAN
discovery and mDNS announcement sent and answered, made while it still
existed. They can't be recorded again. The Go tests replay every record
and need no Node. Don't edit them: when a fix changes an answer on
purpose, the test lists that record as an intended difference with the
row id.

| File | What it holds | Replayed by |
|---|---|---|
| `lan.json.gz` | `services/lanDiscovery.js`: the announcement for 153 combinations of settings, host names, interface lists and public key files (with broken UTF-8, white space, a folder in its place); the order of interface names as a JavaScript object lists them; whether each of 6,098 datagrams gets an answer (valid probes, broken JSON, nesting up to 32,000 levels, bytes that are no UTF-8, thousands of random changes); one real exchange over UDP; the log line and the bound port for `DISCOVERY_PORT` values that are no port | `lan_test.go` |
| `mdns.json.gz` | `services/panelDiscovery.js` with the optional package `bonjour-service` 1.4.4 (over `multicast-dns` 7.2.5 and `dns-packet` 5.6.1): with the socket and the timers replaced, every datagram sent and when, for 16 settings (off, broken address, port 0, schemes, long and odd host names); the goodbye after a name conflict while probing; and for 10,646 arriving datagrams how `dns-packet` decodes them and what the service answers | `mdns_test.go` |

The generators were seeded. `os.hostname()` and `os.networkInterfaces()`
were replaced; the Go tests hand the same values in. The recordings were
made from a copy of the old console in which the old project's names were
replaced by NeoDesk's, so the probes, announcements and mDNS names are
NeoDesk's.

## What the Go tests leave out or change

- Node's log line for a name conflict was `console.log(new Error(...))`,
  which printed the stack trace; Go logs its first line.
- A bind error other than "port in use" is logged with Go's error text,
  not Node's (`bind EACCES 0.0.0.0:1`).
- How Go reads the interfaces of the machine (`SystemInterfaces`: up and
  running, the first six bytes of the hardware address) follows libuv and
  is not compared with Node.
