# Node oracle for console/generator

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

These scripts load the Node services of the support client generator from
`web-nodejs` and write what they return as JSON vectors into `..`. The Go
tests of `console/generator` replay every vector. They are test fixtures
from the time both consoles existed: once the Node services are gone the
scripts cannot run any more, and the recorded files stay as plain data.

- `vectors.js`: `agentBundleService` (profiles, hashes, slugs),
  `lib/generatorBuildTypes`, `customTxtBuilder` (the JSON of custom.txt,
  the signed bytes, Node's base64 reading of seeds, the Ed25519 public key
  of a seed), the installer scripts, generation keys, the channel values
  of the download page's contrast rule, the URL rules, JSON text.
- `worker.js`: the zip adm-zip writes for a folder, the build capabilities
  and platform rules of embedded mode for provider settings, the checks of
  a provider's manifest.

Every seed in them is made up for the test.

    tools/dev.sh "cd betterdesk-server/console/generator/testdata && node nodeoracle/vectors.js && node nodeoracle/worker.js"
