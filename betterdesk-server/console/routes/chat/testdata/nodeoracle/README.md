# Comparison with the Node console's handling of chat file names

This folder is a test fixture. It goes away together with `web-nodejs`:
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
