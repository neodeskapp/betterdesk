# Recordings of the old console for console/devicegroups

The files here are fixed records of what the Node console's device group,
tag and address book code answered, made while it still existed. They
can't be recorded again. The Go tests replay every record and need no
Node. Don't edit them: when a fix changes an answer on purpose, the
fixed answer goes into `<name>_fixed.json` (written by the test with
`DEVICEGROUPS_RECORD_FIXED=1`, with the row id; see `fixedVectors` in
`oracle_test.go`).

| File | What it holds | Replayed by |
|---|---|---|
| `payload.json.gz` | `normalizeGroupPayload`, `buildDeviceGroupCreateFields` and `buildDeviceGroupUpdateFields` of `services/deviceGroupService.js` for about 1,900 bodies | `TestOraclePayload` |
| `tags.json.gz` | the tag functions of `services/tagManagementService.js`, and 600 whole runs of renaming and deleting a tag | `TestOracleTags` |
| `tags_fixed.json` | written by the Go test: the answers the fix DGR4 changed | `TestOracleTags` |
| `addressbook.json.gz` | the functions of `services/rustdeskAddressBookSync.js`, 1,500 books each | `TestOracleAddressBook` |
| `counts.json` | how many records each list holds | none |
| `store_sqlite.json`, `store_postgres.json` | 118 calls each of the folder, device group and user group methods of `services/dbAdapter.js` | `TestStoreAgainstNode` (`store_oracle_test.go`) |
| `peers_sqlite.json`, `peers_postgres.json` | `getPeerStats` and `getAllPeers` with the copy of the server's devices they made first, and the rows afterwards | `TestPeerSyncAgainstNode` (`peers_test.go`) |

## The vectors

No database and no network were involved: the functions work on values.
The generators were seeded.

| File | List | What it holds |
|---|---|---|
| `payload` | | bodies with every key with every kind of JSON value (numbers at the edges of JavaScript's plain notation, lists, objects, long text, text cut inside an emoji) and random combinations |
| `tags` | `sanitize`, `equal`, `has`, `rename`, `remove` | `sanitizeTagName`, `tagsEqual`, `deviceHasTag`, `rewriteTagsRename` and `rewriteTagsDelete`: tags as list, as text, as JSON text, with other values mixed in, in letters whose lower case differs between JavaScript and Go (`İ`, a final sigma) |
| | `runs` | 600 whole runs of `renameTagAcrossScope` and `deleteTagAcrossScope` over devices and groups, with the answers the server gave for each device, the calls made, the devices' tags and the groups' filters afterwards |
| `addressbook` | `normalize`, `parse`, `merge`, `filter`, `visible`, `updates` | `normalizeTags`, `parseAddressBookData`, `mergeAddressBookData`, `filterAddressBookPeersByScope`, `collectVisibleTags` and `collectPeerTagUpdates`: books as object, as JSON text, as something else, with peers that are no objects, peers without id, banned and deleted devices |

What the Go tests leave out or change:

- Where Node cut a tag in the middle of a character outside the Basic
  Multilingual Plane it kept half of the character, written `\ud83d` in
  JSON text. A Go string cannot hold one; the Go code writes U+FFFD and the
  test reads Node's text that way (BUGS DGR2).
- Node stored the tags of up to four devices at once. The Go code does one
  after the other. With the answers of the recording (each after one turn
  of the event loop) both make the calls in the order of the devices.

## The store

The store recordings hold each result of the adapter methods (both
adapters, user group memberships included) on a scratch database as JSON,
or the message of the error it threw. The test replays them on a database
with the same rows and compares the JSON byte for byte, with the times the
database fills in and random guids masked. Arguments are the values a
request can carry: text, numbers, NaN, flags, null, undefined, and lists
of those.

Differences the test allows: on SQLite the Go driver's message carries the
kind of failure and the code around the engine's text (`constraint failed:
NOT NULL constraint failed: folders.name (1299)`); the test checks that
Node's text is in it. `getUserById` is replayed through `UserIDByID`,
which takes the id as a number, so the text `"2"` is read as 2.

## The copy of the server's devices

The peers recordings come from the two reads of the adapter that brought
the console's device table (`peer`) up to date with the signal server's
(`peers`) before they read it (`syncGoPeers` with its ghost clean-up and
the id changes it carried over), on a scratch database with the server's
tables as the server creates them. They hold what the reads returned and
the rows of `peer` and its side tables afterwards. The test replays them
on the same rows and compares the JSON byte for byte.
