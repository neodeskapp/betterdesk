# Recordings of the old console for console/routes/serverattestation

The file below is a fixed record of what the Node console answered, made
while it still existed. It can't be recorded again. The Go tests replay it
and need no Node. Don't edit it: when a fix changes an answer on purpose,
the test lists that record as an intended difference with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `routes.json.gz` | the status and the bytes of every answer of the server attestation routes, in the lists below | `TestRoutesAsNodeAnswered` (`vectors_test.go`) |

## What is recorded

The recorder mounted the Node console's
`routes/server-attestation.routes.js` with the real
`services/serverAttestation.js` in an Express app, with a settings table
and audit log in memory (whose reads and writes could be made to fail), a
fixed brand name and fixed texts of the request language.

| List | What it holds |
|---|---|
| `put` | `PUT /api/server-attestation/badge-config` for 32 bodies (objects, arrays, every JSON type in the texts, values that cannot be made text, control characters, texts at and over their limits, halves of characters outside the basic plane, repeated keys, `__proto__`), with three brand names and three sets of texts, and with a failing write and a failing audit log; the rows and audit entries written, and the `GET` after it |
| `get` | `GET /api/server-attestation/badge-config` for stored texts of every kind, eight brand names (a missing one, `null`, a number) and three sets of texts, also with a read that fails; and a second `GET` after the row changed behind the service's back |
| `public` | `GET /api/public/server-attestation` for 55 stored results and 22 stored texts with five brand names and three sets of texts, and with each row unreadable |
| `status` | `GET /api/server-attestation/status` and `GET /api/server-attestation/result` for every stored result, also unreadable |
| `abort` | `POST /api/server-attestation/abort` with no test running |

Every route file and service was loaded anew for each record, because the
service keeps the texts it read. No capacity test was started; the tests
of `console/attestation` cover a run.
