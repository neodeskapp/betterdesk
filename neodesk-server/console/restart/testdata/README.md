# Recordings of the old console for console/restart

The file below is a fixed record of what the Node console's
`services/restartCoordinator.js` did, made while the console still
existed. It can't be recorded again. The Go test replays it and needs no
Node. Don't edit it: when a fix changes a step on purpose, the test lists
that step as an intended difference with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `vectors.json.gz` | scripted lives of a restart transaction, step by step | `TestNodeVectors` (`vectors_test.go`) |

## What is recorded

The real coordinator ran through scripted lives of a restart transaction:
registering changes, cancel, confirm, the public status after a new start
of the process, complete, dismiss, and the same calls on transaction files
that were edited by hand or are no JSON. The services the coordinator
calls (the restart of the server, the rollbacks, the API client) were
fakes whose answers each step set. The clock moved one second per reading
and the random bytes counted up, with a fixed seed.

The Go test gives its service the same clock, random bytes and fakes and
compares, per step, the result or the error, the transaction file byte for
byte, the calls made and whether the end of the process was scheduled.
