# Node oracle for console/remoterelay

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
the old console's `services/remoteRelay.js` (and `services/dbAdapter.js`)
from `web-nodejs` and record what it does; the Go tests replay the
recordings. They go when `web-nodejs` goes: once the service is deleted they
run only on a tree that still has it (the commit before the move), and the
recordings stay.

`remote.js` writes `../node_remote.json`: 7 scenarios with 168 steps on a
real HTTP server (the agent's checks, the single-use token with its expiry,
the viewer's checks with upstream's scope check, streaming with every
message kind and odd JSON, a viewer before its agent with replacement and
the idle timeout, the viewer limit, frames at the length marks of the
frame header), and 13 crash cases, each run in a
child process to see whether the Node process ends (a broken escape in the
agent's path, the JSON null from an agent or a viewer, messages nested too
deeply for `JSON.stringify`), plus the deepest status message
`JSON.stringify` still wrote inside the socket's listener. Stubbed are the
session middleware (the session is the JSON in `X-Oracle-Session`), the
permission lookup (admin and operator have `device.connect`, the role
`throw` makes it fail), the device token check (`tok-<id>-good`,
`tok-throw` fails), the device lookup (dev1, dev2, dev-3; dev-throw fails)
and the scope check (the account `scoped` sees dev1 only, `scopethrow`
fails); each step records those calls. `Date.now` and the timers are a
made-up clock and the tokens come from a made-up random source (xorshift32,
the same bytes the Go test reads). `TestNodeScenarios` replays every step;
`TestWhereNodeEndedTheConnectionIsDropped` and
`TestStringifyDepthIsTheRecordedOne` read the crash cases.

`peer.js` writes `../node_peer_sqlite.json` and, with `ORACLE_PG_DSN`,
`../node_peer_postgres.json`: `db.getPeerById` of the real adapter for
devices whose columns hold every kind of value, before and after the signal
server's table exists, with the copy of a device only that table has.
`TestNodePeerLookups` replays them on both databases.

`fuzzhelper.js` answers the fuzz tests (`REMOTERELAY_NODE_HELPER=1`) with
the old message code: the two message listeners, copied letter for letter
because the module does not export them, and `decodeURIComponent`.

    tools/dev.sh "cd betterdesk-server/console/remoterelay/testdata && node nodeoracle/remote.js && node ../../ws/wstest/squeeze.js node_remote.json"
    DEV_DOCKER_ARGS="--network bdgo" tools/dev.sh "cd betterdesk-server/console/remoterelay/testdata && node nodeoracle/peer.js && ORACLE_PG_DSN=postgres://betterdesk:bdgo-local@bdgo-testpg:5432/betterdesk_test?sslmode=disable node nodeoracle/peer.js"
    DEV_DOCKER_ARGS="-e REMOTERELAY_NODE_HELPER=1" tools/dev.sh "cd betterdesk-server/console/remoterelay && go test -run '^$' -fuzz '^FuzzAgentMessage$' -fuzztime 10m ."
