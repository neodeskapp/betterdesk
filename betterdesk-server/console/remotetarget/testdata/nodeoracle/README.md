# Node oracle for console/remotetarget

This script is a test fixture for the time both consoles exist. It loads
the old console's `services/remoteTargetGateway.js` from `web-nodejs` and
records what it does; the Go test replays the recording. It goes when
`web-nodejs` goes: once the service is deleted it runs only on a tree that
still has it (the commit before the move), and the recording stays.

`gateway.js` writes `../node_gateway.json`: 5 scenarios with 52 steps on a
real HTTP server in front of a made-up API server, whose tunnel echoes
messages, closes with 1000 or 1011, drops the connection, refuses with 503,
answers after 300 ms, sends a greeting or a message over the 16 MiB limit,
by the target's id, and accounts whose name Node's HTTP client cannot put into a header. Stubbed are the session middleware (the session is the
JSON in `X-Oracle-Session`), the permission lookup (admin and operator have
`remote_target.connect`, the role `throw` makes it fail), the target lookup
and the scope check (the account `scoped` sees `rt_echo-0001` only,
`scopethrow` fails). Each step records what every browser socket got, what
every tunnel got (the path and the headers of its handshake, messages, the
close), and the outside calls. A message over 4 KiB is noted by its length
and SHA-256.

`TestNodeScenarios` replays every step against a made-up API handler in Go
that behaves the same, reached in process as the console reaches the API
server. Two differences are allowed there: the tunnel's handshake has no
X-API-Key (the in-process handler carries the console's identity), and a session whose
user object has no name sends the name at the session's top in X-Username
(the Go session reads it there), where Node sent `user#<id>`.

    tools/dev.sh "cd betterdesk-server/console/remotetarget/testdata && node nodeoracle/gateway.js"
