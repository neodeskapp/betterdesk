# Recordings of the old console for console/jsbind

The JSON files here are fixed records of what JavaScript, the Node
console's database drivers (`better-sqlite3` and `pg`) and its query
parser made of values, made while the Node console still existed. They
can't be recorded again. The Go tests replay them and need no Node. Don't
edit them: when a fix changes a result on purpose, the test lists that
record as an intended difference with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `vectors.json` | the parts below | `jsbind_test.go` |
| `query.json` | what qs, with the options Express gave it, made of the parameter "name" in about 6,000 query strings | `TestQueryAsTheOldParserReadIt`, `TestQueryTextAsTheOldParserReadIt` |

| Part of `vectors.json` | What it holds |
|---|---|
| `strings` | about 4,100 texts with `Number(s)`, `s.trim()` and `s.toLowerCase()` |
| `values` | JSON values of every type with `Number`, `String`, truth, `JSON.stringify`, the parameter node-postgres sent, and the type and text SQLite got from better-sqlite3 (or the error it threw) |
| `lists` | lists of values bound to a statement with two placeholders: what arrived, or the error |

The generators were seeded.
