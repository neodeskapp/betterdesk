# Node oracle for console/wsrelay

`web-nodejs` is gone from this tree. These scripts stay as the record of
how the recordings in this package were made; the Go tests replay the
recordings and need no Node. To run a script again, use a tree that still
has `web-nodejs`: the spec worktree
`C:\Users\info\worktrees\betterdesk-go-spec264`. Mount its `web-nodejs`,
with its modules, over `/repo/web-nodejs` of this tree:

    DEV_DOCKER_ARGS="-v C:/Users/info/worktrees/betterdesk-go-spec264/web-nodejs:/repo/web-nodejs -v bdgo-node-nm:/repo/web-nodejs/node_modules" \
      tools/dev.sh "cd betterdesk-server/<this folder> && node <script>"

Its Node code is upstream's release there, so a script that loads a file
the port deleted or changed on the way can fail or record other answers.

These scripts are test fixtures from the time both consoles existed. They load
the old console's `services/wsRelay.js` from `web-nodejs` and record what it
does; the Go tests replay the recordings. They go when `web-nodejs` goes:
once the service is deleted they run only on a tree that still has it (the
commit before the move), and the recordings stay.

`codec.js` writes `../node_codec.json` (1.4 MB):

- the frame header `encodeRelayFrame` writes for 23 payload lengths around
  every header size and the 64 MiB limit, and its refusal over the limit;
- 202 streams fed to `createRelayFrameDecoder` in chunks (every header form,
  byte by byte, 150 random streams, the two limits), with the frames or the
  error after each chunk;
- 7,515 payloads decoded for the guest check (`decodeRendezvousPayload`,
  `guestTargetMatches`) with protobufjs and the exception text: valid
  messages, both requests in one message in either order, the id twice,
  wrong wire types for known fields, strings, bytes and sub-messages longer
  than their message, unknown fields of every wire type, groups nested
  around the depth limit of 100, ten-byte and truncated varints, the 64-bit
  field of `ControlPermissions` near the end, text that is not UTF-8, every
  truncation and many one-byte changes of three messages, 5,000 random
  payloads;
- the schema protobufjs reads from `protos/rendezvous.proto`, which
  `TestSchemaIsNodes` compares with the server's generated descriptors.

`relay.js` writes `../node_relay.json`: 14 scenarios with 209 steps on a
real HTTP server
with fake signal and relay servers on TCP (the relay one on 127.0.0.2, which
the loopback replacement picks from a stubbed interface list). Stubbed are
the session middleware (the session is the JSON in `X-Oracle-Session`), the
permission lookup (admin and operator have `device.connect`, the role
`throw` makes the lookup fail), the API server's client (guest links by
token, every call recorded with its answer), the guest cookie's name, the
configuration and `os.networkInterfaces`. `Date.now` and the timers are a
made-up clock that moves only when a step says so. Each step records what
every browser socket got (answer head, messages, close, end), what each
fake server accepted, received (per step) and saw closed, the API calls,
and what `invalidateGuestGrant` returned. It also records the relay address
the service logged for 12 configurations of the relay host and interfaces.

`TestNodeScenarios` replays every scenario on the Go service with the
recorded API answers and the same clock. The scenario `guest ordering` is
recorded but not replayed: Node let a later frame reach the server while an
earlier one waited for the use of the link to be counted, the Go bridge
keeps the order (`TestGuestFramesKeepTheirOrder`).

`fuzzhelper.js` answers the fuzz tests (`WSRELAY_NODE_HELPER=1`) line by
line with the old code's results: the guest decode, the frame decoder, and
the guest cookie reader (copied from the upgrade handler, which the module
does not export).

    tools/dev.sh "cd betterdesk-server/console/wsrelay/testdata && node nodeoracle/codec.js && node nodeoracle/relay.js"
    DEV_DOCKER_ARGS="-e WSRELAY_NODE_HELPER=1" tools/dev.sh "cd betterdesk-server/console/wsrelay && go test -run '^$' -fuzz '^FuzzRendezvousDecode$' -fuzztime 10m ."
