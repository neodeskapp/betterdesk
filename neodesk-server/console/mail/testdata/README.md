# Recordings of the old console for console/mail

The files below are fixed records of what the Node console's mail code
(`services/emailService.js`, `lib/smtpSettingsHandlers.js`,
`config/config.js`) did, made while it still existed. They can't be
recorded again. The Go tests replay them and need no Node. Don't edit
them: when a fix changes an answer on purpose, the test lists that step as
an intended difference with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `node_sqlite.json.gz`, `node_postgres.json.gz` | 15,762 steps of the mail service and the SMTP settings handlers on each database | `TestNodeRecording` (`oracle_test.go`) |
| `misc.json.gz` | upper case, text made of bytes that are no UTF-8, and the flags `config.js` read | `TestUpperCaseAsNode`, `TestFlagsAsNodeConfig`, `TestDecodeLossyAsNode` (`mail_test.go`) |

## What is recorded

The steps ran on a scratch database through the real
`services/dbAdapter.js`, with fixed seeds. Values of any JavaScript type
are written as JSON plus the words `undefined`, `NaN`, `-0` and
`Infinity`. nodemailer was replaced by a stub that recorded the options of
`createTransport` and the message of `sendMail`, and failed where the step
said so. The steps:

| Step | What it did |
|---|---|
| `state` | stored a `smtp_config` row (or deleted it), set the environment and the four values of `config.js` the service read, reset the transporter |
| `env` | changed the environment without anything else |
| `load` | `loadSmtpConfig()` and `getAlertEmail()` |
| `get`, `test`, `put` | the three handlers, with their status, body, the row afterwards, the audit entry and what they logged |
| `send`, `alert`, `help` | `sendEmail`, `sendAlertEmail`, `sendHelpRequestEmail`: the answer, the log, the message handed to nodemailer |
| `baseurl` | `getPanelBaseUrl()` |
| `has`, `logsent`, `logcount` | the notification log of the database adapter |
| `reset` | `resetTransporter()` |

The inputs: about 1,000 stored configurations (every member with about 80
values of every JSON type, damaged and odd JSON, random mixes), 350 sets of
`SMTP_*` variables, messages with every form of recipient list, 875 alerts
and rules and 456 help requests with members of every type, and 1,317
saves over 16 kinds of existing rows.

Each step that used a transporter carries a probe of the real nodemailer
for its options, made without a mail server: name lookups answered
127.0.0.1 and connections went to this machine's loopback address, where
nothing listened. The probe says whether nodemailer got as far as opening
a connection and with which host, port, TLS and certificate setting, or
which error it failed with before, or which exception ended the process.
The Go tests take from it what the transport has to be given, and which
configurations fail without a connection. No mail was sent.

`misc.json.gz` holds `toUpperCase` for every code point it changes (the
alert mail's subject), the text Node made of 3,000 byte strings that are
no UTF-8 (a stored row or a request body can hold such bytes), and what
`config/config.js` made of `SMTP_TLS_VERIFY`, `HTTP_REDIRECT_HTTPS`,
`HOST`, `PORT` and `ALLOW_SELF_SIGNED_CERTS`, loaded anew for each of 211
sets of values.
