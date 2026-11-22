# Recordings of the old console for console/serverinfo

The files below are fixed records of what the Node console's own modules
answered on Node 22, made while the console still existed. They can't be
recorded again. The Go tests replay them and need no Node. Don't edit
them: when a fix changes an answer on purpose, the test lists that record
as an intended difference with the row id.

Each `*.json.gz` file holds lists of records: an input and what the Node
function answered for it. `loadVectors` in `helpers_test.go` reads them;
the tests in `keys_test.go`, `host_test.go`, `jsurl_test.go`,
`envmerge_test.go`, `endpoints_test.go`, `connection_test.go` and
`idna_test.go` replay them. `counts.json` has the number of records per
list; no test reads it.

No database was involved: these modules read and write files and call the
Go server, nothing else. The recordings used fixed seeds and a scratch
folder.

## keys.json.gz, hosts.json.gz, urls.json.gz

From `services/keyService.js`, `services/agentBundleConnection.js` and
`services/clientConfigHost.js`. The Go API client was a fake whose answers
each step set, and `QRCode.toDataURL` was replaced by a function that
recorded its text and options.

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

## envmerge.json.gz, endpoints.json.gz, resolve.json.gz

From `lib/envMerge.js`, `services/rustDeskPublicEndpointsService.js` and
`services/clientConfigHost.js`. The public endpoints service was pointed
at files in the scratch folder. Requests were objects made from Express's
request prototype, with `trust proxy` on and off.

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

## connection.json.gz

From `services/serverConnectionConfigService.js`. The Go API client,
`services/updateService.js` and `lib/privilegedUpdateHelper.js` were
fakes. The service works on four fixed paths (the systemd unit, its
drop-in, `docker-compose.yml` beside the console, `/.dockerenv`); the
recording redirected `fs.existsSync`, `fs.promises.readFile` and
`fs.promises.writeFile` for exactly those into the scratch folder.

| List | What it holds |
|---|---|
| `parse_systemd`, `patch_systemd`, `parse_compose`, `patch_compose` | the four text functions on about 700 generated units and 700 compose files |
| `settings_from_env`, `env_vars_from_settings`, `settings_from_api` | the converters, with every kind of JSON value as input |
| `service` | `isDockerSplitDeployment`, `detectDeploymentSource`, `getConnectionMode`, `setConnectionMode` (as the JSON a route answers), a read after the write, and `restartServer`, per layout of files, environment, privileged helper and server answers; with the requests sent and the files afterwards |
| `shell` | the message of the error `child_process.execSync` throws for a few harmless commands |

## idna.json.gz

`new URL('http://<name>/').hostname` for a label with one character
outside ASCII: every such character up to U+1FFFF and every 257th beyond,
once between two letters and once at the start of the label, plus a list
of whole names.
