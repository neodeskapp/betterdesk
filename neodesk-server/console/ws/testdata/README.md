# Recordings of the old console for console/ws

`node_targets.json` is a fixed record of what the old console made of a
socket upgrade, made while it still existed. It can't be recorded again.
The Go tests replay it and need no Node. Don't edit it: when a fix changes
an answer on purpose, the test lists that record as an intended difference
with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `node_targets.json` | request targets and origin checks | `TestNodeTargets`, `TestNodeOrigins` (`target_test.go`) |

## What is recorded

- 6,000 request targets (hand-picked and generated from parts: dot
  segments in every spelling, backslashes, two slashes, absolute URLs,
  escapes good and bad, query pairs with `+`, empty names, repeated names,
  bytes that are not UTF-8): `new URL(target, 'http://console.test:5000')`,
  whether it threw, its pathname, and `searchParams.get()` for eight names;
- 1,320 origin checks: `validateOrigin` of `middleware/wsOrigin.js` for
  eleven origins, eight cookie headers and three hosts under five settings
  of `WS_ALLOWED_ORIGINS` and `BD_ALLOW_MISSING_ORIGIN`.
