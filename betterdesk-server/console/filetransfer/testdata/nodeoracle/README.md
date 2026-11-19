# The Node console as the reference for `console/filetransfer`

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

`filetransfer.js` loads the real `services/fileTransferService.js` (from a
copy in a scratch folder, because the service keeps its files next to its
own code) and runs it through scripted lives of transfers: new transfers
with every kind of value, chunks as base64 text and in every other shape
a device could send, cancels, the lists with filters, the messages of a
device, and the housekeeping with a clock the script moves. Per step it
records what the function returned or threw, every record afterwards and
the files in the transfers folder with their hashes. The clock moves one
millisecond per reading and the random bytes count up, so the Go test
(`vectors_test.go`) gives its service the same and gets the same ids and
times. The file also holds `parseInt(size, 10) || 0` for many values,
which the Go test replays against `ParseSize`.

Run it in the dev image, from the notes folder:

    tools/dev.sh "cd betterdesk-server/console/filetransfer/testdata/nodeoracle && node filetransfer.js"
