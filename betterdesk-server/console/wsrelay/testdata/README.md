# Recordings of the old console for console/wsrelay

The files below are fixed records of what the Node console's socket relay
(`services/wsRelay.js`) did, made while it still existed. They can't be
recorded again. The Go tests replay them and need no Node. Don't edit
them: when a fix changes a step on purpose, the step goes into `fixes` in
`node_test.go` with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `node_codec.json` | frame headers, the frame decoder, the guest check's decoding and the schema protobufjs read | `TestNodeFrameHeaders`, `TestNodeFrameDecoder`, `TestNodeRendezvousDecoding`, `TestSchemaIsNodes` (`codec_test.go`) |
| `node_relay.json` | 15 scenarios with 221 steps on a real HTTP server, and the relay address for 12 configurations | `TestNodeScenarios`, `TestNodeRelayHost` (`node_test.go`) |

## What is recorded

`node_codec.json` (1.4 MB) holds:

- the frame header `encodeRelayFrame` wrote for 23 payload lengths around
  every header size and the 64 MiB limit, and its refusal over the limit;
- 202 streams fed to `createRelayFrameDecoder` in chunks (every header
  form, byte by byte, 150 random streams, the two limits), with the frames
  or the error after each chunk;
- 7,515 payloads decoded for the guest check (`decodeRendezvousPayload`,
  `guestTargetMatches`) with protobufjs and the exception text: valid
  messages, both requests in one message in either order, the id twice,
  wrong wire types for known fields, strings, bytes and sub-messages
  longer than their message, unknown fields of every wire type, groups
  nested around the depth limit of 100, ten-byte and truncated varints,
  the 64-bit field of `ControlPermissions` near the end, text that is not
  UTF-8, every truncation and many one-byte changes of three messages,
  5,000 random payloads;
- the schema protobufjs read from `protos/rendezvous.proto`, which
  `TestSchemaIsNodes` compares with the server's generated descriptors.

The guest check now reads frames as the signal server does (BUGS RM20);
`FuzzRendezvousDecode` compares the two.

The scenarios of `node_relay.json` ran on a real HTTP server with fake
signal and relay servers on TCP (the relay one on 127.0.0.2, which the
loopback replacement picked from a stubbed interface list). The recorder
stubbed the session middleware (the session was the JSON in
`X-Oracle-Session`), the permission lookup (admin and operator have
`device.connect`, the role `throw` makes the lookup fail), the API
server's client (guest links by token, every call recorded with its
answer), the guest cookie's name, the configuration and
`os.networkInterfaces`. `Date.now` and the timers were a made-up clock
that moved only when a step said so. Each step records what every browser
socket got (answer head, messages, close, end), what each fake server
accepted, received (per step) and saw closed, the API calls, and what
`invalidateGuestGrant` returned.

`TestNodeScenarios` replays every scenario on the Go service with the
recorded API answers and the same clock, with a proxy on loopback trusted.
The scenario `guest ordering` is recorded but not replayed: Node let a
later frame reach the server while an earlier one waited for the use of
the link to be counted, and the Go bridge keeps the order
(`TestGuestFramesKeepTheirOrder`).
