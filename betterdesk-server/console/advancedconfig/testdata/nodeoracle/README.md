# The Node console as the reference for `console/advancedconfig`

This folder is a test fixture. It goes away together with `web-nodejs`;
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
