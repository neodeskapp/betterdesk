# Recordings of the old console for console/ssrfguard

`vectors.jsonl.gz` is a fixed record of what the Node console's guard
(`web-nodejs/lib/ssrfGuard.js`) and Node's own URL parser answered, made
while the Node console still existed. It can't be recorded again. The Go
tests replay it and need no Node. Don't edit it: when a fix changes an
answer on purpose, the fixed rows go into `guardtest`, which the replay
asks before it compares.

| File | What it holds | Replayed by |
|---|---|---|
| `vectors.jsonl.gz` | one call and its answer per line | `TestNodeVectors` (`vectors_test.go`) |

## What is recorded

The resolver was replaced by a table (`scenario`), so no vector needs a
network. The lines by `fn`:

| `fn` | What it holds |
|---|---|
| `isBlockedIp` | `isBlockedIp` for IPv4 literals of every first octet, odd spellings, IPv6 forms and generated text, with and without `allowPrivate` |
| `hostname` | `assertSafeHostname` for names, addresses and odd input |
| `resolved` | `assertSafeResolvedHost` with what the resolver was asked and what it answered: public, private, loopback, link-local, mapped addresses, several records, none, errors |
| `parse` | `new URL(text)` for hand-written and generated URLs: failure, or the members of the URL |
| `httpUrl`, `monitoringUrl` | `assertSafeHttpUrl` and `assertSafeMonitoringUrl` for the same URLs, each with a resolver scenario |

`../casetables.go` holds the lower-case and final-sigma tables of
`String.prototype.toLowerCase` in Node v22.23.4, for every code point. It
was generated from Node by the old recorder and can't be generated again.
The Go code lowers host names with these tables, so it doesn't depend on
the Unicode version of Go itself.
