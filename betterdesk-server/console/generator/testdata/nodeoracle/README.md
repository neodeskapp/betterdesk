# Node oracle for console/generator

These scripts load the Node services of the support client generator from
`web-nodejs` and write what they return as JSON vectors into `..`. The Go
tests of `console/generator` replay every vector. They are test fixtures
for the time both consoles exist: once the Node services are gone the
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
