# Node oracle for the mail message builder

The Go package `message` builds the same bytes as nodemailer 10.4.0 on
Node 22. The scripts here run the real nodemailer from
`web-nodejs/node_modules` and write what it produces. The Go tests replay
those files.

The scripts and the vector files are fixtures for the move from Node to Go.
They go away together with `web-nodejs`. `tables.bin.gz` in the package
folder stays: the Go code needs it.

## The scripts

`message.js` sends about 10,400 mails through
`transporter.sendMail({ from, to, subject, text, html })`, the call
`services/emailService.js` makes. About 800 of them come out of the real
`sendAlertEmail` and `sendHelpRequestEmail` of that file. The transport is an
object that records `mail.message.getEnvelope()`, `mail.message.messageId()`
and the bytes of `mail.message.createReadStream()`, which is what nodemailer's
SMTP transport takes from a mail. The clock and `crypto.randomBytes` are
replaced by the script, and every vector names the time and the random bytes
it was built with. It writes `../message.json.gz`, which `TestNodeMessageVectors`
replays.

`addresses.js` runs nodemailer's address normalization
(`MimeNode._normalizeAddress`, with Node's `url.domainToASCII` and
`url.domainToUnicode` behind it) on every code point in ten places of an
address, 11 million addresses, and on about 14,000 addresses written by hand
or built by a seeded generator. It writes `../addresses.json.gz`, which
`TestNodeAddressVectors` replays. The sweep is stored as one hash per 4,096
code points.

`tables.js` writes `../../tables.bin.gz`. It takes the IDNA, normalization,
text direction and joining tables out of the source of ada, the URL library
inside Node, and reads the lower-case mapping from Node itself.

## Running them

In the dev image, from `betterdesk-server/console/mail/message`:

```sh
node testdata/nodeoracle/addresses.js
node testdata/nodeoracle/message.js
go test . -run 'TestNode' -count=1
```

Every run writes the same files as long as nodemailer and Node stay the same.

The tables only change with the Node version. `process.versions.ada` names
the version of ada to download:

```sh
curl -sLO https://github.com/ada-url/ada/releases/download/v2.9.2/ada.cpp
node testdata/nodeoracle/tables.js ada.cpp
```

## The vector files

Both are gzipped, with one JSON object per line and everything outside
printable ASCII written as `\uXXXX`.

A line of `message.json.gz` has the mail (`from`, `to`, `subject`, `text`,
`html`; a key that is missing was `undefined`), the clock in milliseconds
(`now`) and the random bytes in the order they were drawn (`rand`, hex). The
result is the envelope (`envFrom`, which is `false` without a sender, and
`envTo`), the Message-ID (`id`) and the message (`raw`, one character per
byte), or the error text (`err`). A message over 24 KiB is stored as
`rawLength` and `rawSha256`, a recipient list over 2,000 as `envToCount` and
`envToSha256`.

A line of `addresses.json.gz` is `{in, out}` for one address, or
`{block, count, sha256}` for a block of the sweep: the hash over the results
of all addresses of the block, each followed by a line feed.

Go gets its input as UTF-8, so the scripts leave out every input with half
a surrogate pair in it. Where nodemailer itself produces one (the fallback
Punycode decoder can), the scripts record U+FFFD, which is what would go
over the wire, and so does the Go code.

## Checking other inputs

To compare Go and Node on more inputs:

```sh
# more random mails and addresses
node testdata/nodeoracle/message.js --mix 50000 --seed 7 --out /tmp/mix.json
node testdata/nodeoracle/addresses.js --mix 300000 --seed 7 --out /tmp/addr.json
# your own: a JSON array of mails ({from, to, subject, text, html}) or of addresses
node testdata/nodeoracle/message.js --inputs mails.json --out /tmp/mix.json
node testdata/nodeoracle/addresses.js --inputs addresses.json --out /tmp/addr.json

MESSAGE_VECTORS=/tmp/mix.json MESSAGE_ADDRESS_VECTORS=/tmp/addr.json \
  go test . -run TestExtra -count=1 -v
```

When a block of the sweep fails, `node testdata/nodeoracle/addresses.js
--block 12 --out /tmp/block.json` writes every result of that block, and
`MESSAGE_ADDRESS_VECTORS=/tmp/block.json` shows which address differs.
