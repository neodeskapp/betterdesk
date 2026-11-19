# The Node console as the reference for `console/authn`

`web-nodejs` is gone from this tree. These scripts stay as the record of
how the recordings in this package were made; the Go tests replay the
recordings and need no Node. To run a script again, use a tree that still
has `web-nodejs`: the spec worktree
`C:\Users\info\worktrees\betterdesk-go-spec264`. Mount its `web-nodejs`,
with its modules, over `/repo/web-nodejs` of this tree:

    DEV_DOCKER_ARGS="-v C:/Users/info/worktrees/betterdesk-go-spec264/web-nodejs:/repo/web-nodejs -v bdgo-node-nm:/repo/web-nodejs/node_modules" \
      tools/dev.sh "cd betterdesk-server/<this folder> && node <script>"

Its Node code is upstream's release there, so a script that loads a file
the port deleted or changed on the way can fail or record other answers.

This folder is a test fixture. It stays now that `web-nodejs` is gone:
once the Node console is deleted there is nothing left to compare with,
and the vectors under `../vectors` stay as the record of what it did.

The scripts load the Node console's real code (`services/authService.js`,
`lib/tokenHash.js`, `lib/deviceTokenAuth.js`, `middleware/deviceAuth.js`,
`lib/oidcRedirectUrl.js` and the database adapter), call it with many
inputs and write down what it answered. The Go tests replay the files. The
code is the spec tree of upstream 3.5.264, mounted into the container at
`/spec/web-nodejs` (`BETTERDESK_NODE_ROOT` names another place);
`interop.js` reads `/repo/web-nodejs`.

| Script | Writes | Replayed by |
|---|---|---|
| `pure.js` | `hashes.json`, `strength.json`, `totp.json`, `tokens.json`, `oidc.json`, `providers.json`: the functions that need no database | `vectors_test.go` |
| `db.js sqlite`, `db.js postgres` | `db-sqlite.json`, `db-postgres.json`: scenarios on a real database | `scenario_test.go` |
| `interop.js` | nothing: it is the child process of `interop_test.go` (build tag `nodeinterop`) | |

`lib.js` has what the scripts share, `harness.js` is what `db.js` runs on.

## Running them

In the dev image, from the notes folder:

    tools/dev.sh "cd betterdesk-server/console/authn/testdata/nodeoracle && node pure.js && node db.js sqlite"

    DEV_DOCKER_ARGS="--network bdgo -e BETTERDESK_TEST_POSTGRES_DSN=postgres://betterdesk:bdgo-local@bdgo-testpg:5432/betterdesk_test?sslmode=disable" \
      tools/dev.sh "cd betterdesk-server/console/authn/testdata/nodeoracle && node db.js postgres"

    tools/gotest.sh "./console/authn/ -tags nodeinterop -run Interop -count=1"

`db.js` removes and creates files under `/opt/rustdesk`, `/opt/betterdesk`,
`/app/data` and `/.dockerenv`, because the default admin's set-up looks
there. Run it in the throwaway container only.

## What `db.js` controls

A scenario starts on empty tables, seeds rows, and calls the service step
by step. Everything around the service is fixed so that a second run, and
the Go replay, get the same values:

- The Go server is an HTTP listener in the script. A step says what it
  answers (a status with a body, no answer at all, a closed connection),
  and every request the service sends is written down.
- The clock (`Date.now()`, `new Date()`) stands still during a step. It is
  either a fixed time (for time-based codes, which then are literal in the
  vectors) or the start of the scenario plus an offset (for lockouts and
  token lifetimes, which the database compares with its own clock).
- `crypto.randomBytes` returns a repeatable stream per step (the step's
  `seed`), for the sizes the service asks for. Salts, tokens, secrets and
  codes are therefore the same in Node and Go, and the stored hashes are
  compared letter by letter.
- bcrypt hashes with cost 4 instead of 12, to keep thousands of sign-ins
  fast. Nothing else about bcrypt is changed, and `pure.js` covers the
  real cost.
- The waits of the default admin's set-up (`setTimeout`) return at once
  and move the clock. A step can say what happens during a wait: a file
  appears, a user appears.
- Log lines are recorded as the service passes them to the logger, before
  the console's logger shortens the names in them.

A step's record has the call, what it returned or threw, the requests to
the Go server, the log lines, the waits and the watched files. A scenario
ends with the rows of `users`, `login_attempts`, `account_lockouts` and
`access_tokens`; times in them are written as seconds from the database's
clock, rounded to ten.

## What cannot be compared

- The text of a thrown error. The replay checks that Go fails where Node
  threw, not the wording.
- The operating system's words in the log line for a credentials file that
  cannot be written.
- The QR code of the two-factor set-up: Node's is replaced by `QR:` and
  the address it encodes, and the Go test uses a stand-in that returns
  the same. The picture itself is `console/qr`'s business.
