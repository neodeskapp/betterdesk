# Comparison with the Node console's handling of chat file names

`web-nodejs` is gone from this tree. These scripts stay as the record of
how the recordings in this package were made; the Go tests replay the
recordings and need no Node. To run a script again, use a tree that still
has `web-nodejs`: the spec worktree
`C:\Users\info\worktrees\betterdesk-go-spec264`. Mount its `web-nodejs`,
with its modules, over `/repo/web-nodejs` of this tree:

    DEV_DOCKER_ARGS="-v C:/Users/info/worktrees/betterdesk-go-spec264/web-nodejs:/repo/web-nodejs -v bdgo-node-nm:/repo/web-nodejs/node_modules" \
      tools/dev.sh "cd betterdesk-server/<this folder> && node <script>"

Its Node code is upstream's release there, so a script that loads a file
the port deleted or changed on the way can fail or record other answers.

This folder is a test fixture. It stays now that `web-nodejs` is gone:
once the Node console is deleted there is nothing left to compare with.

`names.js` runs the expressions of `web-nodejs/routes/chat.routes.js` and of
Express's `res.download` on about 4,000 file names and writes the results to
`../names.json.gz`, one JSON object per line:

- `name`: the name a client gave its file
- `ext`: `path.extname(name).slice(0, 10)`, the extension the file is stored
  with
- `fileId`: `path.basename(stored, path.extname(stored))` for the stored
  name `0123456789abcdef0123456789abcdef<ext>`
- `disposition`: the `Content-Disposition` header of a download of the
  stored name, or `{"throws": "<message>"}`. It throws only for an
  extension that ends in half of a character outside the basic plane, a
  name that cannot be on disk
- `type`: the `Content-Type` header of the download

`TestNameVectors` in `names_test.go` replays every line.

Run it in the dev image, from the repository root:

    node betterdesk-server/console/routes/chat/testdata/nodeoracle/names.js

## The relay

`relay.js` runs the old console's `services/chatRelay.js` on a real HTTP
server and plays scenarios against it. `Date.now()` and the timers are a
made-up clock; the device token check (`tok-<id>-good` passes for that
device, `tok-throw` makes it fail), the permission lookup (admin,
super_admin and operator have `chat.access`, `throw` makes it fail), the
session (the JSON in `X-Oracle-Session`) and the API server (the answers
of each scenario by `METHOD path`, behind the real path guard of
`lib/goApiPath.js`) are stubs that note every call. Sockets are opened
with raw upgrade requests (header values as Latin-1 bytes) and read frame
by frame.

After each step it records what every socket got (the answer's head, text
and binary messages, the close frame, the end of the connection) and every
call: to the API server (method, path, body), the token check (device id,
token) and the permission lookup (role, permission). `../node_relay.json`
holds 19 scenarios with 235 steps. `TestNodeScenarios` in
`node_relay_test.go` plays them on the Go relay with the same clock and
the same API answers; every socket and every kind of call must see the
same, in order.

Of the twelve scenarios named `crash: ...`, ten send a message that ended
the Node process (a member of `null`, `String()` of an object with its own
`toString`; BUGS SK3) and two a near miss that did not; the recording
notes `{crash}` and that every connection went. The Go
test checks what the Go console does instead (DECISIONS A19): the sender's
connection ends without an answer and every other socket stays.

    tools/dev.sh "cd betterdesk-server/console/routes/chat/testdata && node nodeoracle/relay.js"

The relay is gone from `web-nodejs` since it moved: run the script on a
tree before that.
