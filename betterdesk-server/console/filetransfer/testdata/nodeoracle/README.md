# The Node console as the reference for `console/filetransfer`

This folder is a test fixture. It goes away together with `web-nodejs`;
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
