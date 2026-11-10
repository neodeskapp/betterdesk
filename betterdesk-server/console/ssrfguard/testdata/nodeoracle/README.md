# Node oracle for console/ssrfguard

These scripts run the Node console's guard (`web-nodejs/lib/ssrfGuard.js`)
and Node's own URL parser, and record or report what they answer. The Go
tests in `console/ssrfguard` must answer the same.

This folder is a test fixture for the time both servers exist. It needs
`web-nodejs` and goes away with it. The recorded file in the parent folder
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
