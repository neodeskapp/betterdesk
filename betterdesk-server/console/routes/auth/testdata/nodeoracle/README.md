# Node oracle for console/routes/auth

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

`auth.js` runs the Node console's sign-in routes (`routes/auth.routes.js`)
with the real `services/dbAdapter.js`, `services/authService.js`, the
translations and the sign-in limiter, against a made-up API server that
answers 404 to everything (so the password is checked locally), and
records two things per database:

- `../lookup_<db>.json`: about 870 requests to the account typeahead
  `GET /login/lookup` over 70 accounts: the parameter missing, given twice,
  in brackets, empty, too short and too long (in UTF-16 units), prefixes of
  every name in upper, lower and given case with white space of every kind
  around them, and names whose lower case JavaScript writes its own way
  (final sigma, the dotted capital I, the Kelvin sign, title case letters).
  `TestLookupAnswersOfNode` replays them.
- `../lockout_<db>.json`: about 70 sign-ins that the lockout check decides:
  locked and unlocked accounts with the right and the wrong password,
  user names of every JSON type (each database driver binds them its own
  way), the order of the checks, an address with 29 and with 30 failures
  in the window, a name locked by its own ten failures, and the sign-in
  attempts, lockouts and audit entries all of that left.
  `TestLockoutAnswersOfNode` replays them; the seconds left of a lockout
  are compared to the full ones the seed gave.

This folder is a test fixture from the time both servers existed. The route
file is gone from `web-nodejs` here; the script loads the spec tree of
upstream 3.5.264, mounted at `/spec/web-nodejs` (`WEB_NODEJS` names another
place):

    DEV_DOCKER_ARGS="--network bdgo -v <spec tree>/web-nodejs:/spec/web-nodejs -v bdgo-node-nm:/spec/web-nodejs/node_modules" \
      tools/dev.sh "cd betterdesk-server/console/routes/auth/testdata && node nodeoracle/auth.js && \
        ORACLE_PG_DSN='postgres://betterdesk:bdgo-local@bdgo-testpg:5432/betterdesk_test?sslmode=disable' node nodeoracle/auth.js"

On PostgreSQL the script creates a database of its own and drops it at
the end. A sign-in whose attempt SQLite cannot store (a list holding null)
makes Node's unawaited `recordAttempt` reject; the script notes the
rejection in the step instead of ending, as the console would (BUGS.md AR1).
