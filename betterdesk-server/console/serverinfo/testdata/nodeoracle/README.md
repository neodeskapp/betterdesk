# Node oracle for console/serverinfo

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

These scripts run the console's Node modules (`web-nodejs`) and record what
they answer. The Go tests in `console/serverinfo` replay every record and
must answer the same.

This folder is a test fixture from the time both servers existed. It needs
`web-nodejs`, which this tree no longer has. The recorded files in the parent folder
(`*.json.gz`, `counts.json`) stay: they are plain data and keep pinning the
Go code to what Node did.

## Running

In the dev container, from this folder:

    node keys.js && node envfiles.js && node connection.js && node idna.js

Each script uses a fixed seed, so a second run writes the same files. They
work in a scratch folder under the system's temporary folder and write
nothing else outside `testdata`.

## Scripts

`lib.js` has the seeded random source, the writer of the vector files, and
`fakeModule`, which puts a fake in place of a module of the console before
anything loads it.

`keys.js` loads `services/keyService.js`, `services/agentBundleConnection.js`
and `services/clientConfigHost.js`. The Go API client is a fake whose
answers the script sets, and `QRCode.toDataURL` is replaced by a function
that records its text and options. It writes:

| File | List | What it holds |
|---|---|---|
| `keys` | `valid_key` | `isValidRustDeskPublicKey` for valid keys, padding variants, white space, placeholders and damaged keys |
| | `api_url_for_host` | `apiUrlForHost` over API ports, client API ports, hosts and both schemes |
| | `payload` | `buildRustDeskConfigPayload`, `encodeRustDeskCliConfigString` and `encodeRustDeskConfigUri` for about 8,000 combinations of configuration, key file, host (bare or as an object with relay and API address) and options |
| | `key_files` | `getPublicKey` and `getApiKey` on files: missing, empty, valid, with a byte order mark, not UTF-8, a directory |
| | `api_key_half_characters` | masked API keys whose cut falls inside a character outside the Basic Multilingual Plane |
| | `runs` | scripted runs of `resolvePublicKey`, `getClientConfig`, `getServerConfigQR`, `getPublicKeyQR`, `getServerConfig` and `buildRustDeskConfigPayloadAsync` with a clock, a key file and server answers that change from step to step, and the requests and QR texts each step caused |
| | `qr_options` | the options the QR codes were drawn with |
| `hosts` | | `normalizeServerHost`, `normalizeHostInput`, `stripRequestHost`, `isPhoneUnreachableHost` for about 2,400 hosts each, and `defaultServerHost`, `defaultUseHttps`, `defaultApiPort` per API address |
| `urls` | `parse` | protocol, hostname, port, pathname and search of `new URL(text)`, or that it throws |

`envfiles.js` loads `lib/envMerge.js`,
`services/rustDeskPublicEndpointsService.js` and
`services/clientConfigHost.js`. The public endpoints service is pointed at
files in the scratch folder with its own `_setPathsForTests`. Requests are
objects made from Express's request prototype, with `trust proxy` on and
off. It writes:

| File | List | What it holds |
|---|---|---|
| `envmerge` | `parse_env_lines`, `parse_env_file`, `build_fresh` | the parsers on about 700 env texts: comments, blank lines, quotes, `export`, duplicates, CRLF, lone CR, no last line break, values with `=`, `#`, `$` and spaces |
| | `upsert` | `upsertEnvKey`: one to four updates in a row on each text, with the text after each |
| | `merge` | `mergeEnv` for pairs of texts |
| | `substitute`, `assert_resolved`, `build_substitutions` | `applySubstitutions`, `assertResolvedSubstitutions`, and `buildEnvSubstitutions` with fixed random bytes |
| | `merge_file` | `mergeEnvFile` on files, with the target's bytes and mode afterwards |
| `endpoints` | `normalize` | `normalizeSettings` and `validateSettings` for strings and every other kind of JSON value |
| | `read` | `readPublicEndpointEnv`, `readPanelPublicHostValue` and `getPublicEndpointSettings` for combinations of environment, durable file and console env file, with the files afterwards (the one-time copy) |
| | `write` | `writePublicEndpointSettingsToEnv` on existing files, with the files, their modes and the environment afterwards |
| `resolve` | `resolve` | `resolveRustDeskEndpoints`, `resolveClientFacingHost` and `readPanelPublicHost` over API addresses, ports, public endpoint settings, files, request headers and query hosts |

`connection.js` loads `services/serverConnectionConfigService.js` and
`lib/productVersion.js`. The Go API client, `services/updateService.js` and
`lib/privilegedUpdateHelper.js` are fakes. The service works on four fixed
paths (the systemd unit, its drop-in, `docker-compose.yml` beside the
console, `/.dockerenv`); the script redirects `fs.existsSync`,
`fs.promises.readFile` and `fs.promises.writeFile` for exactly those into
the scratch folder. It writes:

| File | List | What it holds |
|---|---|---|
| `connection` | `parse_systemd`, `patch_systemd`, `parse_compose`, `patch_compose` | the four text functions on about 700 generated units and 700 compose files |
| | `settings_from_env`, `env_vars_from_settings`, `settings_from_api` | the converters, with every kind of JSON value as input |
| | `service` | `isDockerSplitDeployment`, `detectDeploymentSource`, `getConnectionMode`, `setConnectionMode` (as the JSON a route answers), a read after the write, and `restartServer`, per layout of files, environment, privileged helper and server answers; with the requests sent and the files afterwards |
| | `shell` | the message of the error `child_process.execSync` throws for a few harmless commands |
| `version` | `read` | `readProductVersion`, `readPackageJsonVersion` and `readEmbeddedServerVersion` on generated folders. Nothing replays it any more: the console reports the version built into the binary, and the Go readers are gone |

`idna.js` records `new URL('http://<name>/').hostname` for a label with one
character outside ASCII: every such character up to U+1FFFF and every 257th
beyond, once between two letters and once at the start of the label, plus a
list of whole names. It writes `idna`.

`counts.json` has the number of records per list.

No database is involved: these modules read and write files and call the
Go server, nothing else.
