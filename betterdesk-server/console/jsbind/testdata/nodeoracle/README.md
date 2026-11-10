# Node oracle for console/jsbind

`vectors.js` records what JavaScript and the Node console's database drivers
(`better-sqlite3` and `pg` from `web-nodejs/node_modules`) make of values.
The tests in `console/jsbind` replay `../vectors.json` and must agree.

This folder is a test fixture for the time both servers exist. It needs
`web-nodejs/node_modules` and goes away with it. `vectors.json` stays.

| Part | What it holds |
|---|---|
| `strings` | about 4,100 texts with `Number(s)`, `s.trim()` and `s.toLowerCase()` |
| `values` | JSON values of every type with `Number`, `String`, truth, `JSON.stringify`, the parameter node-postgres sends, and the type and text SQLite gets from better-sqlite3 (or the error it throws) |
| `lists` | lists of values bound to a statement with two placeholders: what arrives, or the error |

## Running

    tools/dev.sh "cd betterdesk-server/console/jsbind/testdata && node nodeoracle/vectors.js"

The generator has a fixed seed, so a diff in `vectors.json` after a run means
JavaScript or a driver changed.
