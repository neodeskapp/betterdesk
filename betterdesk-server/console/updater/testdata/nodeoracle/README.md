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

These scripts are fixtures that stay now that the Node console is gone. They load
upstream's signed update manifest module (`web-nodejs/lib/updateManifestSignature.js`
of upstream dev; its path is the first argument, since this branch does
not have the file yet) and write the vectors the Go tests replay:

    node manifest-signature.js <path of updateManifestSignature.js> ../manifest_signature_vectors.json

`manifest_signature_vectors.json` holds manifests, the canonical text
upstream builds from each, the signature of that text by a fixed test
key, and what upstream's verifier answers for good, foreign, changed and
malformed signatures and keys. The test key is made for these vectors and
signs nothing else.
