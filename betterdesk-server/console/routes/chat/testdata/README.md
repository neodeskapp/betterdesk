# Recordings of the old console for console/routes/chat

The files below are fixed records of what the Node console did, made while
it still existed. They can't be recorded again. The Go tests replay them
and need no Node. Don't edit them: when a fix changes a step of the relay
on purpose, it goes into `fixes` in `node_relay_test.go` with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `names.json.gz` | what the chat routes and Express's `res.download` made of about 4,000 file names, one JSON object per line | `TestNameVectors` (`names_test.go`) |
| `node_relay.json` | 19 scenarios with 235 steps of `services/chatRelay.js` | `TestNodeScenarios` (`node_relay_test.go`) |

## File names

Each line of `names.json.gz` holds the results of the expressions of
`routes/chat.routes.js` and of `res.download` for one name:

- `name`: the name a client gave its file
- `ext`: `path.extname(name).slice(0, 10)`, the extension the file is stored
  with
- `fileId`: `path.basename(stored, path.extname(stored))` for the stored
  name `0123456789abcdef0123456789abcdef<ext>`
- `disposition`: the `Content-Disposition` header of a download of the
  stored name, or `{"throws": "<message>"}`. It throws only for an
  extension that ends in half of a character outside the basic plane, a
  name that can't be on disk
- `type`: the `Content-Type` header of the download

## The relay

The recorder ran `services/chatRelay.js` on a real HTTP server and played
scenarios against it. `Date.now()` and the timers were a made-up clock. The
device token check (`tok-<id>-good` passes for that device, `tok-throw`
makes it fail), the permission lookup (admin, super_admin and operator
have `chat.access`, `throw` makes it fail), the session (the JSON in
`X-Oracle-Session`) and the API server (the answers of each scenario by
`METHOD path`, behind the real path guard of `lib/goApiPath.js`) were stubs
that noted every call. Sockets were opened with raw upgrade requests
(header values as Latin-1 bytes) and read frame by frame.

After each step the recording holds what every socket got (the answer's
head, text and binary messages, the close frame, the end of the
connection) and every call: to the API server (method, path, body), the
token check (device id, token) and the permission lookup (role,
permission). `TestNodeScenarios` plays them on the Go relay with the same
clock and the same API answers; every socket and every kind of call must
see the same, in order.

Of the twelve scenarios named `crash: ...`, ten send a message that ended
the Node process (a member of `null`, `String()` of an object with its own
`toString`; BUGS SK3) and two a near miss that did not; the recording notes
`{crash}` and that every connection went. The Go test checks what the Go
console does instead (DECISIONS A19): the sender's connection ends without
an answer and every other socket stays.
