# Node oracle for the QR code tests

`generate.js` writes the two vector files one folder up with the npm package
`qrcode` (1.5.4), the one the Node console makes its QR codes with:

- `vectors.jsonl.gz`: about 7,000 texts with the version, the mask and every
  module the package produces for them.
- `images.jsonl.gz`: a few hundred PNG images from the call the console
  makes (`QRCode.toDataURL`) for several widths and margins.

The Go tests in `console/qr` replay both files and compare module by module
and pixel by pixel. They need only the files, not Node.

This folder is a fixture that goes away with Node. The script needs the
console's `node_modules`, so once `web-nodejs` is gone it cannot run any
more. The vector files stay useful as they are: they pin what the Go
encoder draws.

## Running it

In the dev container, from `/repo/web-nodejs`:

    node ../betterdesk-server/console/qr/testdata/nodeoracle/generate.js

The texts come from a seeded random generator, so a second run writes the
same vectors. Change the script, run it, and commit the two files with it.

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
