# Node oracle for package trends

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

`trends.js` ran the Node route `web-nodejs/routes/trends.routes.js` with a
made-up API client and wrote what it answered to `../trends.json`. The Go
tests replay that file.

The route file is deleted with the move to Go, so the script runs only on a
commit that still has it:

    cd /repo/web-nodejs && node ../betterdesk-server/console/routes/trends/testdata/nodeoracle/trends.js

These files are fixtures. They stay now that the Node console is gone.
