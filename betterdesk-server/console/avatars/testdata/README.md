# Recordings of the old console for console/avatars

The files here are fixed records of what the Node console's avatar code
answered, made while it still existed. They can't be recorded again. The
Go tests replay every record and need no Node. Don't edit them: when a fix
changes an answer on purpose, the test lists that record as an intended
difference with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `image.json.gz` | `lib/avatarImage.js`: `sniffImageType`, `parseAvatarDataUrl` and `isRefusedAvatarAddress` | `TestOracleImage` |
| `fetch.json.gz` | `lib/avatarImage.js`: `fetchAvatarUrl`, `loadAvatarPicture` and `imageVersion` | `TestOracleFetch` |
| `store_sqlite.json.gz`, `store_postgres.json.gz` | every call of a scenario of `services/avatarService.js` on a scratch database, per database | `TestOracleStore` (`service_test.go`) |
| `counts.json` | the module's limits (`MAX_AVATAR_BYTES`, `FETCH_TIMEOUT_MS`, `MAX_REDIRECTS`) and how many records each list holds | `TestLimitsAreTheOldOnes` |

## What is recorded

No request left the process: `http.request` and `https.request` were
replaced by a fake that recorded what would be sent (scheme, host, port,
path, headers) and played the answers a record names.

- `sniff`: `sniffImageType` for the signatures, each cut short, each with
  one bit changed, and random bytes.
- `data_url`: `parseAvatarDataUrl` for every declared type with every kind
  of content, both base64 alphabets, padding variants, damaged text, sizes
  around a small limit and around the real one.
- `refused`: `isRefusedAvatarAddress` for about 4,200 texts: the edges of
  every refused range, random IPv4 and IPv6 addresses, the forms that carry
  an IPv4 address, zones, and text that is no address.
- `fetches`: `fetchAvatarUrl` for about 900 addresses (schemes,
  credentials, host names, IPv4 in hex, octal and short forms, IPv6, ports,
  paths with dot segments and characters that get escaped), for answers by
  status, type, size and content, and for redirects by status, `Location`
  and number.
- `loads`: `loadAvatarPicture` by kind of value, with the version and the
  stored source address, and `imageVersion`.

Host names with letters outside ASCII are left out: the Go transport
converts them when it connects, not when the address is read. The
generators were seeded.

The store scenario stores, replaces and removes pictures from both
sources, lets every kind of refusal happen, writes for a user that does not
exist, and moves the clock past the 30 seconds the list of pictures is
kept. Its records hold times of the run; the Go test checks their form,
not their value.
