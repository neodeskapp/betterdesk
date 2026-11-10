# Node oracle for the device guards of console/kit

This folder is a test fixture. It goes away together with `web-nodejs`:
once the Node console is deleted there is nothing left to compare with,
and the recordings next to this folder stay as the record of what it did.

`devicetoken.js` runs `requireDeviceToken` and `requireTokenDeviceMatch`
of the Node console (`middleware/deviceAuth.js`, with the real database
facade) in a small Express app with the console's body parsers and its
error answer, on a scratch database with token rows in every state:
valid, bound to another device, expired, revoked, bound to no device or to
an id of the wrong form, found by its hash only, found by the token text
only, stored with the hash of another text, with a non-ASCII token, and one
whose use cannot be noted (a trigger refuses the update). It sends raw HTTP
requests: the header in its many spellings, and the device id as a claim
in the body (JSON of every type, forms), the query (as Express parses it:
lists, objects, repeats) and `X-Device-Id`, in every mix. At the end it
renames the token table, so that the lookup itself fails.

For each request it records the bytes sent, the status and body of the
answer, and the token rows whose use was noted. `TestDeviceGuardsAnswerAsNode`
in `../../device_test.go` sends the same bytes through `kit.Deps.RequireDeviceToken`
and `RequireTokenDeviceMatch` behind the console's body step, on a database
with the same rows, and must get the same.

In a recording a character of `raw` stands for one byte.

## Running

In the dev image, from the notes folder:

    tools/dev.sh "cd betterdesk-server/console/kit/testdata/nodeoracle && node devicetoken.js"
    DEV_DOCKER_ARGS="--network bdgo" tools/dev.sh "cd betterdesk-server/console/kit/testdata/nodeoracle && \
      ORACLE_PG_DSN='postgres://betterdesk:bdgo-local@bdgo-testpg:5432/betterdesk_test?sslmode=disable' node devicetoken.js"

The first writes `../device_sqlite.json`, the second `../device_postgres.json`.
