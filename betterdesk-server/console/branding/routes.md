<!--
Routes that call this package. From inventory.json: every route whose
calls.services names brandingService, fontService or supportersService.
Method, path, file and line of the Node route.

GET    /api/bd/server-info                               routes/bd-api.routes.js:161
GET    /api/bd/branding                                  routes/bd-api.routes.js:525
GET    /api/bd/appearance                                routes/bd-api.routes.js:550
GET    /api/auth/login-page                              routes/auth.routes.js:91
GET    /console-boot.js                                  routes/console.routes.js:187
GET    /api/console/bootstrap                            routes/console.routes.js:245
GET    /api/console/supporters                           routes/console.routes.js:315
GET    /api/settings/branding                            routes/settings.routes.js:319
GET    /api/settings/appearance                          routes/settings.routes.js:338
POST   /api/settings/branding                            routes/settings.routes.js:357
POST   /api/settings/branding/upload-logo                routes/settings.routes.js:418
POST   /api/settings/branding/upload-background          routes/settings.routes.js:519
POST   /api/settings/branding/reset                      routes/settings.routes.js:569
GET    /api/settings/branding/export                     routes/settings.routes.js:585
POST   /api/settings/branding/import                     routes/settings.routes.js:600
GET    /api/settings/branding/profiles                   routes/settings.routes.js:623
POST   /api/settings/branding/profiles                   routes/settings.routes.js:636
PUT    /api/settings/branding/profiles/:id               routes/settings.routes.js:660
POST   /api/settings/branding/profiles/:id/apply         routes/settings.routes.js:682
POST   /api/settings/branding/profiles/:id/duplicate     routes/settings.routes.js:703
DELETE /api/settings/branding/profiles/:id               routes/settings.routes.js:720
POST   /api/settings/themes/:id/apply                    routes/settings.routes.js:772
GET    /api/settings/fonts                               routes/settings.routes.js:810
GET    /api/settings/fonts/local                         routes/settings.routes.js:828
POST   /api/settings/fonts/download                      routes/settings.routes.js:842
DELETE /api/settings/fonts/:family                       routes/settings.routes.js:863
POST   /api/settings/fonts/upload                        routes/settings.routes.js:895
GET    /css/branding.css                                 routes/settings.routes.js:925
GET    /css/theme.css                                    routes/settings.routes.js:941
GET    /branding/favicon.svg                             routes/settings.routes.js:956
GET    /api/d/:publicId/page                             routes/generator.routes.js:754
GET    /api/remote/login-info                            routes/remote.routes.js:191
GET    /api/remote/guest-info                            routes/remote.routes.js:208
GET    /api/server-attestation/badge-config              routes/server-attestation.routes.js:68
PUT    /api/server-attestation/badge-config              routes/server-attestation.routes.js:81
GET    /api/public/server-attestation                    routes/server-attestation.routes.js:107

Two more routes use what this package ported although the inventory
lists no service for them (their code was in the route file):

GET    /api/settings/branding/backgrounds                routes/settings.routes.js:484
GET    /api/settings/themes                              routes/settings.routes.js:738

Not this package: GET and POST /api/client-branding
(routes/client-branding.routes.js) edit the server's own client branding
through the Go API, see storage.md.
-->

# Routes that call `console/branding`

`s` is the `*branding.Service`. `b := s.GetBranding()` is the cached
settings object; `b.Text("appName")` reads a setting as text. Everything
the functions return can go into `httpx.WriteJSON` as it is: the objects
write their keys in the order Node sent them.

Request bodies have to be read with `branding.ParseJSON` and handed on as
they are. The cleaning rules depend on what a client really sent (a
number, a list, a repeated key), which `encoding/json` into Go types
loses. For what the routes did with body values themselves there are
`branding.Field(body, "name")` (`body?.name`), `branding.Truthy`,
`branding.ToString` (`String(x)`) and `branding.Trim`. `branding.Message(err)`
is the text a route sent as `err.message`.

## Read-only users of the settings

| Route | Node | Go |
|---|---|---|
| `GET /api/bd/server-info` | bd-api.routes.js:161 | `b.Text("appName")` (or the configured app name when empty), `s.Revision()` |
| `GET /api/bd/branding` | :525 | `b`, `s.GetPublicAppearance()`. `company_name` is appName or "BetterDesk", `accent_color` is `colors.accentBlue` or "#3b82f6", `support_contact` is always "" (no such setting). An error of GetPublicAppearance means the three-field fallback answer |
| `GET /api/bd/appearance` | :550 | `s.GetPublicAppearance()`; on error the fixed fallback object of the route (revision "fallback") |
| `GET /api/auth/login-page` | auth.routes.js:91 | `appName`: `s.AppName(config app name)`, and "BetterDesk" when that is empty too. `branding`: `s.SignInBranding()`, the whole object as the route sent it. The capacity badge (`attestationSummary`, auth.routes.js:51) takes `s.GetBranding().Text("appName")` as the brand name |
| `GET /console-boot.js` | console.routes.js:94 (`publicBoot`), :187 | `themeMode`: `s.ThemeMode()`, `palettes`: `s.BootPalettes()`, `brandingRevision`: `s.Revision()` |
| `GET /api/console/bootstrap` | :245 | `branding`: `s.BootstrapBranding(config app name)`, `palettes`: `s.BootPalettes()` |
| `GET /api/console/supporters` | :315 | `{success: true}` plus the three keys of `s.LoadSupporters()` (honorary, individuals, links) |
| `GET /api/d/:publicId/page` | generator.routes.js:754 | `b` (appName, logoType, logoUrl, agentShowPoweredBy), `branding.BuildBackgroundValue(agentBgType, agentBgColor, agentBgGradient, agentBgImageUrl)` |
| `GET /api/remote/login-info`, `guest-info` | remote.routes.js:191, :208 | `b`: appName, logoType, logoIcon, logoSvg, logoUrl, logoText, logoTextAccent |
| `/api/server-attestation/badge-config` (GET, PUT), `GET /api/public/server-attestation` | server-attestation.routes.js:68, :81, :107 | `b.Text("appName")` as the brand name |
| `GET /css/branding.css` | settings.routes.js:925 | `s.GenerateThemeCSS()`. `Content-Type: text/css; charset=utf-8`, `Cache-Control: public, max-age=60`, `X-Branding-Revision: s.Revision()`. On error: 200 with the body `/* branding error */`, the content type, and neither of the other two headers |
| `GET /css/theme.css` | :941 | the same without `X-Branding-Revision`; on error `/* theme error */` |
| `GET /branding/favicon.svg` | :956 | `s.GenerateFavicon()`, `Content-Type: image/svg+xml`, `Cache-Control: public, max-age=300` |

At start, after the database is ready: `s.LoadBranding(ctx)` (server.js:432).

## Settings

| Route | Guard | Go |
|---|---|---|
| `GET /api/settings/branding` | signed in | `{success, data: b, appearance: s.GetAppearanceModel(b), readability: s.AssessAppearanceReadability(b), revision: s.Revision()}` |
| `GET /api/settings/appearance` | signed in | `{success, data: model, flat: b, readability, revision}` |
| `POST /api/settings/branding` | branding.edit | Body that is no object or list: 400 "Invalid branding data". `s.SaveBranding(ctx, body)`; any error: 500 server error. Answer as GET. Audit: `theme_toggle` "Theme switched to <themeMode>" when the body has at least one key, every key is one of themeMode, colors, glassColor and themeMode is not empty; otherwise `branding_update` "Updated branding configuration" |
| `POST .../branding/reset` | branding.edit | `s.ResetBranding(ctx)`, audit `branding_reset`, `{success, message: "Branding reset to defaults"}` |
| `GET .../branding/export` | branding.edit | `s.ExportPreset()` as the whole body, `Content-Disposition: attachment; filename="betterdesk-theme.json"` |
| `POST .../branding/import` | branding.edit | `s.ImportPreset(ctx, body)`: false is 400 "Invalid theme preset file", an error 500; audit `branding_import` |
| `GET .../branding/profiles` | signed in | `s.ListProfiles(ctx)` as `data` |
| `POST .../branding/profiles` | branding.edit | name and description are `String(x \|\| '').trim()` of the body values; empty name: 400 "Profile name is required". When `body.branding` is an object the theme is `{version: "1.0", type: "betterdesk-theme", branding: body.branding}`, else nil. `s.CreateProfile(ctx, name, description, theme)`. Any error is 400: "Profile name already exists" when `branding.Message(err)` contains "UNIQUE" (SQLite only; PostgreSQL sends the database's message), else the message. Audit `branding_profile_create` "Created profile: <name>" |
| `PUT .../profiles/:id` | branding.edit | id is `parseInt`; not a number: 400 "Invalid profile id". `s.UpdateProfile(ctx, id, ProfileUpdate{Name: body.name, Description: body.description, Branding: body.branding})` with the raw body values (`branding.Undefined` for a missing key). Errors are 400 with `branding.Message(err)` |
| `POST .../profiles/:id/apply` | branding.edit | `s.ApplyProfile(ctx, id)`: false is 404 "Profile not found or invalid", an error 500. Answer `{success, data: b, revision}` |
| `POST .../profiles/:id/duplicate` | branding.edit | `s.DuplicateProfile(ctx, id, body.name or "")`; errors are 400 with the message; answer `{success, data: {id}}`. No audit entry |
| `DELETE .../profiles/:id` | branding.edit | `s.DeleteProfile(ctx, id)`; errors are 400 with the message. The answer is `{success: true}` also when there was no such profile |
| `GET /api/settings/themes` | signed in | `s.ListThemes()` as `data`; an error is 500 "Failed to list themes". Node lists in the folder's order, Go by file name |
| `POST /api/settings/themes/:id/apply` | branding.edit | `s.ApplyTheme(ctx, id)`: ThemeInvalidID 400 "Invalid theme id", ThemeNotFound 404 "Theme not found", ThemeInvalidFormat 400 "Invalid theme format", an error 500 "Failed to apply theme". Audit `theme_apply` "Applied theme: <id>", message `Theme "<id>" applied` |

Profile ids: `parseInt` accepts "12abc" as 12 and gives numbers beyond
int64 for long digit runs. Such an id matches no row on SQLite; on
PostgreSQL the database refuses it with `value "<n>" is out of range for
type integer`, which the service reproduces for ids beyond 32 bits.

## Uploads

Node read these with multer. What the functions here need is what multer
handed the route: the file name with folders cut off (the last part after
`/` or `\`) and read as Latin-1, and the content type the client named.
A part without a file name, or one named `..`, is no file for multer
(400 "No file provided"). A file in another field is 400 "Unexpected
field". Both are the route's business.

| Route | Field | Go |
|---|---|---|
| `POST .../branding/upload-logo` | `logo` | `branding.AcceptImageUpload(type)` false: 400 "Invalid file type". `s.StoreLogo(name, body)`: `ErrFileTooLarge` is 400 "File too large (max 2 MB)", other errors 400 with the message. Then `s.RemovePreviousLogo(stored)`, audit `branding_logo_upload` "Uploaded logo: <stored>", answer `{success, url: "/uploads/<stored>"}` |
| `POST .../branding/upload-background` | `background`, text field `target` | As the logo with `s.StoreBackground` and "(max 8 MB)". Then `s.ApplyBackground(ctx, trimmed target, url)`. Answer `{success, url, applied, data: settings or null, revision}` with `revision` only when applied. Audit `branding_bg_upload` "Uploaded and applied background: <stored> (<target>)" or "Uploaded background: <stored>" |
| `GET .../branding/backgrounds` | | `s.ListBackgrounds()` as `data`; an error is 500 |
| `POST /api/settings/fonts/upload` | `font`, text field `family` | `branding.AcceptFontUpload(name, type)` false: 400 "Invalid font file type". More than `FontMaxBytes`: 400 "File too large (max 5 MB)". `s.UploadFont(family field or Undefined, name, bytes)`: errors are 400 with the message. Audit `font_upload` "Uploaded font: <family field, or the file name without extension, trimmed>", answer `{success, data}` |

The uploads folder is served as `/uploads/` without a session, and the
stored extension is the client's (BUGS I9).

## Fonts

| Route | Guard | Go |
|---|---|---|
| `GET /api/settings/fonts?q=&category=&source=` | signed in | q cut at 100 characters, category (default "all") and source (default "curated") at 20. `s.SearchGoogleFonts(q, category)` when source is "google", else `s.SearchFonts(q, category)`; an error is 500 "Failed to search fonts" |
| `GET /api/settings/fonts/local` | signed in | `s.ListLocalFonts()`; an error is 500 "Failed to list fonts" |
| `POST /api/settings/fonts/download` | branding.edit | `family` must be non-empty text, else 400 "Font family is required". `s.DownloadFont(ctx, family, body.weights)` with `branding.Undefined` when `weights` is missing or empty in JavaScript's eyes (null, false, 0, ""). Errors are 500 with the message. Audit `font_download` "Downloaded font: <family> (<n> files)" |
| `DELETE /api/settings/fonts/:family` | branding.edit | the path part decoded once more (`decodeURIComponent`), `s.DeleteLocalFont(family)`; an error is 500 "Failed to delete font". Audit `font_delete` only when something was deleted. Answer `{success, deleted}` |

The fonts folder is served as `/fonts/`.
