# Recordings of the old console for console/routes/toolkit

The files below are fixed records of what the handlers of the Node
console's toolkit routes (`web-nodejs/routes/toolkit.routes.js`) answered,
made while it still existed. They can't be recorded again. The Go tests
replay them and need no Node. Don't edit them: when a fix changes an
answer on purpose, the test lists that record as an intended difference
with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `text-vectors.jsonl.gz` | the handlers that answer from the request alone, one JSON document per line | `TestNodeTextVectors` (`vectors_test.go`) |
| `ssl-vectors-plain.json` | the certificate check against TLS servers showing the certificates of `certs/` | `TestNodeSSLVectors` (`vectors_test.go`) |
| `ssl-vectors-trusted.json` | the same with the test root trusted | `TestNodeSSLVectors` |

## What is recorded

### The text vectors

Each line holds the route, the request body as JSON text, the random bytes
the request drew (password), the status and the answer as the bytes
`res.json` sent. The guards were replaced by pass-throughs,
`crypto.randomBytes` by a seeded generator and the clock by
2026-11-10T12:00:00Z.

| Route | What it holds |
|---|---|
| `hash` | every algorithm, text with lone surrogates, the size limit |
| `password` | every choice of character sets at every length from 4 to 128, odd lengths and flags, array bodies |
| `base64` | 3,000 generated texts to decode (both alphabets, padding, other characters, letters beyond Latin-1), 400 to encode, every kind of `mode` |
| `urlencode` | 2,500 generated texts to decode, 1,500 to encode, lone surrogates |
| `jwt-decode` | every kind of `exp`, 3,000 generated headers and payloads (valid and broken JSON), 600 tokens that are no clean base64 |

### The certificate vectors

The recorder started a TLS server for each entry of `certs/servers.json`
and ran the certificate check against it at `0.0.0.0` (which the guard lets
through), once as it was and once with the test root trusted. The Go test
hands the same certificates to the certificate check as the result of a
handshake, so both sides describe the same certificates.

The certificates in `certs/` were made by `TestWriteCerts`
(`certs_test.go`). Their private keys were needed for this recording only
and are not kept. Don't make new ones: the recordings were made with these
certificates, and `TestNodeSSLVectors` fails with any others.
