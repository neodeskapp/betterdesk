# Node oracle for package jsreq

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

`query.js` asks the real Express query parser and JavaScript what they make
of query strings, JSON values and path segments, and writes the answers to
`../query.json`.

`dates.js` asks JavaScript, in the time zone America/New_York, what
`Date.parse`, `new Date` and `JSON.stringify` make of texts and JSON
values, and writes the answers to `../dates.json`. Its list `loose` holds
forms `Date.parse` reads that the Go port does not; the tests leave them
out.

The Go tests replay both files.

    cd /repo/web-nodejs && node ../betterdesk-server/console/routes/internal/jsreq/testdata/nodeoracle/query.js
    cd /repo/web-nodejs && node ../betterdesk-server/console/routes/internal/jsreq/testdata/nodeoracle/dates.js

These files are fixtures. They stay now that the Node console is gone.
