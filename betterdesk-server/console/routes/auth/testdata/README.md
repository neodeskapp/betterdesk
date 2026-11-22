# Recordings of the old console for console/routes/auth

The files below are fixed records of what the Node console answered, made
while it still existed. They can't be recorded again. The Go tests replay
them and need no Node. Don't edit them: when a fix changes an answer on
purpose, the test handles that step as an intended difference with the row
id (`TestLockoutAnswersOfNode` for UM20, UM21 and S17, `flowFixes` in
`flowfixes_test.go` for the flow).

| File | What it holds | Replayed by |
|---|---|---|
| `lockout_sqlite.json`, `lockout_postgres.json` | about 70 sign-ins that the lockout check decides, per database | `TestLockoutAnswersOfNode` (`oracle_test.go`) |
| `flow.json` | the sign-in flow of one account, as a person goes through it | `TestSignInFlow` (`flow_test.go`, build tag `authflow`) |

## What is recorded

The lockout recordings ran the sign-in routes (`routes/auth.routes.js`)
with the real `services/dbAdapter.js`, `services/authService.js`, the
translations and the sign-in limiter, against a made-up API server that
answered 404 to everything, so the password was checked locally. They
hold locked and unlocked accounts with the right and the wrong password,
user names of every JSON type (each database driver binds them its own
way), the order of the checks, an address with 29 and with 30 failures in
the window, a name locked by its own ten failures, and the sign-in
attempts, lockouts and audit entries all of that left. The test compares
the seconds left of a lockout to the full ones the seed gave.

A sign-in whose attempt SQLite cannot store (a list holding null) made
Node's unawaited `recordAttempt` reject; the recording notes the rejection
in the step instead of ending, as the console would (BUGS.md AR1).

The recorder also asked the account typeahead `GET /login/lookup`. The Go
console has no such route (BUGS UM26), so those answers are not kept.

`flow.json` was written by `TestSignInFlow` itself against a test instance
while the Node console still answered the sign-in routes: two-factor
enrolment, recovery codes, a password change, a second sign-in that ends
the first session, sign-out through the page, the lockout after ten wrong
passwords, the pro account, and the audit entries.
