# Recordings of the old console for console/advancedconfig

`vectors.json.gz` is a fixed record of what the Node console's
`services/advancedConfigService.js` did, made while it still existed. It
can't be recorded again. The Go test replays it and needs no Node. Don't
edit it: when a fix changes a result on purpose, the test lists that step
as an intended difference with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `vectors.json.gz` | per step of each scenario the result or the error, and the files of the scratch folder afterwards | `TestNodeVectors` (`vectors_test.go`) |

## What is recorded

The real service ran through scenarios on a scratch folder: listing,
reading and writing every file of the catalog in every state (missing, a
folder, too large, binary, broken UTF-8, behind a symbolic link, below a
file), writing every kind of content, and restoring backups with good and
hostile backup paths. Each scenario had its own copy of the module, with a
`config.js` that pointed the data and keys folders into the scratch folder.
The clock moved one second per `new Date()`, so the names of the backups
are known.

For every step the vectors hold what the function returned or the message
it threw, and the files afterwards (type, mode, size, SHA-256). The Go test
runs the same scenarios on a temporary folder and compares all of it.

The recording ran as one user, for whom every file was writable, so
"not_writable" does not occur in it; `advancedconfig_test.go` covers that
case.
