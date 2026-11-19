# Node oracle for console/safepath

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

`vectors.js` builds a small tree with symbolic links (inside the root, out of
it, to nowhere, to themselves) and records what `lib/safePath.js` of the
Node console answers for 1,856 calls of `resolveChildPath`,
`resolvePathWithinRoot`, `resolvePathWithinAnyRoot`, `resolvePathUnderRoot`
and `isPathInsideRoot` on it: the resolved path, or the error. The Go test
`TestNodeVectors` builds the same tree from the list in `../vectors.json`
and must answer the same.

This folder is a test fixture from the time both servers existed. It needs
`web-nodejs/lib/safePath.js`, which this tree no longer has. `vectors.json` stays.

## Running

    tools/dev.sh "cd betterdesk-server/console/safepath/testdata && node nodeoracle/vectors.js"

The tree is made in a temp folder; `<T>` stands for it in the recording.
