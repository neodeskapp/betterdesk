# Recordings of the old console for console/routes/generator

The files below are fixed records of what the Node console answered, made
while it still existed. They can't be recorded again. The Go test replays
them and needs no Node. Don't edit them: when a fix changes an answer on
purpose, its step goes into `fixes` in `fixes_test.go` with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `node_sqlite.json`, `node_postgres.json` | the generator routes of `routes/generator.routes.js`, per database | `TestNodeAnswers` (`replay_test.go`) |

## What is recorded

The routes ran with the real `dbAdapter`, template module, build queue and
bundle service on a scratch database, last on upstream 3.5.264 (relay
addresses with a port). The test compares status and body byte for byte,
and for downloads the headers too.

The console ran from a copy in which the old project's names and vendor
id were replaced by NeoDesk's, so the recordings hold NeoDesk's names. In
that copy the default client repository is the client fork,
`neodeskapp/neodesk-client`, as in the Go console (L238).

The recordings hold the module installed from an uploaded template archive
(made by the recorder: six portable templates with stub binaries), bundles
created, listed, changed, rebuilt, revoked and deleted, every validation
error, the download portal with a ready build (with ranges and HEAD),
previews of odd profiles, and the old toml generator with relay addresses
of every form.

- The signing seed and the server key are made up; the Go test uses the
  same ones (`testSeed`, `testPublicKey`).
- Bundle ids count up from 000000000001: the recorder replaced
  `crypto.randomBytes` for six bytes, and the Go test gives the generator
  the same source.
- Creation times were fixed by statements between the requests, so the
  newest-first lists have one order.
- The paths of the scratch folder are written as `<DATA>`.
- A step can carry a binary request body (`bodyBase64`), and `<ID:slug>`
  in a path stands for the id of the bundle with that slug.

The harness had the console's body parsers, language and no-store
middleware, a session set from a header, and the route file.
