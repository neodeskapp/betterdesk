# Node oracle for console/mail

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

These scripts run the Node console's mail code (`web-nodejs/services/emailService.js`,
`web-nodejs/lib/smtpSettingsHandlers.js`, `web-nodejs/config/config.js`) and
record what it answers. The Go tests in `console/mail` replay every record and
must answer the same.

This folder is a test fixture from the time both servers existed. It needs
`web-nodejs`, which this tree no longer has. The recorded files in the parent folder
(`*.json.gz`) stay: they are plain data and keep pinning the Go code to what
Node did.

## Running

In the dev image, from `betterdesk-server/console/mail/testdata`:

    tools/dev.sh "cd betterdesk-server/console/mail/testdata && node nodeoracle/mail.js && node nodeoracle/misc.js"
    DEV_DOCKER_ARGS="--network bdgo" tools/dev.sh "cd betterdesk-server/console/mail/testdata && \
      ORACLE_PG_DSN='postgres://betterdesk:bdgo-local@bdgo-testpg:5432/betterdesk_test?sslmode=disable' node nodeoracle/mail.js"

The scripts use fixed seeds, so a second run records the same steps. They
work in a scratch folder under the system's temporary folder (SQLite) or in
a schema of their own (PostgreSQL) and write nothing else outside
`testdata`. `mail.js` refuses to run when `web-nodejs/.env` exists, because
`config.js` would read it.

No mail is sent and nothing leaves the machine: see `probe.js` below.

## Scripts

`lib.js` opens the scratch database with the real `services/dbAdapter.js`,
and has the seeded random source, the capture of `console.log`, and `dump`,
which writes a value of any JavaScript type as text (JSON plus the words
`undefined`, `NaN`, `-0`, `Infinity`).

`mail.js` writes `node_sqlite.json.gz` or `node_postgres.json.gz`: 15,762
steps, replayed in order by `TestNodeRecording`. nodemailer is replaced by a
stub that records the options of `createTransport` and the message of
`sendMail`, and fails where the step says so. The steps:

| Step | What it does |
|---|---|
| `state` | stores a `smtp_config` row (or deletes it), sets the environment and the four values of `config.js` the service reads, resets the transporter |
| `env` | changes the environment without anything else |
| `load` | `loadSmtpConfig()` and `getAlertEmail()` |
| `get`, `test`, `put` | the three handlers, with their status, body, the row afterwards, the audit entry and what they logged |
| `send`, `alert`, `help` | `sendEmail`, `sendAlertEmail`, `sendHelpRequestEmail`: the answer, the log, the message handed to nodemailer |
| `baseurl` | `getPanelBaseUrl()` |
| `has`, `logsent`, `logcount` | the notification log of the database adapter |
| `reset` | `resetTransporter()` |

The inputs: about 1,000 stored configurations (every member with about 80
values of every JSON type, damaged and odd JSON, random mixes), 350 sets of
`SMTP_*` variables, messages with every form of recipient list, 875 alerts
and rules and 456 help requests with members of every type, and 1,317 saves
over 16 kinds of existing rows.

`probe.js` asks the real nodemailer what it does with a set of options,
without a mail server. While a probe runs, name lookups answer 127.0.0.1 and
connections are redirected to this machine's loopback address, where
nothing listens. The record says whether nodemailer got as far as opening a
connection and with which host, port, TLS and certificate setting, or which
error it failed with before, or which exception ended the process. Each
step that used a transporter carries the probe's answer for its options;
the Go tests take from it what the transport has to be given, and which
configurations fail without a connection.

`misc.js` writes `misc.json.gz`: `toUpperCase` for every code point it
changes (the alert mail's subject), the text Node makes of 3,000 byte
strings that are no UTF-8 (a stored row or a request body can hold such
bytes), and what `config/config.js` makes of `SMTP_TLS_VERIFY`,
`HTTP_REDIRECT_HTTPS`, `HOST`, `PORT` and `ALLOW_SELF_SIGNED_CERTS`, loaded
anew for each of 211 sets of values.
