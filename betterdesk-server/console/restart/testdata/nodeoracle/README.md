# The Node console as the reference for `console/restart`

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

This folder is a test fixture. It stays now that `web-nodejs` is gone;
`../vectors.json.gz` stays as the record of what the Node console did.

`restart.js` loads the real `services/restartCoordinator.js` from
`/repo/web-nodejs` and runs it through scripted lives of a restart
transaction: registering changes, cancel, confirm, the public status after
a new start of the process, complete, dismiss, and the same calls on
transaction files that were edited by hand or are no JSON. The services
the coordinator calls (the restart of the server, the rollbacks, the API
client) are fakes whose answers each step sets. The clock moves one second
per reading and the random bytes count up, so the Go test
(`vectors_test.go`) gives its service the same and compares, per step, the
result or the error, the transaction file byte for byte, the calls made
and whether the end of the process was scheduled.

Run it in the dev image, from the notes folder:

    tools/dev.sh "cd betterdesk-server/console/restart/testdata/nodeoracle && node restart.js"

It uses a fixed seed, works in a scratch folder under the system's
temporary folder and restarts nothing.
