# Node oracle for console/statuspush

`push.js` runs the old console's `services/deviceStatusPush.js` on a real
HTTP server. It stubs the session middleware (the session is the JSON in
the header `X-Oracle-Session`), the permission lookup (admin, super_admin,
global_admin and "allowed" have `enrollment.approve`, "broken" makes the
lookup fail) and the database's id cascade, and fixes `Date.now()`. A
made-up event socket of the API server sends the event frames; a browser on
each socket records what it gets. `../node_push.json` holds:

- 1,536 event frames (every handled type with typical data, and random
  events: types, keys that are array indexes, `type`, `timestamp`,
  `__proto__`, texts with HTML characters, line separators, control
  characters, emoji), written as the API server's event socket writes them
  (Go's encoding/json: sorted keys, HTML escapes), with the message each
  socket got and the id changes carried into the database;
- 88 raw upgrade requests with the raw answer: signed in or not, roles,
  sessions with odd fields, every check of the websocket library (method,
  Upgrade, key, version, subprotocols, extensions), dot segments, a
  backslash, an absolute-form target.

`TestNodeEvents`, `TestNodeHandshakes` and `TestFramesAreTheEventBus`
(which checks that each recorded frame is what `events.MarshalEvent`
writes for that event) replay it.

This folder is a test fixture for the time both servers exist. The service
is gone from `web-nodejs` since it moved, so the script runs only on a tree
that still has it (the commit before the move); the recording stays.

    tools/dev.sh "cd betterdesk-server/console/statuspush/testdata && node nodeoracle/push.js"

After a run, `node ../../ws/wstest/squeeze.js <recording>` (from `testdata`) writes the long runs of the size limits as markers; the test reads the file through `wstest.Expand`.
