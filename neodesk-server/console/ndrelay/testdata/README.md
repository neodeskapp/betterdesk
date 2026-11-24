# Recordings of the old console for console/ndrelay

`node_relay.json` is a fixed record of what the Node console's
`services/bdRelay.js` did, made while it still existed. It can't be
recorded again. The Go tests replay it and need no Node. Don't edit it:
when a fix changes a step on purpose, the step goes into `fixes` in
`node_test.go` with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `node_relay.json` | 15 scenarios with 230 steps against the relay on a real HTTP server | `TestNodeScenarios`, `TestFixesNameRecordedSteps` (`node_test.go`) |

## What is recorded

`Date.now()` and the timers were a made-up clock that moved only when a
step said so (`advance`), and the session ids and tokens came from a
made-up random source (xorshift32, the same bytes the Go test reads). The
token check was stubbed: `tok-<device>-good` passes for that device,
`tok-throw` makes the check fail. Sockets were opened with raw upgrade
requests and read frame by frame.

A step opens a socket, sends a message (text, binary, bytes that are not
UTF-8, a repeated byte up to megabytes, a close), moves the clock, makes
the next `Date.now()` throw (`throwNow`: the relay handler read the clock
right after its token check, so the handler's catch answered), or calls a
function the routes used (`createRelaySession`, `getRelaySession`, the
map's `delete`, `notifyTarget`, `getOnlineDeviceIds`, `isDeviceOnline`, the
heartbeat's list of pending sessions, `requestFromDevice` and its result).
After each step the recording holds what every socket got (the answer's
head, text and binary messages, the close frame, the end of the
connection) and what the call returned. The Go test plays the scenarios on
the Go service with the same clock and random source (a `throwNow` step
makes the Go clock panic once); every socket must see the same, in order,
and every call must return the same JSON.

The recording is of upstream 3.5.264 (GHSA-98mc-f6p7-gj85): a relay token
of another length closes with 4403 "Invalid token" (scenario `relay token
lengths`), and a throw in the relay handler closes that socket with 1011
"Internal error" (`relay handler throws`). The scenario `relay frames
around the length marks` relays messages of 125, 126, 127, 65535 and 65536
bytes, the lengths where the frame header changes.

Long runs of one piece of text are stored as run markers; the test expands
them with `wstest.Expand`.
