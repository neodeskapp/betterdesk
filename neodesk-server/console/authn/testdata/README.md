# Recordings of the old console for console/authn

The files in `vectors/` are fixed records of what the Node console's
sign-in code answered, made while it still existed. They can't be recorded
again. The Go tests replay them and need no Node. Don't edit them: when a
fix changes an answer on purpose, the test lists that step as an intended
difference with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `vectors/hashes.json` | password hashes and their checks | `TestHashVectors` (`vectors_test.go`) |
| `vectors/strength.json` | the password strength rules | `TestStrengthVectors` |
| `vectors/totp.json` | time-based codes and secrets | `TestTOTPVectors` |
| `vectors/tokens.json` | token hashes and device token checks | `TestTokenVectors` |
| `vectors/oidc.json` | redirect addresses of single sign-on | `TestOIDCVectors` |
| `vectors/providers.json` | the sign-in providers | `TestProviderVectors` |
| `vectors/db-sqlite.json`, `vectors/db-postgres.json` | scenarios of the auth service on a real database | `TestScenarios` (`scenario_test.go`) |

## What is recorded

The recorders loaded the Node console's real code (`services/authService.js`,
`lib/tokenHash.js`, `lib/deviceTokenAuth.js`, `middleware/deviceAuth.js`,
`lib/oidcRedirectUrl.js` and the database adapter) from upstream 3.5.264,
called it with many inputs and wrote down what it answered. The first six
files cover the functions that need no database.

A scenario started on empty tables, seeded rows, and called the service
step by step. Everything around the service was fixed, so that the Go
replay gets the same values:

- The Go server was an HTTP listener in the recorder. A step says what it
  answers (a status with a body, no answer at all, a closed connection),
  and every request the service sent is in the record.
- The clock (`Date.now()`, `new Date()`) stood still during a step. It was
  either a fixed time (for time-based codes, which then are literal in the
  vectors) or the start of the scenario plus an offset (for lockouts and
  token lifetimes, which the database compares with its own clock).
- `crypto.randomBytes` returned a repeatable stream per step (the step's
  `seed`, the generator `mulberry` in `scenario_test.go`), for the sizes
  the service asked for. Salts, tokens, secrets and codes are therefore the
  same in Node and Go, and the stored hashes are compared letter by letter.
- bcrypt hashed with cost 4 instead of 12, to keep thousands of sign-ins
  fast. Nothing else about bcrypt was changed, and `hashes.json` covers the
  real cost.
- The waits of the default admin's set-up (`setTimeout`) returned at once
  and moved the clock. A step can say what happened during a wait: a file
  appeared, a user appeared.
- Log lines were recorded as the service passed them to the logger, before
  the console's logger shortened the names in them.

A step's record has the call, what it returned or threw, the requests to
the Go server, the log lines, the waits and the watched files. A scenario
ends with the rows of `users`, `login_attempts`, `account_lockouts` and
`access_tokens`; times in them are written as seconds from the database's
clock, rounded to ten.

## What the replay doesn't compare

- The text of a thrown error. The replay checks that Go fails where Node
  threw, not the wording.
- The operating system's words in the log line for a credentials file that
  can't be written.
- The QR code of the two-factor set-up: Node's is replaced by `QR:` and
  the address it encodes, and the Go test uses a stand-in that returns
  the same. The picture itself is `console/qr`'s business.
