# Node oracle for console/routes/avatars

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

`routes.js` runs the Node console's `routes/avatars.routes.js` on a scratch
SQLite database that holds pictures, sends it 223 requests and records
status, headers and body of every answer in `../routes.json.gz`. The Go
test `TestOracleRoutes` replays them.

The contract suite cannot do this: a test instance has no pictures, and
only a single sign-on or a fetch from another host stores one.

The rows cover what the route can meet: PNG, GIF, WebP and JPEG, a type in
upper case, a type the console does not serve, a row without bytes, a row
without a type, versions that are short, empty or hold a space. The
requests cover ids in every form, `?v=` as text, list and object,
`If-None-Match` in every form, HEAD, and a request without a session.

This folder is a test fixture from the time both servers existed. The script
needs `web-nodejs` with the route file in it, which is gone since the route
moved to Go; the recorded file stays and keeps pinning the Go route to what
Node answered.

It was run in the dev image:

    tools/dev.sh "cd betterdesk-server/console/routes/avatars/testdata && node nodeoracle/routes.js"
