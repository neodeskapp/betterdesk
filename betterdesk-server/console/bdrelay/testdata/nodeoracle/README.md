# Node oracle for console/bdrelay

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

`relay.js` runs the old console's `services/bdRelay.js` on a real HTTP
server and plays scenarios against it. `Date.now()` and the timers are a
made-up clock that moves only when a step says so (`advance`), and the
session ids and tokens come from a made-up random source (xorshift32, the
same bytes the Go test reads). The token check is stubbed:
`tok-<device>-good` passes for that device, `tok-throw` makes the check
fail. Sockets are opened with raw upgrade requests and read frame by frame.

A step opens a socket, sends a message (text, binary, bytes that are not
UTF-8, a repeated byte up to megabytes, a close), moves the clock, makes
the next `Date.now()` throw (`throwNow`: the relay handler reads the clock
right after its token check, so the handler's catch answers), or calls a
function the routes used (`createRelaySession`, `getRelaySession`, the
map's `delete`, `notifyTarget`, `getOnlineDeviceIds`, `isDeviceOnline`, the
heartbeat's list of pending sessions, `requestFromDevice` and its result).
After each step it records what every socket got (the answer's head, text
and binary messages, the close frame, the end of the connection) and what
the call returned. `../node_relay.json` holds 15 scenarios with 230 steps.
`TestNodeScenarios` plays them on the Go service with the same clock and
random source (a `throwNow` step makes the Go clock panic once); every
socket must see the same, in order, and every call must return the same
JSON.

The recording is of upstream 3.5.264 (GHSA-98mc-f6p7-gj85): a relay token
of another length closes with 4403 "Invalid token" (scenario `relay token
lengths`), and a throw in the relay handler closes that socket with 1011
"Internal error" (`relay handler throws`). The scenario `relay frames
around the length marks` relays messages of 125, 126, 127, 65535 and 65536
bytes, the lengths where the frame header changes. The other twelve
scenarios are the same as in the recording of the tree before the move.

This folder is a test fixture from the time both servers existed. The service
is gone from `web-nodejs` since it moved, so the script runs on the spec
tree of the upstream merge, mounted at `/spec/web-nodejs` with the
dependencies of the dev image; the recording stays.

    DEV_DOCKER_ARGS="-v <spec tree>/web-nodejs:/spec/web-nodejs:ro -v bdgo-node-nm:/spec/web-nodejs/node_modules" \
      tools/dev.sh "cd betterdesk-server/console/bdrelay/testdata && WEB_NODEJS=/spec/web-nodejs node nodeoracle/relay.js"

After a run, `node ../../ws/wstest/squeeze.js <recording>` (from `testdata`) writes the long runs of the size limits as markers; the test reads the file through `wstest.Expand`.
