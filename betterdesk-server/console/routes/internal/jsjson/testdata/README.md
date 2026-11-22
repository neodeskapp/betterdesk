# Recordings of the old console for console/routes/internal/jsjson

The file below is a fixed record of what JavaScript did in the Node
console, made while it still existed. It can't be recorded again. The Go
tests replay it and need no Node. Don't edit it: when a fix changes a
result on purpose, the test lists that text as an intended difference with
the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `vectors.json` | 8,840 JSON texts and what Node made of each | `TestNodeVectors` (`jsjson_test.go`) |

## What is recorded

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
