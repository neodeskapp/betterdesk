# Recordings of the old console for console/branding

The gzipped files here are fixed records of what the Node console's
branding and font code answered, made while it still existed.
They can't be recorded again. The Go tests replay every record and must
answer the same, byte for byte; they need no Node. Don't edit them: when a
fix changes an answer on purpose, the test lists that record as an
intended difference with the row id. The copies of the console's own files
in `console` belong to the recordings and stay as they are too.

`files.json.gz` differs from the recording in two places, both edited by
hand: it has no supporters list (the console has none), and the branded
theme it applies is the made-up `console/themes/example.json`, whose names
replace the recorded theme's in the theme steps.

| File | What it holds | Replayed by |
|---|---|---|
| `pure.json.gz` | about 35,000 calls of the functions of `services/brandingService.js` and `services/fontService.js` that need no database: `sanitizeSvg`, `validateBrandingUrl`, `sanitizeCssColorValue`, `sanitizeCustomCss`, `buildBackgroundValue`, the normalisers, `sanitizeLoginSlides`, `flattenAppearanceInput`, `resolveThemeColors`, `hexToMutedRgba`, `usesDefaultLogo`, `getAppearanceModel`, `assessAppearanceReadability`, `sanitizeFontName`, `escapeCssString`, and the exported constants | `TestOraclePure`, `TestOracleConstants` |
| `store_sqlite.json.gz`, `store_postgres.json.gz` | a scenario of about 3,300 steps on the real `services/database` and `brandingService`: generated settings saved, resets, theme imports, rows written straight into `branding_config`, and the profile functions; after every step the stored rows, the cached settings, the stylesheet, and one hash over appearance model, readability, public appearance, export and favicon | `TestOracleStore` |
| `files.json.gz` | the font service step by step with the fonts folder after each step (search, download with the requests it sent, upload, list, delete, the font CSS, the cache file); uploads of logos, backgrounds and fonts through `routes/settings.routes.js`; the background list; the themes list and apply; the font files the uploads used | `TestOracleFiles`, and `TestUploadsLikeNode` in `console/routes/settings` |
| `idna.json.gz` | what Node's `URL` made of every code point in a host name (the table), and 20,000 URLs with hosts made of interesting characters | `TestOracleHosts` |

The Go source file `../idna_table.go` was generated from Node's tables by
the same recording. It can't be generated again either.

## What is recorded

Each recording worked on a copy of the console's code in a temporary
folder, because the font service and the upload routes write below their
own folder. For the file recording the database was a table in memory and
`https.get` answered from a list. The generators were seeded.
`pure.json.gz` and the two store recordings were made from a copy of the
old console in which the old project's names were replaced by NeoDesk's,
so their texts and hashes hold NeoDesk's names.

A call is `{a: [arguments], r: "<JSON text of the answer>"}`, with
`{u: true}` for undefined and `{e: message, n: error name}` for a throw.
An argument `{"$undefined": true}` stands for undefined. A long text is
recorded as `{len, sha256}` (of its UTF-8 bytes). Lone surrogates stay
what they are (`"\ud83d"`); the Go tests read the files with the package's
own JSON reader for that reason.

What changed from run to run is masked on both sides: `"revision":"..."`
becomes `<REV>`, the random part of upload names `<HEX>`, times `<T>`.
The default favicon is recorded as `<default favicon>`, because the file
has other line ends on a Windows checkout.

The font files in `files.json.gz` were built from tables, so no binary
font lives in the repository: a TrueType font with two glyphs (`.notdef`
and a triangle for "A"), the same outline as a CFF OpenType font, both
wrapped as WOFF (zlib) and WOFF2 (Brotli, `glyf` and `loca`
untransformed), with correct table checksums. FreeType 2.12.1 opened
`valid.ttf`, `valid.otf`, `valid.woff`, `valid.woff2` and `otf-in.woff2`
and refused the truncated files, the wrong magic and the non-fonts.
`bad-checksum.ttf` has one flipped byte in the last table; FreeType does
not check checksums and opens it.

`BRANDING_SHOW_ALL=1` makes the tests print every differing vector instead
of the first fifteen per function.

## What the two sides cannot agree on

- Lists that Node returned in the order of the folder on disk (installed
  fonts, the weights of a font, the themes, background pictures of the
  same age): Go returns them sorted by name. The recordings are sorted
  before they are compared.
- The wording of errors that come from the database driver or the
  network. A profile name that is taken, and an id beyond 32 bits on
  PostgreSQL, are worded as Node's drivers did, because routes send them.
- The revision on PostgreSQL for a process outside UTC (see
  `../storage.md`).
- A row order PostgreSQL does not promise: with two stored rows named
  like something every JavaScript object has (`constructor`, `valueOf`),
  their order as keys of the settings follows the table scan. The
  scenario uses one such row at a time.
- JSON nested deeper than 5,000 levels is refused by `ParseJSON`. Node
  read it and failed later, where the value was used, or not at all.
