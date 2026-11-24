# Recordings of the old console for console/servicedef

The file below is a fixed record of what the Node console's
`sanitizeGoServerServiceConfig` and the helpers it calls
(`services/updateService.js`) did, made while the console still existed.
It can't be recorded again. The Go tests replay it and need no Node. Don't
edit it: when a fix changes a result on purpose, the change goes into
`fixes_test.go` with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `sanitize.json` | the text helpers with many inputs, and whole checks on Linux and Windows | `TestTextVectors`, `TestFlowVectors` (`vectors_test.go`) |

## What is recorded

The functions ran on their own, taken out of the updater, with the system
around them stubbed: the shell, nssm, the files and the privileged daemon
reload answered what each vector scripts, and every command and write was
recorded.

The functions came from a copy of the old console in which the old
project's names were replaced by NeoDesk's, and the change message about
the signal, relay and API ports names no issue number of the old project.
