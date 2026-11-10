# Node fixtures for the session and CSRF interop tests

These files are test fixtures. They load the Node console's code from
`/repo/web-nodejs` (the path in the dev image), so they only work while the
Node console exists. Delete this folder, `interop_test.go` and
`interop_policy_test.go` together with the Node console.

| File | Used by | What it does |
|---|---|---|
| `app.js` | `interop_test.go` (build tag `nodeinterop`) | A small Express app around the console's real `express-session` setup, `DatabaseSessionStore`, `panelSessionService`, the idle check from `server.js` and `middleware/csrf.js`. The Go test starts it on a free port, on the same database and with the same secret as the Go packages |
| `policy.js` | `interop_policy_test.go` (build tag `nodeinterop`) | Prints what `lib/sessionPolicy.js` makes of settings and values the test sends on stdin |
| `cookie-vectors.js` | by hand | Writes `../../cookie/testdata/parse_vectors.json`: Cookie headers and what the `cookie` package reads from them. That file stays after Node is gone |

Run the interop tests in the dev image, with the test PostgreSQL:

    tools/gotest.sh "-tags nodeinterop ./console/session/ -run 'Interop|MatchesNode' -count=1"

Regenerate the cookie vectors:

    tools/dev.sh "cd betterdesk-server/console/session/testdata/interop && node cookie-vectors.js"
