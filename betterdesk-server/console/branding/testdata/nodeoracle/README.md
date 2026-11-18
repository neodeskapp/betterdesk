# Node oracle for console/branding

These scripts run the console's Node modules (`web-nodejs`) and record what
they answer. The Go tests in `console/branding` replay every record and
must answer the same, byte for byte (`oracle_test.go`,
`store_oracle_test.go`, `files_oracle_test.go`).

This folder is a test fixture for the time both servers exist. It needs
`web-nodejs` and goes away with it. The recorded files in the parent
folder (`*.json.gz`) and the copies of the console's own files in
`../console` stay: they are plain data and keep pinning the Go code to
what Node did. `idna.js` also writes the Go source file `../../idna_table.go`.

`files.js` loads `routes/settings.routes.js` for its uploads, and that file
is gone: the routes are answered by `console/routes/settings`, whose test
`TestUploadsLikeNode` replays the recorded uploads through the Go routes.
The script runs again only on a checkout from before that move.

Every script works on a copy of the console's code in a temp folder
(`lib.js`, `scratchConsole`), because the font service and the upload
routes write below their own folder.

## Scripts

| Script | Loads | Writes | What it records |
|---|---|---|---|
| `pure.js` | `services/brandingService.js`, `services/fontService.js`, the database replaced by a fake that fails | `pure.json.gz` | About 35,000 calls of the functions that need no database: `sanitizeSvg`, `validateBrandingUrl`, `sanitizeCssColorValue`, `sanitizeCustomCss`, `buildBackgroundValue`, the normalisers, `sanitizeLoginSlides`, `flattenAppearanceInput`, `resolveThemeColors`, `hexToMutedRgba`, `usesDefaultLogo`, `getAppearanceModel`, `assessAppearanceReadability`, `sanitizeFontName`, `escapeCssString`, and the exported constants |
| `store.js` | the real `services/database` and `brandingService` on a scratch database | `store_sqlite.json.gz`, and `store_postgres.json.gz` with `ORACLE_PG_DSN` | A scenario of about 3,300 steps: generated settings saved, resets, theme imports, rows written straight into `branding_config`, and the profile functions. After every step: the stored rows, the cached settings, the stylesheet, and one hash over appearance model, readability, public appearance, export and favicon |
| `files.js` | `fontService`, `brandingService`, `supportersService`, and `routes/settings.routes.js` in an Express app with multer; the database is a table in memory, `https.get` answers from a list | `files.json.gz` | The font service step by step with the fonts folder after each step (search, download with the requests it sent, upload, list, delete, the font CSS, the cache file); uploads of logos, backgrounds and fonts through the routes; the background list; the themes list and apply; the supporters list for a dozen file contents |
| `idna.js` | Node's `URL`, `brandingService` | `idna.json.gz`, `../../idna_table.go` | What Node makes of every code point in a host name (the table), and 20,000 URLs with hosts made of interesting characters |
| `fontfiles.js` | nothing of the console | (used by `files.js`) | Builds the font files for the upload tests |

`lib.js` has the shared parts: the seeded random source, the generators
for settings (plain, wrong type, hostile), the copy of the console and the
vector format.

## Vector format

A call is `{a: [arguments], r: "<JSON text of the answer>"}`, with
`{u: true}` for undefined and `{e: message, n: error name}` for a throw.
An argument `{"$undefined": true}` stands for undefined. A long text is
recorded as `{len, sha256}` (of its UTF-8 bytes). Lone surrogates stay
what they are (`"\ud83d"`); the Go tests read the files with the package's
own JSON reader for that reason.

What changes from run to run is masked on both sides: `"revision":"..."`
becomes `<REV>`, the random part of upload names `<HEX>`, times `<T>`.
The default favicon is recorded as `<default favicon>`, because the file
has other line ends on a Windows checkout.

## The font files

`fontfiles.js` builds them from tables written out in the script, so no
binary file lives in the repository: a TrueType font with two glyphs
(`.notdef` and a triangle for "A"), the same outline as a CFF OpenType
font, both wrapped as WOFF (zlib) and WOFF2 (Brotli, `glyf` and `loca`
untransformed), with correct table checksums. FreeType 2.12.1 in the dev
image opens `valid.ttf`, `valid.otf`, `valid.woff`, `valid.woff2` and
`otf-in.woff2` (checked with `FT_New_Memory_Face`, error 0) and refuses
the truncated files, the wrong magic and the non-fonts. `bad-checksum.ttf`
has one flipped byte in the last table; FreeType does not check checksums
and opens it.

## Running

From the worktree, in the dev image (`tools/dev.sh` of the notes folder):

    tools/dev.sh "cd betterdesk-server/console/branding/testdata && \
      node nodeoracle/pure.js && node nodeoracle/idna.js && node nodeoracle/files.js && node nodeoracle/store.js"
    DEV_DOCKER_ARGS="--network bdgo" tools/dev.sh "cd betterdesk-server/console/branding/testdata && \
      ORACLE_PG_DSN='postgres://betterdesk:bdgo-local@bdgo-testpg:5432/betterdesk_test?sslmode=disable' node nodeoracle/store.js"

The generators are seeded, so a second run gives the same inputs. A
changed recorded file after a run means Node's behaviour changed (or, for
`idna.js`, Node's Unicode data).

To look at a difference, record everything in full somewhere else and
point the Go tests at it:

    ORACLE_FULL=1 ORACLE_OUT=/tmp/full ORACLE_STEPS=400 node nodeoracle/store.js
    BRANDING_VECTORS_FULL=1 BRANDING_VECTORS_DIR=/tmp/full go test ./console/branding/ -run TestOracleStore/sqlite

`BRANDING_SHOW_ALL=1` prints every differing vector instead of the first
fifteen per function.

## What the two sides cannot agree on

- Lists that Node returns in the order of the folder on disk (installed
  fonts, the weights of a font, the themes, background pictures of the
  same age): Go returns them sorted by name. The recordings are sorted
  before they are compared.
- The wording of errors that come from the database driver or the
  network. A profile name that is taken, and an id beyond 32 bits on
  PostgreSQL, are worded as Node's drivers did, because routes send them.
- The revision on PostgreSQL for a process outside UTC (see
  `../../storage.md`).
- A row order PostgreSQL does not promise: with two stored rows named
  like something every JavaScript object has (`constructor`, `valueOf`),
  their order as keys of the settings follows the table scan. The
  scenario uses one such row at a time.
- JSON nested deeper than 5,000 levels is refused by `ParseJSON`. Node
  reads it and fails later, where the value is used, or not at all.
