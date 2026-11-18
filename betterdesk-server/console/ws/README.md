# Websockets of the console

`console/ws` is the registry of the Go console's sockets, the checks a
socket runs before its handshake, and the accepted socket (`doc.go` has an
example). The front door (`console/server.go`) sends an upgrade here only
when a Go socket is registered for its path; every other upgrade still goes
to the Node console while it runs behind this one. Sockets that live here:

| Path | Package | Old service |
|---|---|---|
| `/ws/bd-relay`, `/ws/bd-signal` | `console/bdrelay` | `services/bdRelay.js` |
| `/ws/chat/<id>`, `/ws/chat-operator/<id>` | `console/routes/chat` (`relay.go`) | `services/chatRelay.js` |
| `/api/cdap/devices/<id>/terminal`, `desktop`, `video`, `files`, `audio` | `console/cdapproxy` | `services/cdapTerminalProxy.js`, `services/cdapMediaProxy.js` |
| `/agent.ashx`, `/meshrelay.ashx`, `/control.ashx` | `console/meshproxy` | `services/meshAshxProxy.js` |
| `/ws/device-status`, `/ws/panel-events` | `console/statuspush` | `services/deviceStatusPush.js` |
| `/ws/rendezvous`, `/ws/relay` | `console/wsrelay` | `services/wsRelay.js` |
| `/ws/remote-agent/<id>`, `/ws/remote-viewer/<id>` | `console/remoterelay` | `services/remoteRelay.js` |
| `/ws/remote-target/<id>` | `console/remotetarget` | `services/remoteTargetGateway.js` |

The CDAP and MeshCentral sockets pass what they get on to a socket of
the API server. The old console opened that socket over the network
with the console's API key; here `DialHandler` opens it to the API
server's handler in this process (`goapi.Client.Handler`, the console's
identity), over an in-memory connection that the handler reads as a
normal HTTP connection, so it answers as on its listener. `Pass`
carries the messages both ways with the old proxies' rules: what waits
for the handshake, the kind of each message, the closes without a code,
the error message when the API server's socket fails.

## How a socket is tested

A socket has three layers of tests, each against what Node did.

**1. Recordings of the old service, replayed in the package.** A Node script
under the package's `testdata/nodeoracle/` loads the real service from
`web-nodejs` on a real HTTP server, with stubs only where the service
reached out (the session middleware, the permission lookup, the token
check, the API server's event socket), and records:

- the raw answer to upgrade requests: every refusal byte for byte (the bare
  status line the services wrote, the websocket library's HTML answer to a
  malformed handshake), the handshake by its headers;
- conversations: sockets opened, messages sent, the clock moved on,
  functions called, and after each step what every socket got (text and
  binary messages, the close code and reason, the end of the connection)
  and what each call returned. Timers run on a made-up clock and random
  ids and tokens come from a made-up source, so the Go test can give the
  service the same clock and source and compare every byte.

The Go test (`TestNode...`) plays the same requests and steps against the
Go service on an `httptest` server and must see the same. `wstest` has the
raw request helper, a client that keeps every frame, the session stub the
oracles use (`OracleSession`) and the goroutine count for the leak checks.
Each script's README says how to run it. A recording that holds
megabytes of one letter (the size limits) is made small with
`node console/ws/wstest/squeeze.js <file>`, which writes each long run as
a marker; the tests read recordings through `wstest.Expand`.

| Package | Script | Recording | What |
|---|---|---|---|
| `ws` | `targets.js` | `node_targets.json` | 6,000 request targets as `new URL()` read them (path, query values, refusal), 1,320 origin checks under five settings |
| `statuspush` | `push.js` | `node_push.json` | 1,536 event frames and what each socket got, 88 upgrade requests |
| `bdrelay` | `relay.js` | `node_relay.json` | 12 scenarios, 198 steps: every check and close code, messages, replacement, the per-address limit, the size limits, the request and answer of `RequestFromDevice` with its timeouts, pairing, expiry, idle timeout, the sweep, reject |
| `routes/devices` | `agent.js` | `node_agent_*.json` | the 17 routes that reach into an agent, with a made-up agent that answers each request, on SQLite and PostgreSQL |
| `routes/chat` | `relay.js` | `node_relay.json` | 19 scenarios, 235 steps: the checks of both sockets in their order (origin, token in the query or a bearer header, session, permission, the handshake), every message type of agents and operators with odd values, the routing between rooms and the inbox, history, contacts and groups with the API server's answers and failures, the room's memory of 500, replacement, the limits; with every call to the API server (path and body), token check and permission lookup; ten messages that ended the Node process |
| `cdapproxy` | `proxy.js` | `node_proxy.json` | 15 scenarios, 180 steps: who may open which of the five sockets, the order of the checks, what reaches the API server's socket (path, subprotocol, user headers) and comes back, messages that wait for the handshake, closes and failures on either side, user names that ended the Node process |
| `meshproxy` | `proxy.js` | `node_proxy.json` | 9 scenarios, 102 steps: the checks, path and query and X-Forwarded-For as the gateway gets them, messages both ways, those lost before the gateway answers, closes and failures |
| `wsrelay` | `codec.js`, `relay.js` | `node_codec.json`, `node_relay.json` | 7,515 rendezvous payloads and 202 frame streams through the guest check and the codec; 14 scenarios, 213 steps: every check, the dialing of the signal server and the relay, bytes both ways, message transport, the per-address limit, idle timeout, guest links (validate, consume, recheck, expiry, revocation) |
| `remoterelay` | `remote.js`, `peer.js` | `node_remote.json`, `node_peer_*.json` | 6 scenarios, 160 steps of agents and viewers, 13 cases that ended Node; the device lookup on SQLite and PostgreSQL |
| `remotetarget` | `gateway.js` | `node_gateway.json` | 5 scenarios, 52 steps: every check, messages both ways, the tunnel's answers and sizes, names that cannot be a header |

`wstest.Player` plays these recordings with raw sockets, `wstest.PlayProxy`
the two proxy recordings against a made-up API server (`wstest.FakeAPI`).

**2. Unit tests of the limits.** Each limit has a test that fails when the
limit is removed: the size limits (1009), text that is not UTF-8 (1007),
the client that stops reading (dropped after 1,024 queued messages), the
per-address count, the idle and waiting times, the request timeout. Every
test that opens sockets checks that no goroutine is left
(`wstest.Goroutines`). The packages run with `-race`.

**3. Contract cases against live instances.** A case of the contract suite
can be a step on a socket (`contract.Socket`, see the contract README,
"Websockets"): it opens the socket with the role's cookies (or none, or a
foreign Origin), sends messages, waits for messages or the close, and its
answer is the handshake's refusal or a JSON description of what came back.
Other cases between the steps cause what a socket should push (a new device
id, a LAN registration). The cases run for every role of
`contract/roles.go` and signed out, were recorded from Node behind the
front door, and are checked through the front door and with
`STACK_MODE=go`. The groups are `ws-device-status`, `ws-panel-events`,
`ws-bd-signal`, `ws-bd-relay`, `ws-chat`, `ws-cdap`, `ws-mesh`, `ws-rendezvous`,
`ws-relay`, `ws-remote` and `ws-remote-target`; their names start with `ws-` so they run
after the route groups, whose answers they would otherwise change (audit
entries, a device's id history). The answers of an agent that is connected
are pinned by the package recordings: a test instance has no agent, so the
device routes' cases see the answers for an agent that is offline.

## What differs from Node, on purpose

- The library's ping has a payload ("1", "2", ...); Node's ping had none.
  Browsers answer either; nothing reads it.
- A protocol error the library finds itself (a bad frame, a close code that
  is not allowed) closes with its own reason text; the websocket library
  closed with the same code and no reason. The size limit and text that is
  not UTF-8 close exactly as Node did.
- Node queued messages for a client that does not read without end; here a
  client with 1,024 messages waiting is dropped.
- Node checked the size of a message from its frame header; here the
  socket reads one byte over the limit before it closes. The client sees
  the same close.
- Where Node's process ended (BUGS WS2, WS9, SK2, SK3) the Go console
  drops that one connection without an answer (DECISIONS A19).
- The chat relay calls the API server for one socket one call after the
  other, in the order its messages came; Node let the calls run side by
  side and sent each answer when it came. With an API server that answers
  in the order it is asked, as the one in this process does, the messages
  go out in the same order.
- The proxies to the API server let at most 1,024 messages of a browser
  wait for the API server's socket; a browser that sends more is not read
  until there is room. Node queued without a limit.
- Absolute-form request targets with unusual hosts are approximated: the
  host checks of the URL Standard are not all ported. Node's own parser
  refused most of those targets before the upgrade handler saw them.
