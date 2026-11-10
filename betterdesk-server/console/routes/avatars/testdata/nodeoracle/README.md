# Node oracle for console/routes/avatars

`routes.js` runs the Node console's `routes/avatars.routes.js` on a scratch
SQLite database that holds pictures, sends it 223 requests and records
status, headers and body of every answer in `../routes.json.gz`. The Go
test `TestOracleRoutes` replays them.

The contract suite cannot do this: a test instance has no pictures, and
only a single sign-on or a fetch from another host stores one.

The rows cover what the route can meet: PNG, GIF, WebP and JPEG, a type in
upper case, a type the console does not serve, a row without bytes, a row
without a type, versions that are short, empty or hold a space. The
requests cover ids in every form, `?v=` as text, list and object,
`If-None-Match` in every form, HEAD, and a request without a session.

This folder is a test fixture for the time both servers exist. The script
needs `web-nodejs` with the route file in it, which is gone since the route
moved to Go; the recorded file stays and keeps pinning the Go route to what
Node answered.

It was run in the dev image:

    tools/dev.sh "cd betterdesk-server/console/routes/avatars/testdata && node nodeoracle/routes.js"
