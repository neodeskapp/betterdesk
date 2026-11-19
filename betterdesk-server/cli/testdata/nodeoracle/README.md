# Node oracles of package cli

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

These scripts are test fixtures that stay now that `web-nodejs` is gone. They run
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
