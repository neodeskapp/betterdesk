# The Node console as the reference for `console/servicedef`

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

This folder is a test fixture. It stays now that `web-nodejs` is gone.

`sanitize.js` takes `sanitizeGoServerServiceConfig` and the helpers it
calls out of `web-nodejs/services/updateService.js` as source and runs
them with the system around them stubbed: the shell, nssm, the files and
the privileged daemon reload answer what each vector scripts, and every
command and write is recorded. Loading the whole updater would start too
much. It writes `../sanitize.json` (the text helpers with many inputs,
and whole checks on Linux and Windows), which `vectors_test.go` replays.

    tools/dev.sh "cd betterdesk-server/console/servicedef/testdata/nodeoracle && node sanitize.js"
