# Node oracle for console/bdrelay

`relay.js` runs the old console's `services/bdRelay.js` on a real HTTP
server and plays scenarios against it. `Date.now()` and the timers are a
made-up clock that moves only when a step says so (`advance`), and the
session ids and tokens come from a made-up random source (xorshift32, the
same bytes the Go test reads). The token check is stubbed:
`tok-<device>-good` passes for that device, `tok-throw` makes the check
fail. Sockets are opened with raw upgrade requests and read frame by frame.

A step opens a socket, sends a message (text, binary, bytes that are not
UTF-8, a repeated byte up to megabytes, a close), moves the clock, or calls
a function the routes used (`createRelaySession`, `getRelaySession`, the
map's `delete`, `notifyTarget`, `getOnlineDeviceIds`, `isDeviceOnline`, the
heartbeat's list of pending sessions, `requestFromDevice` and its result).
After each step it records what every socket got (the answer's head, text
and binary messages, the close frame, the end of the connection) and what
the call returned. `../node_relay.json` holds 12 scenarios with 198 steps.
`TestNodeScenarios` plays them on the Go service with the same clock and
random source; every socket must see the same, in order, and every call
must return the same JSON.

Not in the recording, because it ends the Node process: a relay token of
another length (BUGS WS2); `TestTokenOfAnotherLength` covers the Go side.

This folder is a test fixture for the time both servers exist. The service
is gone from `web-nodejs` since it moved, so the script runs only on a tree
that still has it (the commit before the move); the recording stays.

    tools/dev.sh "cd betterdesk-server/console/bdrelay/testdata && node nodeoracle/relay.js"

After a run, `node ../../ws/wstest/squeeze.js <recording>` (from `testdata`) writes the long runs of the size limits as markers; the test reads the file through `wstest.Expand`.
