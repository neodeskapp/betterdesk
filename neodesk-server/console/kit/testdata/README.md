# Recordings of the old console for console/kit

The JSON files here are fixed records of what the Node console answered,
made while it still existed. They can't be recorded again. The Go tests
replay them and need no Node. Don't edit them: when a fix changes an
answer on purpose, the test lists that record as an intended difference
with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `device_sqlite.json`, `device_postgres.json` | `requireDeviceToken` and `requireTokenDeviceMatch` of `middleware/deviceAuth.js` on raw requests, per database | `TestDeviceGuardsAnswerAsNode` (`device_test.go`) |
| `setcookie_vectors.json` | the `Set-Cookie` headers of Express's `res.cookie(name, value, options)` for the options the console uses | `TestSetCookieMatchesExpress` (`cookies_test.go`) |

## The device guards

The two guards ran with the real database facade in a small Express app
with the console's body parsers and its error answer, on a scratch
database with token rows in every state: valid, bound to another device,
expired, revoked, bound to no device or to an id of the wrong form, found
by its hash only, found by the token text only, stored with the hash of
another text, with a non-ASCII token, and one whose use could not be noted
(a trigger refused the update). The requests were raw HTTP: the header in
its many spellings, and the device id as a claim in the body (JSON of
every type, forms), the query (as Express parsed it: lists, objects,
repeats) and `X-Device-Id`, in every mix. At the end the token table was
renamed, so that the lookup itself failed.

For each request the recording holds the bytes sent, the status and body
of the answer, and the token rows whose use was noted. The Go test sends
the same bytes through `kit.Deps.RequireDeviceToken` and
`RequireTokenDeviceMatch` behind the console's body step, on a database
with the same rows, and must get the same.

In a recording a character of `raw` stands for one byte.
