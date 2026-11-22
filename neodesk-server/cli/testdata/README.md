# Recordings of the old console for cli

The gzipped JSON files here are fixed records of what the Node console's
scripts did, made while they still existed. The subcommands of package cli
replace those scripts. The recordings can't be made again. The Go tests
replay them and need no Node. Don't edit them: when a fix changes an
answer on purpose, the test lists that record as an intended difference
with the row id (for example `checkRunDifferences` in `check_test.go`).

| File | What it holds | Replayed by |
|---|---|---|
| `envmerge.json.gz` | what `web-nodejs/scripts/merge-env.js` made of env files | `envmerge_test.go` |
| `substfiles.json.gz` | what `write-installer-env-subst.js` wrote | `envmerge_test.go` |
| `resetpassword.json.gz` | runs of `web-nodejs/scripts/reset-password.js` on SQLite and PostgreSQL | `resetpassword_test.go` |
| `managementcli.json.gz` | `web-nodejs/scripts/management-cli.js` with `check` and `set` | `config_test.go` |
| `protocolcheck.json.gz` | `scripts/installer-protocol-check.js`: how it read its arguments, and whole runs against test servers | `check_test.go` |

## The certificates

`certs` holds the test CA and the server certificates the protocol check
ran against when it was recorded: good, for another host, expired,
self-signed and without subject alternative names. The recorded lines name
what these certificates hold (host names, expiry, self-signed), and
`check_test.go` starts its servers with the same files. Keep them as they
are. `gencerts` is the program that made them; don't run it again, since
the recording can't be made again to match new certificates.
