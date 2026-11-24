# Recordings of the old console for console/mail/message

The package `message` builds the same bytes as nodemailer 10.4.0 on Node
22. The files below are fixed records of what the real nodemailer of the
Node console produced, made while the console still existed. They can't be
recorded again. The Go tests replay them and need no Node. Don't edit
them: when a fix changes a result on purpose, the test lists that vector
as an intended difference with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `message.json.gz` | about 10,400 mails and what nodemailer handed to the transport for each | `TestNodeMessageVectors` (`vectors_test.go`) |
| `addresses.json.gz` | nodemailer's address normalization for a sweep of every code point and about 14,000 addresses | `TestNodeAddressVectors` (`domain_test.go`) |

`../tables.bin.gz` holds the IDNA, normalization, text direction and
joining tables of ada, the URL library inside Node 22, and the lower-case
mapping of Node's JavaScript engine. It was generated from Node by the
old recorder and can't be generated again. The Go code embeds it.

## What is recorded

The mails went through `transporter.sendMail({ from, to, subject, text,
html })`, the call `services/emailService.js` made. About 800 of them came
out of the real `sendAlertEmail` and `sendHelpRequestEmail` of that file.
The transport was an object that recorded `mail.message.getEnvelope()`,
`mail.message.messageId()` and the bytes of
`mail.message.createReadStream()`, which is what nodemailer's SMTP
transport takes from a mail. The clock and `crypto.randomBytes` were
replaced, and every vector names the time and the random bytes it was
built with. The mails of `message.json.gz` were made from a copy of the
old console in which the old project's names were replaced by NeoDesk's,
so they name NeoDesk.

The addresses went through nodemailer's address normalization
(`MimeNode._normalizeAddress`, with Node's `url.domainToASCII` and
`url.domainToUnicode` behind it): every code point in ten places of an
address, 11 million addresses, stored as one hash per 4,096 code points,
and about 14,000 addresses written by hand or built by a seeded generator.
The ten places are `sweepTemplates` in `domain_test.go`.

Both files are gzipped, with one JSON object per line and everything
outside printable ASCII written as `\uXXXX`.

A line of `message.json.gz` has the mail (`from`, `to`, `subject`, `text`,
`html`; a key that is missing was `undefined`), the clock in milliseconds
(`now`) and the random bytes in the order they were drawn (`rand`, hex).
The result is the envelope (`envFrom`, which is `false` without a sender,
and `envTo`), the Message-ID (`id`) and the message (`raw`, one character
per byte), or the error text (`err`). A message over 24 KiB is stored as
`rawLength` and `rawSha256`, a recipient list over 2,000 as `envToCount`
and `envToSha256`.

A line of `addresses.json.gz` is `{in, out}` for one address, or
`{block, count, sha256}` for a block of the sweep: the hash over the
results of all addresses of the block, each followed by a line feed.

Go gets its input as UTF-8, so inputs with half a surrogate pair in them
were left out. Where nodemailer itself produced one (the fallback Punycode
decoder can), the recording holds U+FFFD, which is what would go over the
wire, and so does the Go code.
