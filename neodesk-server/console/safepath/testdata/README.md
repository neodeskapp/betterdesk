# Recordings of the old console for console/safepath

The file below is a fixed record of what the Node console's
`lib/safePath.js` answered, made while the console still existed. It
can't be recorded again. The Go test replays it and needs no Node. Don't
edit it: when a fix changes an answer on purpose, the test lists that
vector as an intended difference with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `vectors.json` | a small tree with symbolic links (`tree`) and 1,856 calls on it (`vectors`) | `TestNodeVectors` (`safepath_test.go`) |

## What is recorded

The tree has symbolic links inside the root, out of it, to nowhere and to
themselves. The calls are `resolveChildPath`, `resolvePathWithinRoot`,
`resolvePathWithinAnyRoot`, `resolvePathUnderRoot` and `isPathInsideRoot`,
each with the resolved path or the error. The tree was made in a temp
folder; `<T>` stands for it in the file. The Go test builds the same tree
from `tree` and must answer the same.
