// Package backup makes and restores the console's backups
// (services/backupService.js and services/backupArchive.js of the old
// console), in the formats the old console wrote, so that a backup of
// either can be restored by the other.
//
// # The full backup
//
// A gzip-compressed ustar file, written and read without the archive/tar
// package: the old reader's quirks decide what an uploaded archive means
// (see ExtractTarGz), and the old writer's header bytes are matched
// exactly. CreateFullBackup lists its entries. The logical dump in
// console/data.json (DumpAllTables) holds every table of the database:
// on SQLite the one file the console and the server share, on PostgreSQL
// every table of the schema, the server's included.
//
// A restore (RestoreFullBackup) unpacks and checks the whole archive
// first and writes nothing when that fails. Then each part runs on its
// own: the dump table by table (a table that fails keeps its rows) with
// the chat files and the ticket attachments, the uploads, the secrets,
// the .env and the server's SQLite file. Parts after a failed one still
// run; the result lists what was restored, skipped and what failed.
//
// Every restore of accounts (the dump, the settings snapshot, the
// server's SQLite file at the next start) ends the sign-in tokens of the
// accounts whose password or second factor it replaced, and of accounts
// that were not there before, through users.credentials_changed_at
// (tokentimes.go).
//
// # The settings snapshot
//
// The older format, one JSON file: settings, branding, accounts, folders,
// groups, rules, address books and what the API server said
// (CreateBackup, ValidateBackup, RestoreBackup).
//
// # Values
//
// The old console handed rows from its database drivers to
// JSON.stringify and bound JSON values to statements with the same
// drivers; the conversions of better-sqlite3 and node-postgres are kept
// on both sides, so the same rows come out and go back in, failures
// included. BUGS.md BK1 to BK15 list where that loses data.
package backup
