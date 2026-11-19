# Node oracles of package cli

These scripts are test fixtures that go away with `web-nodejs`. They run
the old console's scripts that the subcommands replace and record what
they did, as gzipped JSON in `..`, which the Go tests replay:

| Script | Runs | Writes | Replayed by |
|---|---|---|---|
| `envmerge.js` | `web-nodejs/scripts/merge-env.js`, `write-installer-env-subst.js` | `envmerge.json.gz`, `substfiles.json.gz` | `envmerge_test.go` |
| `resetpassword.js` | `web-nodejs/scripts/reset-password.js` on SQLite and PostgreSQL | `resetpassword.json.gz` | `resetpassword_test.go` |
| `managementcli.js` | `web-nodejs/scripts/management-cli.js` (`check`, `set`) | `managementcli.json.gz` | `config_test.go` |
| `protocolcheck.js` | `scripts/installer-protocol-check.js`, with servers it starts | `protocolcheck.json.gz` | `check_test.go` |

Run them in the dev image, the worktree at `/repo`; `resetpassword.js`
needs the test PostgreSQL for its PostgreSQL runs:

    tools/dev.sh "cd betterdesk-server/cli/testdata/nodeoracle && node envmerge.js && node managementcli.js && node protocolcheck.js"
    DEV_DOCKER_ARGS="--network bdgo -e BETTERDESK_TEST_POSTGRES_DSN=..." tools/dev.sh \
        "cd betterdesk-server/cli/testdata/nodeoracle && node resetpassword.js"

The certificates in `../certs` are for these tests only; `go run
./cli/testdata/gencerts` makes them again (then record `protocolcheck.js`
again too).
