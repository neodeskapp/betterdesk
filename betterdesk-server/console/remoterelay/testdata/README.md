# Recordings of the old console for console/remoterelay

The files below are fixed records of what the Node console's remote relay
(`services/remoteRelay.js` with `services/dbAdapter.js`) did, made while
it still existed. They can't be recorded again. The Go tests replay them
and need no Node. Don't edit them: when a fix changes a step on purpose,
the step goes into `fixes` in `node_test.go` with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `node_remote.json` | 7 scenarios with 168 steps on a real HTTP server, 13 crash cases, and the deepest status message `JSON.stringify` still wrote | `TestNodeScenarios` (`node_test.go`), `TestWhereNodeEndedTheConnectionIsDropped` and `TestStringifyDepthIsTheRecordedOne` (`relay_test.go`) |
| `node_peer_sqlite.json`, `node_peer_postgres.json` | `db.getPeerById` of the real adapter, per database | `TestNodePeerLookups` (`device_test.go`) |

Long runs of one piece of text are stored as run markers; the test
expands them with `wstest.Expand`.

## What is recorded

The scenarios cover the agent's checks, the single-use token with its
expiry, the viewer's checks with upstream's scope check, streaming with
every message kind and odd JSON, a viewer before its agent with
replacement and the idle timeout, the viewer limit, and frames at the
length marks of the frame header. The text messages of the scenarios are
also the seeds of the fuzz tests.

Each crash case ran in a child process to see whether the Node process
ended: a broken escape in the agent's path, the JSON null from an agent or
a viewer, messages nested too deeply for `JSON.stringify`.

The recorder stubbed the session middleware (the session was the JSON in
`X-Oracle-Session`), the permission lookup (admin and operator have
`device.connect`, the role `throw` makes it fail), the device token check
(`tok-<id>-good`; `tok-throw` fails), the device lookup (dev1, dev2,
dev-3; dev-throw fails) and the scope check (the account `scoped` sees dev1
only, `scopethrow` fails); each step records those calls. `Date.now` and
the timers were a made-up clock and the tokens came from a made-up random
source (xorshift32, the same bytes the Go test reads).

The peer lookups ran on devices whose columns hold every kind of value,
before and after the signal server's table existed, with the copy of a
device only that table has.
