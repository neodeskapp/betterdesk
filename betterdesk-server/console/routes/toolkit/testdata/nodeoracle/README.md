# Node oracle for console/routes/toolkit

These scripts ran the handlers of the Node console's toolkit routes
(`web-nodejs/routes/toolkit.routes.js`) and recorded what they answered.
The Go tests in `console/routes/toolkit` replay every record and must
answer the same (`vectors_test.go`).

The route file was deleted in the commit that moved the group to Go, so
the scripts no longer run on this tree. They stay as the record of how
the files in the parent folder were made; to run them, check out the
commit before the move. The recorded files stay: they are plain data and
keep pinning the Go code to what Node did.

## Scripts

`gen.js` calls the handlers that answer from the request alone and writes
`../text-vectors.jsonl.gz`, one JSON document per line: the route, the
request body as JSON text, the random bytes the request drew (password),
the status and the answer as the bytes `res.json` sent. The guards are
replaced by pass-throughs, `crypto.randomBytes` by a seeded generator and
the clock by 2026-11-10T12:00:00Z.

| Route | What it holds |
|---|---|
| `hash` | every algorithm, text with lone surrogates, the size limit |
| `password` | every choice of character sets at every length from 4 to 128, odd lengths and flags, array bodies |
| `base64` | 3,000 generated texts to decode (both alphabets, padding, other characters, letters beyond Latin-1), 400 to encode, every kind of `mode` |
| `urlencode` | 2,500 generated texts to decode, 1,500 to encode, lone surrogates |
| `jwt-decode` | every kind of `exp`, 3,000 generated headers and payloads (valid and broken JSON), 600 tokens that are no clean base64 |

`ssl.js` starts a TLS server for each entry of `../certs/servers.json`,
runs the certificate check against it at `0.0.0.0` (which the guard lets
through) and writes `../ssl-vectors-plain.json`, and with the test root
trusted (`NODE_EXTRA_CA_CERTS`) `../ssl-vectors-trusted.json`.

The certificates in `../certs` are test material made by `TestWriteCerts`
(`certs_test.go`). Their private keys were only needed for this recording
and are not kept (the repository ignores `*.key`); `TestWriteCerts` writes
new certificates with new keys, and then the answers have to be recorded
again. The Go test hands the same certificates to the certificate check
as the result of a handshake, so both sides describe the same
certificates.

## Running (on a checkout that still has the route file)

    tools/dev.sh "cd betterdesk-server/console/routes/toolkit/testdata && node nodeoracle/gen.js"
    tools/dev.sh "cd betterdesk-server/console/routes/toolkit/testdata && node nodeoracle/ssl.js && NODE_EXTRA_CA_CERTS=certs/root.crt node nodeoracle/ssl.js trusted"

The output is the same on every run: the generators are seeded and the
certificates are files.
