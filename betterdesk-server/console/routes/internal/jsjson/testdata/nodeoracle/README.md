# Node oracle for console/routes/internal/jsjson

`vectors.js` writes `../vectors.json`: 8,840 JSON texts with what Node made
of each. `TestNodeVectors` in the Go package does the same operations and
must get the same results.

For every text:

- whether `JSON.parse` took it (1,611 are no JSON)
- `JSON.stringify` of the parsed value
- its truth (`!!v`)
- `String(v)` as UTF-16 units, or that it threw
- `Number(v)`, with the sign of a negative zero, or that it threw
- the members the routes read (`length`, `hostname`, `cpu`, ...), each as
  JSON, and which of them are missing
- for a text: its length, and its first 0, 1, 2, 3, 5 and 120 units both as
  units and as `JSON.stringify` writes them

The texts are scalars picked by hand (numbers at the edges of every format,
texts that are almost numbers, escapes, both halves of surrogate pairs
alone and together), lists and objects built from them at random with a
fixed seed, and valid texts with one character dropped, doubled or
replaced.

The script needs nothing but Node and has no backslash in it: every
special character is made from its number.

This folder is a test fixture for the time the Node console exists. The
vectors stay.

## Running

In the dev image:

    tools/dev.sh "cd betterdesk-server/console/routes/internal/jsjson && node testdata/nodeoracle/vectors.js"
