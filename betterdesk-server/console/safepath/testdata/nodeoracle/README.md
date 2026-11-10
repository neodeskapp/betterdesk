# Node oracle for console/safepath

`vectors.js` builds a small tree with symbolic links (inside the root, out of
it, to nowhere, to themselves) and records what `lib/safePath.js` of the
Node console answers for 1,856 calls of `resolveChildPath`,
`resolvePathWithinRoot`, `resolvePathWithinAnyRoot`, `resolvePathUnderRoot`
and `isPathInsideRoot` on it: the resolved path, or the error. The Go test
`TestNodeVectors` builds the same tree from the list in `../vectors.json`
and must answer the same.

This folder is a test fixture for the time both servers exist. It needs
`web-nodejs/lib/safePath.js` and goes away with it. `vectors.json` stays.

## Running

    tools/dev.sh "cd betterdesk-server/console/safepath/testdata && node nodeoracle/vectors.js"

The tree is made in a temp folder; `<T>` stands for it in the recording.
