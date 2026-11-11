# Node oracle for console/routes/serverattestation

`routes.js` mounts the Node console's `routes/server-attestation.routes.js`
with the real `services/serverAttestation.js` in an Express app, with a
settings table and audit log in memory (whose reads and writes can be made
to fail), a fixed brand name and fixed texts of the request language, and
sends it requests. It records the status and the bytes of every answer:

| List | What it holds |
|---|---|
| `put` | `PUT /api/server-attestation/badge-config` for 32 bodies (objects, arrays, every JSON type in the texts, values that cannot be made text, control characters, texts at and over their limits, halves of characters outside the basic plane, repeated keys, `__proto__`), with three brand names and three sets of texts, and with a failing write and a failing audit log; the rows and audit entries written, and the `GET` after it |
| `get` | `GET /api/server-attestation/badge-config` for stored texts of every kind, eight brand names (a missing one, `null`, a number) and three sets of texts, also with a read that fails; and a second `GET` after the row changed behind the service's back |
| `public` | `GET /api/public/server-attestation` for 55 stored results and 22 stored texts with five brand names and three sets of texts, and with each row unreadable |
| `status` | `GET /api/server-attestation/status` and `GET /api/server-attestation/result` for every stored result, also unreadable |
| `abort` | `POST /api/server-attestation/abort` with no test running |

Every route file and service is loaded anew for each record, because the
service keeps the texts it read. No capacity test is started here; the
tests of `console/attestation` cover a run.

`TestRoutesAsNodeAnswered` replays every record against the Go handlers
and compares status and bytes.

This folder is a test fixture for the time both servers exist. The route
file is gone from the tree, so the script runs on a checkout from before
the move (point `WEB_NODEJS` at its `web-nodejs` folder). The recorded file
stays.

    tools/dev.sh "cd betterdesk-server/console/routes/serverattestation/testdata/nodeoracle && node routes.js"
