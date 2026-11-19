# Node oracle for console/ssrfguard

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

These scripts run the Node console's guard (`web-nodejs/lib/ssrfGuard.js`)
and Node's own URL parser, and record or report what they answer. The Go
tests in `console/ssrfguard` must answer the same.

This folder is a test fixture from the time both servers existed. It needs
`web-nodejs`, which this tree no longer has. The recorded file in the parent folder
(`vectors.jsonl.gz`) stays: it is plain data and keeps pinning the Go code
to what Node did.

## Scripts

`gen.js` writes `../vectors.jsonl.gz`, one JSON document per line. The
resolver is replaced by a table (`scenario`), so no vector needs a network.

| `fn` | What it holds |
|---|---|
| `isBlockedIp` | `isBlockedIp` for IPv4 literals of every first octet, odd spellings, IPv6 forms and generated text, with and without `allowPrivate` |
| `hostname` | `assertSafeHostname` for names, addresses and odd input |
| `resolved` | `assertSafeResolvedHost` with what the resolver was asked and what it answered: public, private, loopback, link-local, mapped addresses, several records, none, errors |
| `parse` | `new URL(text)` for hand-written and generated URLs: failure, or the members of the URL |
| `httpUrl`, `monitoringUrl` | `assertSafeHttpUrl` and `assertSafeMonitoringUrl` for the same URLs, each with a resolver scenario |

`vectors_test.go` replays every line.

`server.js` answers the same questions over stdin and stdout, one line
each. `FuzzSSRFGuard` starts it when `node` and `web-nodejs` are there and
compares every input the fuzzer makes with Node's answer.

`casing.js` writes `../../casetables.go`, the lower-case and final-sigma
tables of `String.prototype.toLowerCase` in this Node, for every code
point. The Go code lowers host names with them, so it does not depend on
which Unicode version Go itself has.

## Running

From the worktree, in the dev image:

    tools/dev.sh "node betterdesk-server/console/ssrfguard/testdata/nodeoracle/gen.js"
    tools/dev.sh "cd betterdesk-server && node console/ssrfguard/testdata/nodeoracle/casing.js && gofmt -w console/ssrfguard/casetables.go"

The output is the same on every run (the generator is seeded), so a diff
in the recorded file after a run means Node's behaviour changed.
