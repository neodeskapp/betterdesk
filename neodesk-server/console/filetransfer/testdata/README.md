# Recordings of the old console for console/filetransfer

`vectors.json.gz` is a fixed record of what the Node console's
`services/fileTransferService.js` did, made while it still existed. It
can't be recorded again. The Go tests replay it and need no Node. Don't
edit it: when a fix changes a step on purpose, the fix goes into
`fixes_test.go` with the row id, and `FILETRANSFER_RECORD_FIXED=1` writes
the Go answers of the steps it names into `vectors_fixed.json.gz`.

| File | What it holds | Replayed by |
|---|---|---|
| `vectors.json.gz` | scripted lives of transfers, and `parseInt(size, 10) \|\| 0` for many values | `TestNodeVectors`, `TestParseSizeVectors` (`vectors_test.go`) |
| `vectors_fixed.json.gz` | written by the Go test: the answers of the steps a fix changed | `TestNodeVectors` |

## What is recorded

The real service ran from a copy in a scratch folder, because it keeps its
files next to its own code. The lives of transfers are new transfers with
every kind of value, chunks as base64 text and in every other shape a
device could send, cancels, the lists with filters, the messages of a
device, and the housekeeping with a clock the recorder moved. Per step the
recording holds what the function returned or threw, every record
afterwards and the files in the transfers folder with their hashes.

The clock moved one millisecond per reading and the random bytes counted
up, so the Go test gives its service the same and gets the same ids and
times.
