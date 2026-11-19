# The Node console as the reference for `console/advancedconfig`

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

`advancedconfig.js` loads the real `services/advancedConfigService.js` from
`/repo/web-nodejs` and runs it through scenarios on a scratch folder:
listing, reading and writing every file of the catalog in every state
(missing, a folder, too large, binary, broken UTF-8, behind a symbolic
link, below a file), writing every kind of content, and restoring backups
with good and hostile backup paths. Each scenario gets a copy of the
module in `<root>/web-nodejs/services` with a `config.js` that points the
data and keys folders into the scratch folder, so nothing outside it is
touched. The clock moves one second per `new Date()`, so the names of the
backups are known.

For every step the vectors hold what the function returned or the message
it threw, and the files of the scratch folder afterwards (type, mode,
size, SHA-256). The Go test (`vectors_test.go`) runs the same scenarios on
a temporary folder and compares all of it.

Run it in the dev image, from the notes folder:

    tools/dev.sh "cd betterdesk-server/console/advancedconfig/testdata/nodeoracle && node advancedconfig.js"

Both must run as the same user: as root every file can be written, so
"not_writable" does not occur in the vectors; `advancedconfig_test.go`
covers it.
