# Where the branding lives

## Two brandings that do not know each other

There are two things called branding, with separate storage and no code
that copies one into the other:

| | Console appearance (this package) | Client branding (the server) |
|---|---|---|
| What it is | How the web console looks: name, logo, theme, colours, wallpapers, sign-in page, custom CSS | What desktop clients show: company name, phone, e-mail, website, accent colour, support contact, a logo, colours |
| Storage | table `branding_config` (one row per setting), profiles in `branding_profiles` | keys `branding_company_name`, `branding_phone`, `branding_email`, `branding_website`, `branding_accent_color`, `branding_support_contact`, `branding_colors`, `branding_logo`, `branding_revision` in `server_config` |
| Written by | `SaveBranding`, `ResetBranding` (Node: `services/brandingService.js`) | `POST /api/branding` of the Go API (`api/branding_handlers.go:331-373`) |
| Read by | the console and its pages, `/css/branding.css`, `/api/nd/appearance` | `GET /api/branding` of the Go API, the heartbeat of RustDesk clients |
| Edited in | Settings, Appearance | Main, Client Branding (`/api/client-branding`, which only passes the request on to the Go API through `neodeskApi.getBranding` and `saveBranding`) |
| Revision | `Service.Revision()`, from `MAX(updated_at)` of `branding_config` | `branding_revision`, written at every save |

`spec/data.md` (open question 6) asked how the two are kept in line. They
are not. `brandingService.js` never calls the Go API, and
`branding_handlers.go` never reads `branding_config`. Changing the app
name in the console does not change the company name clients see, and the
other way round. The only place both meet is `GET /api/nd/branding`
(`bd-api.routes.js:525`), which answers desktop clients from the console
side alone (`appName` as `company_name`, `colors.accentBlue` as
`accent_color`) and never looks at the server's keys. Its
`support_contact` reads a setting that does not exist and is always empty,
while the server has a `branding_support_contact` (BUGS BR6).

The Go console keeps both as they are. Nothing in this package touches
`server_config`.

## `branding_config`

`key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at` (text
`YYYY-MM-DD HH:MM:SS` on SQLite, `TIMESTAMPTZ` on PostgreSQL). Created by
`store.Migrate`. Not shared with the signal server.

- One row per setting that was ever saved. A missing row means the
  default. `ResetBranding` deletes every row.
- Every value is text. `colors` is a JSON object of the colours that were
  given, `loginSlides` a JSON list of cleaned slides.
- `appearanceSchemaVersion` is written as "2" by every save.
- Rows whose key is no setting are ignored when loading. (A row named
  like something every JavaScript object has, such as `constructor`,
  becomes a key of the settings object. No save writes such a row.)
- Saves are one transaction: `INSERT ... ON CONFLICT(key) DO UPDATE`,
  with `updated_at` set to the database's current time.
- On a SQLite file from before 3.0 the table has no `updated_at` column
  and cannot get one (BUGS D17). Loading works there, the revision is the
  current time, and every save fails.

## The cache and the revision

`LoadBranding` reads all rows over the defaults and keeps the result until
the next load. It runs at start and at the end of every `SaveBranding`
and `ResetBranding`. `GetBranding` never reads the database.

The cache is the one copy every route reads; every write of the
settings goes through this service and reloads it. Rows written into
`branding_config` past the service show at the next write or start. The
restore of a full backup is such a writer (BUGS BR14), as it was in Node.

While the Node console still runs behind the Go one, it keeps its own
cache, filled once at its start. The routes it still answers that read
the branding (`GET /api/remote/login-info`, `guest-info`) show the
settings of that moment until the remote routes move (BUGS BR17).

The revision is:

| State | Revision |
|---|---|
| before the first load | `0` |
| rows exist | `MAX(updated_at)` as text, with everything but letters, digits, `.`, `_`, `-` removed. SQLite: `2026-11-10010551`. PostgreSQL: the time as JavaScript prints a date, `TueNov102026010551GMT0000CoordinatedUniversalTime` |
| no rows, no readable time, or the load failed | the current time in milliseconds, base 36 |

On PostgreSQL the text depends on the time zone of the process, as it did
in Node. For zones other than UTC Node printed the zone's long English
name ("Central European Standard Time"); Go has no such names, so
`Options.ZoneName` can supply one and the zone's abbreviation is used
otherwise. The revision is only compared for change, never parsed.

## `branding_profiles`

`id, name UNIQUE, description, data, is_active, created_at, updated_at`.
`data` is the JSON of a theme: what `ExportPreset` returns when the
profile is made from the current settings, or `{version: "1.0", type:
"neodesk-theme", branding: ...}` when the caller sends settings.
`is_active` is 1 for the profile applied last.

The list is sorted by name: without regard to ASCII case on SQLite, by
the collation of the database on PostgreSQL.

## Files

| What | Where (Node) | Option |
|---|---|---|
| Logo and background pictures: `logo-<16 hex><ext>` (2 MB), `bg-<16 hex><ext>` (8 MB) | `<DATA_DIR>/uploads`, served as `/uploads/` | `UploadsDir` |
| Installed fonts: `<name>/font.css`, `<name>/<name>-<weight>.woff2` or `<name>/<name>.ttf`, `<name>/meta.json` for uploads | `<console>/public/fonts`, served as `/fonts/` | `FontsDir` |
| An uploaded font before it is installed: `font-upload-<ms>.ttf` or `.woff2` | `<console>/data/tmp` | `TempDir` |
| Optional list of more font families for the search | `<console>/data/google-fonts-cache.json` | `FontCacheFile` |
| Built-in themes | `<console>/themes/*.json` | `ThemesDir` |
| Built-in palettes | `<console>/config/theme-palettes.json` | `PalettesFile` |
| Default favicon | `<console>/public/favicon.svg` | `FaviconFile` |

`<console>` is the folder of the Node console (`web-nodejs`). Fonts and
the temporary folder sit in the program's folder, not in the data
folder; an install that replaces the program folder on update loses its
fonts (BUGS BR8).
