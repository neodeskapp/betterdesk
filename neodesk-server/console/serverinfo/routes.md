# Routes that call this package

For the route step: every route of the Node console that calls one of the
sources this package ports, with the calls it makes. `s` is the
`*serverinfo.Service`, `r` the request. Line numbers are those of the Node
route file.

Names: the Node function on the left, the Go call on the right.

| Node | Go |
|---|---|
| `keyService.resolvePublicKey()` | `s.ResolvePublicKey(ctx)`: `NullString`, `""` for null |
| `keyService.getPublicKey()` | `s.GetPublicKey()` |
| `keyService.getApiKey(masked)` | `s.GetAPIKey(masked)`: `(key, ok)`, `ok` false for null |
| `keyService.getServerConfigQR(endpoints)` | `s.GetServerConfigQR(ctx, endpoints.Endpoints())` |
| `keyService.getPublicKeyQR()` | `s.GetPublicKeyQR(ctx)` |
| `keyService.getClientConfig(endpoints)` | `s.GetClientConfig(ctx, endpoints.Endpoints())` |
| `keyService.getServerConfig()` | `s.GetServerConfig(ctx)` |
| `keyService.buildRustDeskConfigPayload(x, opts)` | `s.BuildRustDeskConfigPayload(ep, opts)`; a bare host is `Endpoints{Host: host}` |
| `keyService.buildRustDeskConfigPayloadAsync(x, opts)` | `s.ResolveRustDeskConfigPayload(ctx, ep, opts)` |
| `keyService.encodeRustDeskCliConfigString(p)`, `encodeRustDeskConfigUri(p)` | `EncodeRustDeskCliConfigString(p)`, `EncodeRustDeskConfigURI(p)` |
| `keyService.apiUrlForHost(host, https)`, `normalizeHostInput(h)`, `isValidRustDeskPublicKey(k)` | `s.APIURLForHost(host, https)`, `NormalizeHostInput(h)`, `IsValidRustDeskPublicKey(k)` |
| `clientConfigHost.resolveRustDeskEndpoints(req, queryHost)` | `s.ResolveRustDeskEndpoints(RequestHostOf(r), queryHost)` |
| `clientConfigHost.resolveClientFacingHost(req, queryHost)` | `s.ResolveClientFacingHost(RequestHostOf(r), queryHost)`; `nil` where Node passed no request |
| `clientConfigHost.isPhoneUnreachableHost(h)`, `stripRequestHost(h)`, `readPanelPublicHost()` | `IsPhoneUnreachableHost(h)`, `StripRequestHost(h)`, `s.ReadPanelPublicHost()` |
| `conn.normalizeServerHost(h)`, `defaultServerHost()`, `defaultUseHttps()`, `defaultApiPort()` | `NormalizeServerHost(h)`, `s.DefaultServerHost()`, `s.DefaultUseHTTPS()`, `s.DefaultAPIPort()` |
| `rustDeskPublicEndpoints.getPublicEndpointSettings()` | `s.GetPublicEndpointSettings()` |
| `rustDeskPublicEndpoints.isEnvOverrideActive(settings)` | `IsEnvOverrideActive(settings)` |
| `rustDeskPublicEndpoints.savePublicEndpointSettings(body fields)` | `s.SavePublicEndpointSettings(PublicEndpointsInput{...})`, fields through `Field(body, key)` |
| `serverConnectionConfig.getConnectionMode()` | `s.GetConnectionMode(ctx)` |
| `serverConnectionConfig.detectDeploymentSource()` | `s.DetectDeploymentSource()` |
| `serverConnectionConfig.setConnectionMode(body fields)` | `s.SetConnectionMode(ctx, ConnectionInput{...})`, fields through `Field(body, key)`; the answer is `write.Object()` |
| `serverConnectionConfig.restartServer()` | `s.RestartServer(ctx)` |
| `serverConnectionConfig.isDockerSplitDeployment()` | `s.IsDockerSplitDeployment()` |
| `upsertEnvKey`, `parseEnvLines`, `mergeEnv`, `mergeEnvFile`, `buildEnvSubstitutions`, `applySubstitutions` | `UpsertEnvKey`, `ParseEnvLines`, `MergeEnv`, `MergeEnvFile`, `BuildEnvSubstitutions`, `ApplySubstitutions` |
| `readProductVersion`, `readEmbeddedServerVersion`, `readPackageJsonVersion` | `ReadProductVersion`, `ReadEmbeddedServerVersion`, `ReadPackageJSONVersion` |

A query parameter counts only when it is a string: `typeof req.query.host
=== 'string' ? req.query.host : ''`. `?host=a&host=b` and `?host[x]=1` are
not strings in Express and give `''`.

## keys (`routes/keys.routes.js`, 6 routes, all `requireAuth`)

| Route | Calls | Answer |
|---|---|---|
| `GET /api/keys/public` (42) | `ResolvePublicKey` | 404 `keys.not_found` when `""`, else `{success, data: {key}}` |
| `GET /api/keys/public/qr` (73) | `ResolveRustDeskEndpoints(req, query host)`, `GetServerConfigQR` | 404 `keys.not_found` when the QR is `""`; else `data: {qr, server_id: host, relay_server: relay, api_url: api, phone_unreachable_host: IsPhoneUnreachableHost(host)}` |
| `GET /api/keys/public/download` (101) | `ResolvePublicKey` | 404 as above; else the key as `text/plain` with `Content-Disposition: attachment; filename="id_ed25519.pub"` |
| `GET /api/keys/api` (128) | `GetAPIKey(!show)` with `show = query.show === 'true'` | 403 `Permission denied: server.keys` when `show` and the request lacks `server.keys` (checked before the read); 404 `keys.api_not_found` when `ok` is false or the key is `""`; else `data: {key, masked: !show}` |
| `GET /api/keys/server-info` (162) | `ResolveRustDeskEndpoints(req, query host)`, `GetAPIKey(true)` | `data: {server_id, relay_server, api_url, api_key_masked, endpoint_sources: sources, env_override_active}`; `api_key_masked` is eight U+2022 bullets when `ok` is false or the key is `""` |
| `GET /api/keys/qr` (180) | same as `/api/keys/public/qr` | same |

Every handler answers 500 `errors.server_error` when something throws. None
of the Go calls returns an error, so that branch is left for a panic.

Jest tests of these routes, for the route step: `tests/sensitiveEndpoints.test.js`
(the full key needs `server.keys`, the masked one does not),
`tests/clientsPages.routes.test.js`, `tests/settingsSystemTabs.routes.test.js`.

## dashboard (`routes/dashboard.routes.js`, 6 routes)

| Route | Calls |
|---|---|
| `GET /api/stats` (17) | `ResolvePublicKey`: `publicKey: key != ""`. The rest is `serverBackend` |
| `GET /api/dashboard/client-config` (137) | `ResolveRustDeskEndpoints(req, query host)`, `GetClientConfig`, `GetServerConfigQR`. `data` is the client config's seven fields, then `client_server_host: host`, `endpoint_sources`, `env_override_active`, `phone_unreachable_host`, `qr` (null without a key) |
| `GET /api/server/status` (52) | none of this package. It reads `config.neodeskApiUrl`, `goApiPort`, `apiPort`, `apiEnabled`, `port`, `wsProxy` |
| `GET /api/server/bandwidth` (167), `POST /api/sync-status` (194), `GET /api/dashboard/activity` (216) | none of this package |

`GetClientConfig` and `GetServerConfigQR` each resolve the key, as in Node.
With no valid key file the first call asks the server. The second uses the
answer kept for 30 seconds when the server gave a key, and asks again when
it gave none: a route can then wait for two requests of up to 5 seconds.

## pairing (`routes/pairing.routes.js`, 4 routes)

| Route | Calls |
|---|---|
| `GET /api/panel/pairing/host` (43) | none. Node asks `rustDeskPublicEndpointsService.getPublicEndpoints`, which does not exist, so the answer is always `{success: true, host: ""}` (BUGS SI1). Keep `""` until the bug is fixed; the fix is `s.GetPublicEndpointSettings()` and `PublicServerID` or `PublicAPIURL` |
| `GET /api/panel/pairing/codes` (48), `POST` (62), `DELETE /api/panel/pairing/codes/:id` (97) | none of this package (`goapi` and the audit log) |

## settings (`routes/settings.routes.js`)

| Route | Calls |
|---|---|
| `GET /api/settings/info` (117) | `GetServerConfig`: Node does not wait for it and does not use it. The answer needs `config.appVersion`, which is `ReadProductVersion(fs, rootDir, consoleDir, "0.0.0")` at start, and the paths `dbPath`, `pubKeyPath`, `apiKeyPath` |
| `GET /api/settings/server-info` (161) | `ResolveRustDeskEndpoints(req, "")` (no query host), `GetAPIKey(true)`; same `data` as `/api/keys/server-info` |
| `PUT /api/settings/client-sessions` (2035) | `UpsertEnvKey(content, "API_TOKEN_EXPIRY_DAYS", days)` on the console's `.env` |
| `GET /api/settings/connection-mode` (2275) | `GetConnectionMode`, then `GET /health` on the server (5 s) for `runtime: health.connection or null`, `DetectDeploymentSource`. `data` is the settings' eight fields, then `deployment`, `runtime`. An error of `GetConnectionMode` is a 500 with its message |
| `PUT /api/settings/connection-mode` (2302) | `GetConnectionMode` (for the rollback), `SetConnectionMode`. `data` is `write.Object()` with `Set("restartRequired", pending)`; `pending` comes from the restart coordinator unless `write.Source` is `go-api`. Rollback descriptor: `previous.ConnectionMode`, later replayed with `SetConnectionMode(ctx, mode.Input())`. Audit text: `Connection mode set to <mode>, logged_in_only_initiator=<Y or N>, operator_only_outbound=<Y or N> (<source>)` from `write.Settings`. Errors: `ErrNotConfigurable` is 400 `settings.connection_mode_not_configurable`, `ErrComposeServerNotFound` is 400 `settings.connection_mode_compose_error`, anything else 500 with the error's message |
| `POST /api/settings/connection-mode/restart` (2367) | through the restart coordinator: `RestartServer` |
| `GET /api/settings/public-endpoints` (2388) | `GetPublicEndpointSettings`, `IsEnvOverrideActive`; `data` is the three settings, then `env_override_active` |
| `PUT /api/settings/public-endpoints` (2407) | `SavePublicEndpointSettings`, `IsEnvOverrideActive`. Audit text: `RustDesk public endpoints updated (ID=<id or ->, relay=<relay or ->, api=<api or ->)`. `ErrInvalidPublicHost` is 400 `settings.public_endpoints_invalid_host`, `ErrInvalidPublicAPIURL` is 400 `settings.public_endpoints_invalid_api_url`, anything else 500 with the error's message |

## Other groups

| Group | Route or caller | Calls |
|---|---|---|
| generator | `GET /api/generator/defaults` (404) | `ResolveClientFacingHost(req, "")`, else `DefaultServerHost`; `ResolvePublicKey` |
| generator | `POST /api/generator/bundles`, `PUT /api/generator/bundles/:bundleId` (481), `POST /api/generator/preview` (667), through `finalizeSupportBranding` (190) | `NormalizeServerHost`, `ResolveClientFacingHost(nil, "")`, `DefaultServerHost`, `DefaultAPIPort`, `ResolvePublicKey` |
| generator | `GET /api/generator/config` (829), `POST /api/generator/generate-config` (846) | `ResolvePublicKey` |
| registration | `PUT /api/registrations/:id/approve` (295, also reached as `/api/nd/api/registrations/:id/approve`), through `buildServerConfig` (81) | `ResolvePublicKey` (`""` on any failure) |
| remote | `GET /api/remote/viewer/:deviceId` (369), through `resolveViewer` | `ResolvePublicKey`; `GetConnectionMode` when the server's health has no `connection` |
| rustdesk-api | `GET /api/server-key` (1624), `GET /api/server-key/fingerprint` (1639), on the console port and on the client API port in legacy mode | `ResolvePublicKey` |
| commercialization | billing clock settings (`services/billingClockConfigService.js`) | `UpsertEnvKey` five times on the console's `.env`, `RestartServer`, `IsDockerSplitDeployment` |
| restart (`services/restartCoordinator.js`) | rollback and confirm | `SetConnectionMode(ctx, mode.Input())`, `RestartServer` |
| updater (`services/updateService.js`) | channel, web app folder, env merge, build | `UpsertEnvKey`, `ParseEnvLines`, `MergeEnvFile`, `BuildEnvSubstitutions`, `ReadProductVersion`, `ReadEmbeddedServerVersion` |
| generator worker (`services/clientTemplateWorker.js`) | support client build | `ResolvePublicKey`; `services/customTxtBuilder.js`, which is not in this package |
| installers | `scripts/merge-env.js`, called by `neodesk.sh` | `MergeEnvFile`; needs a command of the server binary when Node goes |
| config (`config/config.js`) | start | `ReadProductVersion(fs, projectRoot, consoleDir, "0.0.0")` for the app version |
