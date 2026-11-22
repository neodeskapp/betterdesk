# Recordings of the old console for console/routes/securityaudit

The file below is a fixed record of what the Node console answered, made
while it still existed. It can't be recorded again. The Go tests replay it
and need no Node. Don't edit it: when a fix changes an answer on purpose,
its scenario goes into `scenarioFixes` (`replay_test.go`) with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `securityaudit.json` | scenarios: answers of a made-up API server and what the Node routes made of them | `TestRoutesMatchNode` (`replay_test.go`) |

## What is recorded

The recorder ran the routes of `web-nodejs/routes/security-audit.routes.js`
through the console's real API client against a made-up API server on a
local port. The times in the recorded answers of the API server are
relative to the moment of the recording, which each scenario carries as
`now`.
