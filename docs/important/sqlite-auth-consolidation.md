# SQLite authentication-store consolidation

BetterDesk versions before the consolidation maintain two SQLite files:

- `db_v2.sqlite3` — BetterDesk server data;
- `auth.db` — legacy Node.js panel users, MFA, tokens and panel state.

The current runtime uses one selected database. On SQLite this is
`db_v2.sqlite3`; on PostgreSQL no SQLite file is created.

## The import at the first start

The web console imports `auth.db` into `db_v2.sqlite3` by itself, at the
first start of a version without the Node.js console. It looks for the file
at `AUTH_DB_PATH`, else `auth.db` in the console's data folder, and imports
it when the file exists, `SQLITE_AUTH_DB_MODE` is not `consolidated` and
`db_v2.sqlite3` has no completion marker yet. Nothing has to be run by hand.

The import:

1. takes snapshots of both files into `backups` next to `db_v2.sqlite3`
   (`db_v2.sqlite3.pre-auth-consolidation-<time>` and
   `auth.db.legacy-auth-<time>`) and checks them;
2. copies the panel data into `db_v2.sqlite3` in one transaction, so it
   applies completely or not at all;
3. matches accounts that exist in both files by name: the account keeps the
   row and id it has in `db_v2.sqlite3` and takes the console's profile,
   second factor and password hash. Other rows whose key `db_v2.sqlite3`
   already has stay as they are, except user groups, device groups and
   strategies, where the console's row wins. Rows that point at an account
   or row that no longer exists are left out and named in the log;
4. ends the signed-in sessions of accounts whose id changed;
5. writes the marker `sqlite_auth_consolidation_v1` into `db_v2.sqlite3`.
   With it, an older version started after a rollback of the binary also
   reads the panel data from `db_v2.sqlite3`.

`auth.db` is only read. It is not changed or deleted. Keep it and the
snapshots until the upgraded service has been verified and the normal
retention period has elapsed.

## Running the import by hand

`-migrate-sqlite-auth` runs the same import without starting the server,
for example to see the result before the upgrade. Stop the BetterDesk
server first, so nothing writes to either file while it runs.

A dry run imports into a copy of `db_v2.sqlite3` in a temporary folder and
leaves both files as they are:

```sh
AUTH_DB_PATH=/opt/BetterDeskConsole/data/auth.db \
  betterdesk-server -db /opt/rustdesk/db_v2.sqlite3 \
  -migrate-sqlite-auth -migrate-sqlite-auth-dry-run

AUTH_DB_PATH=/opt/BetterDeskConsole/data/auth.db \
  betterdesk-server -db /opt/rustdesk/db_v2.sqlite3 \
  -migrate-sqlite-auth \
  -migrate-sqlite-auth-backup-dir /opt/rustdesk/backups
```

The second command imports for real, with the snapshots in the folder
`-migrate-sqlite-auth-backup-dir` names (`backups` next to the database
without it). Both print a JSON report: the accounts merged and added, the
old and new id of every account, the rows copied, kept and left out per
table, the snapshots, and `already_complete: true` when the marker was there
and nothing was imported.

## Verify and rollback

Start BetterDesk and confirm login, MFA, device groups, folders and the
RustDesk address book.

If verification fails before new data is written, stop the server and
restore the snapshot the import named:

```sh
betterdesk-server -db /opt/rustdesk/db_v2.sqlite3 \
  -rollback-sqlite-auth /opt/rustdesk/backups/db_v2.sqlite3.pre-auth-consolidation-YYYYMMDDTHHMMSSZ
```

The failed database is kept next to the active file as
`db_v2.sqlite3.failed-auth-consolidation-<time>`. The restored snapshot has
no marker, so the console imports `auth.db` again at its next start. To stay
on the snapshot without the import, move `auth.db` away or set
`SQLITE_AUTH_DB_MODE=consolidated` first.
