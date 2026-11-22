# Recordings of the old console for console/statuspush

The file below is a fixed record of what the Node console's
`services/deviceStatusPush.js` did, made while the console still existed.
It can't be recorded again. The Go tests replay it and need no Node. Don't
edit it: when a fix changes a message or an answer on purpose, the test
lists that record as an intended difference with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `node_push.json` | 1,536 event frames with what each socket got, and 88 raw upgrade requests with their raw answers | `TestNodeEvents`, `TestNodeHandshakes`, `TestFramesAreTheEventBus` (`push_test.go`) |

## What is recorded

The old service ran on a real HTTP server. Stubbed were the session
middleware (the session is the JSON in the header `X-Oracle-Session`), the
permission lookup (admin, super_admin, global_admin and "allowed" have
`enrollment.approve`, "broken" makes the lookup fail) and the database's
id cascade, and `Date.now()` was fixed. A made-up event socket of the API
server sent the event frames; a browser on each socket recorded what it
got.

- The event frames cover every handled type with typical data, and random
  events: types, keys that are array indexes, `type`, `timestamp`,
  `__proto__`, texts with HTML characters, line separators, control
  characters, emoji. They are written as the API server's event socket
  writes them (Go's encoding/json: sorted keys, HTML escapes), with the
  message each socket got and the id changes carried into the database.
  `TestFramesAreTheEventBus` checks that each recorded frame is what
  `events.MarshalEvent` writes for that event.
- The upgrade requests are signed in or not, with roles, sessions with odd
  fields, every check of the websocket library (method, Upgrade, key,
  version, subprotocols, extensions), dot segments, a backslash, an
  absolute-form target.

Long runs of one piece of text are stored as run markers; the test expands
them with `wstest.Expand`.
