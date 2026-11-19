# Node oracle for console/avatars

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

These scripts run the console's Node modules (`web-nodejs`) and record what
they answer. The Go tests in `console/avatars` replay every record and must
answer the same (`oracle_test.go`, `service_test.go`).

This folder is a test fixture from the time both servers existed. It needs
`web-nodejs`, which this tree no longer has. The recorded files in the parent folder
(`*.json.gz`, `counts.json`) stay: they are plain data and keep pinning the
Go code to what Node did.

## Scripts

`vectors.js` loads `lib/avatarImage.js`. No request leaves the process:
`http.request` and `https.request` are replaced by a fake that records what
would be sent (scheme, host, port, path, headers) and plays the answers a
record names. It writes:

| File | List | What it holds |
|---|---|---|
| `image` | `sniff` | `sniffImageType` for the signatures, each cut short, each with one bit changed, and random bytes |
| | `data_url` | `parseAvatarDataUrl` for every declared type with every kind of content, both base64 alphabets, padding variants, damaged text, sizes around a small limit and around the real one |
| | `refused` | `isRefusedAvatarAddress` for about 4,200 texts: the edges of every refused range, random IPv4 and IPv6 addresses, the forms that carry an IPv4 address, zones, and text that is no address |
| `fetch` | `fetches` | `fetchAvatarUrl` for about 900 addresses (schemes, credentials, host names, IPv4 in hex, octal and short forms, IPv6, ports, paths with dot segments and characters that get escaped), for answers by status, type, size and content, and for redirects by status, `Location` and number |
| | `loads` | `loadAvatarPicture` by kind of value, with the version and the stored source address, and `imageVersion` |

Host names with letters outside ASCII are left out: the Go transport
converts them when it connects, not when the address is read.

`store.js` runs the real `services/avatarService.js` on a scratch database
and records every call of a scenario: `store_sqlite` on SQLite, and
`store_postgres` when `ORACLE_PG_DSN` names a PostgreSQL server (it works
in a schema of its own and drops it). The scenario stores, replaces and
removes pictures from both sources, lets every kind of refusal happen,
writes for a user that does not exist, and moves the clock past the 30
seconds the list of pictures is kept.

## Running

From the worktree, in the dev image:

    tools/dev.sh "cd betterdesk-server/console/avatars/testdata && node nodeoracle/vectors.js && node nodeoracle/store.js"
    DEV_DOCKER_ARGS="--network bdgo" tools/dev.sh "cd betterdesk-server/console/avatars/testdata && \
      ORACLE_PG_DSN='postgres://betterdesk:bdgo-local@bdgo-testpg:5432/betterdesk_test?sslmode=disable' node nodeoracle/store.js"

`vectors.js` writes the same files on every run (its generators are
seeded). `store.js` records times of the run; the Go test checks their
form, not their value.
