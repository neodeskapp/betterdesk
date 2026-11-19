# Node oracle for package securityaudit

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

`securityaudit.js` ran the Node routes of
`web-nodejs/routes/security-audit.routes.js` through the console's real API
client against a made-up API server on a local port, and wrote what they
answered to `../securityaudit.json`. The Go tests replay that file.

The route file is deleted with the move to Go, so the script runs only on a
commit that still has it:

    cd /repo/web-nodejs && node ../betterdesk-server/console/routes/securityaudit/testdata/nodeoracle/securityaudit.js

The times in the recorded answers of the API server are relative to the
moment of the recording, which each scenario carries as `now`.

These files are fixtures. They stay now that the Node console is gone.
