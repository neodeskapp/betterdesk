# Recordings of the old console for console/backup and console/scheduledbackup

The files below are fixed records of what the Node console's backup code
did, made while it still existed. They can't be recorded again. The Go
tests replay them and need no Node. Don't edit them: when a fix changes an
answer on purpose, the test lists that record as an intended difference
with the row id (for the archives: `refusedByNodeOnly` and `fixtureTables`
in `roundtrip_test.go`).

| File | What it holds | Replayed by |
|---|---|---|
| `archive.json.gz` | the tar writer and reader, gunzip, the archive limits and their settings | `TestTarMatchesNodeByteForByte`, `TestExtractMatchesNode`, `TestGunzipMatchesZlib`, `TestExtractLimitsMatchNode`, `TestEnvLimitsMatchNode` (`vectors_test.go`) |
| `values.json.gz` | backup validation, sizes in the audit texts, base64 | `TestValidationMatchesNode`, `TestNumbersAndBase64MatchNode` |
| `../../scheduledbackup/testdata/schedule.json.gz` | the schedule and SMB settings | `console/scheduledbackup` (`vectors_test.go`) |
| `counts.json` | the number of records per list | none |
| `node-archives/sqlite.tar.gz`, `node-archives/postgres.tar.gz` | full backups the Node console made of the install `seed` builds | `TestNodeArchiveFixtures` (`roundtrip_test.go`) |
| `node-archives/sqlite.restored.json`, `node-archives/postgres.restored.json` | what the Node console's own restore made of them: its answer and every table | `TestNodeArchiveFixtures` |
| `goserver.json` | what the stub of the API server answered while the archives were made | `fakeGoServer` (`roundtrip_test.go`) |

## What is recorded

The vectors came from `services/backupArchive.js`, `services/backupService.js`
and `services/scheduledBackupService.js`, run on generated input:

- `archive.json.gz`: `tar` is the tar file `createTarGz` wrote for 400 sets
  of entries (names short, long, split into the ustar prefix, too long,
  with backslashes, leading slashes, multi-byte characters; data of every
  size around the block size; modes). `extract` is what `extractTarGz`
  returned for 2,500 hand-built tar files (size fields with spaces, signs,
  garbage, NULs and bytes with the high bit; names with NULs, line breaks
  and broken UTF-8; types `0`, NUL, `5`, `2`, `L`, `x`; files cut short,
  duplicate names, missing end blocks). `gunzip` is what `zlib.gunzipSync`
  returned or threw for 4,000 gzip files, valid and broken. `limits` is
  `extractTarGz` with `maxBytes` and `maxEntries` for 3,007 archives.
  `env` is the module loaded anew with 410 pairs of
  `BACKUP_MAX_DECOMPRESSED_BYTES` and `BACKUP_MAX_ENTRIES`, the limits it
  took and what it made of eight archives. This file came from upstream
  3.5.264, which has the archive limits of audit M4.
- `values.json.gz`: `validateFullBackup` for 27 manifests, `validateBackup`
  for 18 snapshots, `toFixed` of the sizes in the audit texts, and
  `Buffer.from(text, 'base64')` for 2,300 texts.
- `schedule.json.gz`: `saveConfig`, `getConfig` and `resolveDestination`
  for 3,300 bodies and stored values; `saveSmbSettings` and
  `getSmbSettings` for 1,500 bodies over stored settings;
  `testSmbConnection` for 800 bodies on Linux and Windows with the program
  call it made and a stub's answer (exit codes, ENOENT, EACCES).

A negative tar size that is a multiple of 512 made the Node reader loop
for ever (BUGS.md BK4), so the generator left those out;
`TestExtractStopsOnALoop` covers them.

The archives were made by the Node console's backup service from the
install that `seed` in `roundtrip_test.go` builds, with the API server
answered by `goserver.json` and the console's `.env` redirected to a file
of the test, on SQLite and on PostgreSQL. The `.restored.json` files hold
what the Node console's restore of each archive answered and every table
it left behind.
