# Recordings of the old console for console/qr

The files below are fixed records of what the npm package `qrcode`
(1.5.4), the one the Node console made its QR codes with, produced. They
were made while the console still existed and can't be recorded again.
The Go tests replay them and need no Node. Don't edit them: when a fix
changes a code on purpose, the test lists that vector as an intended
difference with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `vectors.jsonl.gz` | about 7,000 texts with the version, the mask and every module the package produced for them | `TestSameModulesAsNpmPackage` (`oracle_test.go`) |
| `images.jsonl.gz` | a few hundred PNG images from the call the console made (`QRCode.toDataURL`) for several widths and margins | `TestSamePixelsAsNpmPackage` (`oracle_test.go`) |

The tests compare module by module and pixel by pixel. The texts came
from a seeded random generator, in a copy of the old console's recorder in
which the old project's names were replaced by NeoDesk's, so the
two-factor addresses name NeoDesk.

## File format

Both files are gzip-compressed JSON, one object per line.

`vectors.jsonl.gz`:

| Field | Meaning |
|---|---|
| `t` | the text |
| `x` | instead of `t`: bytes in base64 that are not text. The package got them as one explicit byte segment |
| `l` | error correction level: `L`, `M`, `Q` or `H` |
| `e` | `1` when the package refused the input (empty, or too long for version 40). Then the next three are missing |
| `v` | version, 1 to 40 |
| `m` | mask, 0 to 7 |
| `b` | the modules row by row, one bit each, the first module in the highest bit of the first byte, in base64 |

`images.jsonl.gz`:

| Field | Meaning |
|---|---|
| `t`, `l` | text and level |
| `w` | the `width` option; `0` when it was left out |
| `g` | the `margin` option |
| `p` | the PNG file in base64 |
