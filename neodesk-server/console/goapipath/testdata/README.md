# Recordings of the old console for console/goapipath

The JSON files here are fixed records of what the Node console's
`lib/goApiPath.js`, `lib/goApiProxy.js`, `services/neodeskApi.js` and
Express's query parser answered, made while they still existed. They can't
be recorded again. The Go tests replay them and need no Node. Don't edit
them: when a fix changes an answer on purpose, the vector goes into
`proxyFixes` in `proxy_test.go` with the row id, and
`PROXYTEST_RECORD_FIXED=1` writes the Go answers of those vectors into
`proxy_fixed.json`.

| File | What it holds | Replayed by |
|---|---|---|
| `paths.json` | the path checks | `TestPathVectors` |
| `query.json` | `req.query` for raw query strings | `TestQueryVectors` |
| `js.json` | `parseInt`, the text of a number, `slice`, `URLSearchParams` | `TestJavaScriptVectors` |
| `proxy.json` | `proxyToGo` and `proxyBinaryToGo` behind three small routes, with the real API client in front of a scripted API server | `TestProxyVectors` |
| `proxy_fixed.json` | written by the Go test: the answers of the vectors a fix changed | `TestProxyVectors` |

A proxy vector is one request: what the client got and what the API server
was asked. The route groups that only pass calls on have vectors of the
same kind in their own testdata (`console/routes/fleet`, `policies`,
`resourcecontrol`), and package `proxytest` replays them against the Go
routes.

In a vector file a character of a request path stands for one byte, so a
path can hold bytes that are no UTF-8.
