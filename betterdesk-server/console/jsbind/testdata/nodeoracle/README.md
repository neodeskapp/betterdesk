# Node oracle for console/jsbind

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

`vectors.js` records what JavaScript and the Node console's database drivers
(`better-sqlite3` and `pg` from `web-nodejs/node_modules`) make of values.
The tests in `console/jsbind` replay `../vectors.json` and must agree.

This folder is a test fixture from the time both servers existed. It needs
`web-nodejs/node_modules`, which this tree no longer has. `vectors.json` stays.

| Part | What it holds |
|---|---|
| `strings` | about 4,100 texts with `Number(s)`, `s.trim()` and `s.toLowerCase()` |
| `values` | JSON values of every type with `Number`, `String`, truth, `JSON.stringify`, the parameter node-postgres sends, and the type and text SQLite gets from better-sqlite3 (or the error it throws) |
| `lists` | lists of values bound to a statement with two placeholders: what arrives, or the error |

## Running

    tools/dev.sh "cd betterdesk-server/console/jsbind/testdata && node nodeoracle/vectors.js"

The generator has a fixed seed, so a diff in `vectors.json` after a run means
JavaScript or a driver changed.
