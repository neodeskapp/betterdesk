# Node oracle for console/backup and console/scheduledbackup

The scripts here run the backup modules of the Node console (`web-nodejs`)
and record what they answer, so the Go packages can be held to it. They are
a test fixture for the time both servers exist: they need `web-nodejs` and
go away with it. What they recorded stays and keeps pinning the Go code.

## vectors.js

Runs `services/backupArchive.js`, `services/backupService.js` and
`services/scheduledBackupService.js` on generated input and writes:

| File | What it holds |
|---|---|
| `../archive.json.gz` | `tar`: the tar file `createTarGz` writes for 400 sets of entries (names short, long, split into the ustar prefix, too long, with backslashes, leading slashes, multi-byte characters; data of every size around the block size; modes). `extract`: what `extractTarGz` returns for 2,500 hand-built tar files (size fields with spaces, signs, garbage, NULs and bytes with the high bit; names with NULs, line breaks and broken UTF-8; types `0`, NUL, `5`, `2`, `L`, `x`; files cut short, duplicate names, missing end blocks). `gunzip`: what `zlib.gunzipSync` returns or throws for 4,000 gzip files, valid and broken (flipped bits, cut short, trailing zeros and garbage, second members, header flags, header CRC, unknown methods, random deflate data). `limits`: `extractTarGz` with the options `maxBytes` and `maxEntries` for 3,007 archives (around the tar file's length and the number of regular files, fractions, entries past the end, the cases of `tests/backupArchive.limits.test.js`). `env`: the module loaded anew with 410 pairs of `BACKUP_MAX_DECOMPRESSED_BYTES` and `BACKUP_MAX_ENTRIES` (unset, empty, signs, fractions, hex, octal, binary, exponents, infinities, text), the limits it took and what it made of eight archives. |
| `../values.json.gz` | `validateFullBackup` for 27 manifests, `validateBackup` for 18 snapshots, `toFixed` of the sizes in the audit texts, `Buffer.from(text, 'base64')` for 2,300 texts. |
| `../../../scheduledbackup/testdata/schedule.json.gz` | `saveConfig`, `getConfig` and `resolveDestination` for 3,300 bodies and stored values; `saveSmbSettings` and `getSmbSettings` for 1,500 bodies over stored settings; `testSmbConnection` for 800 bodies on Linux and Windows with the program call it made and a stub's answer (exit codes, ENOENT, EACCES). |
| `../counts.json` | the number of records per list |

A negative tar size that is a multiple of 512 made the Node reader loop for
ever (BUGS.md BK4), so the generator leaves those out; the Go test
`TestExtractStopsOnALoop` covers them.

    tools/dev.sh "cd betterdesk-server/console/backup/testdata && node nodeoracle/vectors.js"

`archive.json.gz` is recorded from the spec tree of upstream 3.5.264, which
has the archive limits of audit M4 (the module is gone from `web-nodejs`).
Only that part is written from there: `resolveDestination` compares paths
with the folder the module runs from, so the schedule vectors would change
with the mount; `backupService.js` and `scheduledBackupService.js` are
unchanged upstream.

    DEV_DOCKER_ARGS="-v <spec tree>/web-nodejs:/spec/web-nodejs:ro -v bdgo-node-nm:/spec/web-nodejs/node_modules" \
      tools/dev.sh "cd betterdesk-server/console/backup/testdata && WEB_NODEJS=/spec/web-nodejs ORACLE_PARTS=archive node nodeoracle/vectors.js"

## roundtrip.js

Makes and restores full backups, makes the settings snapshot, restores a
snapshot and runs `importAllTables`, all with the real Node services on a
database and folders the Go test prepared (`roundtrip_test.go`,
`differential_test.go`). The API server is stubbed with `goserver.json`;
the console's own `.env` is redirected to a file of the test. The tests
skip without `node` and `web-nodejs/node_modules`; in the dev image they run
on SQLite, and on PostgreSQL with `tools/gotest.sh`.

With `BACKUP_RECORD_FIXTURES=1` the round trip test writes the Node-made
archives and what the Node restore made of them to `../node-archives/`.
`TestNodeArchiveFixtures` restores them with Go, without Node.
