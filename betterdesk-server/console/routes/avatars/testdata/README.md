# Recordings of the old console for console/routes/avatars

The file below is a fixed record of what the Node console answered, made
while it still existed. It can't be recorded again. The Go test replays it
and needs no Node. Don't edit it: when a fix changes an answer on purpose,
its request goes into `changedByFixes` in `routes_test.go` with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `routes.json.gz` | status, headers and body of 223 requests to `routes/avatars.routes.js` | `TestOracleRoutes` (`routes_test.go`) |

## What is recorded

The route ran on a scratch SQLite database that held pictures. The
contract suite can't cover this: a test instance has no pictures, and only
a single sign-on or a fetch from another host stores one.

The rows cover what the route can meet: PNG, GIF, WebP and JPEG, a type in
upper case, a type the console does not serve, a row without bytes, a row
without a type, versions that are short, empty or hold a space. The
requests cover ids in every form, `?v=` as text, list and object,
`If-None-Match` in every form, HEAD, and a request without a session.
