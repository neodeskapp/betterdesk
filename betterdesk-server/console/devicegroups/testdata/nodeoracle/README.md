# Node oracle for console/devicegroups

`vectors.js` runs three modules of the Node console (`web-nodejs`) on
generated input and records what they answer. The Go tests in
`console/devicegroups` replay every record and must answer the same
(`oracle_test.go`).

This folder is a test fixture for the time both servers exist. It needs
`web-nodejs` and goes away with it. The recorded files in the parent folder
(`*.json.gz`, `counts.json`) stay: they are plain data and keep pinning the
Go code to what Node did.

No database and no network are involved: the functions work on values.

| File | List | What it holds |
|---|---|---|
| `payload` | | `normalizeGroupPayload`, `buildDeviceGroupCreateFields` and `buildDeviceGroupUpdateFields` of `services/deviceGroupService.js` for about 1,900 bodies: every key with every kind of JSON value (numbers at the edges of JavaScript's plain notation, lists, objects, long text, text cut inside an emoji) and random combinations |
| `tags` | `sanitize`, `equal`, `has`, `rename`, `remove` | `sanitizeTagName`, `tagsEqual`, `deviceHasTag`, `rewriteTagsRename` and `rewriteTagsDelete` of `services/tagManagementService.js`: tags as list, as text, as JSON text, with other values mixed in, in letters whose lower case differs between JavaScript and Go (`İ`, a final sigma) |
| | `runs` | 600 whole runs of `renameTagAcrossScope` and `deleteTagAcrossScope` over devices and groups, with the answers the server gives for each device, the calls made, the devices' tags and the groups' filters afterwards |
| `addressbook` | `normalize`, `parse`, `merge`, `filter`, `visible`, `updates` | `normalizeTags`, `parseAddressBookData`, `mergeAddressBookData`, `filterAddressBookPeersByScope`, `collectVisibleTags` and `collectPeerTagUpdates` of `services/rustdeskAddressBookSync.js`, 1,500 books each: as object, as JSON text, as something else, with peers that are no objects, peers without id, banned and deleted devices |

`counts.json` has the number of records per list.

## Running

In the dev image, from the worktree:

    tools/dev.sh "cd betterdesk-server/console/devicegroups/testdata && node nodeoracle/vectors.js"

The generators are seeded, so a second run writes the same files.

## What the Go tests leave out or change

- Where Node cuts a tag in the middle of a character outside the Basic
  Multilingual Plane it keeps half of the character, written `\ud83d` in
  JSON text. A Go string cannot hold one; the Go code writes U+FFFD and the
  test reads Node's text that way (BUGS DGR2).
- Node stores the tags of up to four devices at once. The Go code does one
  after the other. With the answers the oracle gives (each after one turn
  of the event loop) both make the calls in the order of the devices.

## The store (`store.js`)

`store.js` runs the folder, device group and user group methods of
`services/dbAdapter.js` (both adapters) on a scratch database and records
each result as JSON, or the message of the error it threw:
`../store_sqlite.json` and `../store_postgres.json`, 86 calls each.
`TestStoreAgainstNode` replays them on a database with the same rows and
compares the JSON byte for byte, with the times the database fills in and
random guids masked. Arguments are the values a request can carry: text,
numbers, NaN, flags, null, undefined, and lists of those.

    tools/dev.sh "cd betterdesk-server/console/devicegroups/testdata && node nodeoracle/store.js"
    DEV_DOCKER_ARGS="--network bdgo" tools/dev.sh "cd betterdesk-server/console/devicegroups/testdata && \
      ORACLE_PG_DSN='postgres://betterdesk:bdgo-local@bdgo-testpg:5432/betterdesk_test?sslmode=disable' node nodeoracle/store.js"

Differences the test allows: on SQLite the Go driver's message carries the
kind of failure and the code around the engine's text (`constraint failed:
NOT NULL constraint failed: folders.name (1299)`); the test checks that
Node's text is in it. `getUserById` is replayed through `UserIDByID`,
which takes the id as a number, so the text `"2"` is read as 2.
