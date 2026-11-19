# Node oracle for the settings

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

`settings.js` loads the Node console's `config/config.js` with many
environments and writes what it makes of them to `../settings.json`. The
Go tests replay the file (`TestSettingsAsTheOldConsoleReadThem`).

It is a fixture: it stays now that the Node console is gone. Run it in the dev
container:

    tools/dev.sh "cd betterdesk-server/console/config/testdata/nodeoracle && node settings.js"
