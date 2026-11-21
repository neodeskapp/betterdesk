# Migration Guide

BetterDesk provides migration tools for moving between databases and from legacy RustDesk deployments.

---

## Migration Modes

The migration tool supports 5 modes:

| Mode | Source | Target | Description |
|------|--------|--------|-------------|
| `rust2go` | RustDesk Rust `db_v2.sqlite3` | BetterDesk Go SQLite | Migrate from original RustDesk server |
| `sqlite2pg` | BetterDesk Go SQLite | PostgreSQL | Move to PostgreSQL for production |
| `pg2sqlite` | PostgreSQL | BetterDesk Go SQLite | Reverse migration for backup/testing |
| `nodejs2go` | Node.js console SQLite | BetterDesk Go SQLite | Copy a few Node.js console tables (to upgrade such an install, see below) |
| `backup` | Any SQLite | SQLite copy | Create timestamped backup |

---

## Using the ALL-IN-ONE Script

The simplest way to migrate:

```bash
# Linux
sudo ./betterdesk.sh
# Choose option M — Migrate databases

# Windows
.\betterdesk.ps1
# Choose option M — Migrate databases
```

The script:
1. Detects current database configuration
2. Offers available migration paths
3. Creates automatic backup before migration
4. Runs the migration tool
5. Updates `.env` and service files
6. Restarts services

---

## Using the Migration Tool Directly

### Binary Location

```bash
# Linux
./betterdesk-server/tools/migrate/migrate-linux-amd64

# Or compile from source
cd betterdesk-server/tools/migrate
go build -o migrate .
```

### Command Line

```bash
./migrate -mode <mode> -src <source> -dst <destination> [options]
```

### Examples

#### RustDesk → BetterDesk Go

```bash
./migrate -mode rust2go \
  -src /path/to/rustdesk/db_v2.sqlite3 \
  -dst /opt/betterdesk/db_v2.sqlite3
```

This handles schema differences:
- `peer` table → `peers` table
- Column name mapping
- UUID generation for entries without UUIDs

#### SQLite → PostgreSQL

```bash
./migrate -mode sqlite2pg \
  -src /opt/betterdesk/db_v2.sqlite3 \
  -dst "postgres://betterdesk:password@localhost:5432/betterdesk?sslmode=disable"
```

Migrated data:
- All peers with status, tags, notes
- Users with roles and TOTP config
- API keys
- Server config
- Audit log entries
- Address books
- Access policies

#### PostgreSQL → SQLite

```bash
./migrate -mode pg2sqlite \
  -src "postgres://betterdesk:password@localhost:5432/betterdesk" \
  -dst /opt/betterdesk/db_v2.sqlite3
```

Useful for creating portable backups or moving to a smaller deployment.

#### Install with the Node.js console

An install that still runs the Node.js web console does not need the migration tool. Run the installer's update instead:

```bash
sudo ./betterdesk.sh --update     # Linux
.\betterdesk.ps1 -Update          # Windows
```

The update moves the install to the Go server in place and keeps all data, keys and accounts. Roll it back with `sudo ./betterdesk.sh --rollback-go` or `.\betterdesk.ps1 -RollbackGo`.

The tool's `nodejs2go` mode copies only a few tables, so it is no substitute for the update.

---

## Data Preservation

The migration tool preserves:

| Data | rust2go | sqlite2pg | pg2sqlite | nodejs2go |
|------|---------|-----------|-----------|-----------|
| **Peers** | ✅ | ✅ | ✅ | ✅ |
| **Ed25519 keys** | ✅ | ✅ | ✅ | N/A |
| **UUIDs** | ✅ (generated) | ✅ | ✅ | ✅ (generated) |
| **ID history** | ✅ | ✅ | ✅ | N/A |
| **Bans** | ✅ | ✅ | ✅ | N/A |
| **Tags** | ✅ | ✅ | ✅ | ✅ |
| **Notes** | N/A | ✅ | ✅ | ✅ |
| **Users** | N/A | ✅ | ✅ | ✅ |
| **API keys** | N/A | ✅ | ✅ | N/A |
| **Audit log** | N/A | ✅ | ✅ | N/A |
| **Address books** | N/A | ✅ | ✅ | N/A |
| **Access policies** | N/A | ✅ | ✅ | N/A |

---

## Docker Migration

### Migrate from RustDesk Docker

```bash
./betterdesk-docker.sh
# Choose option M — Migrate from RustDesk Docker
```

The script:
1. Detects existing RustDesk containers and volumes
2. Copies `db_v2.sqlite3` and `id_ed25519` from old volumes
3. Runs `rust2go` migration
4. Creates BetterDesk containers with migrated data

### Migrate Docker to PostgreSQL

```bash
./betterdesk-docker.sh
# Choose option P — Migrate to PostgreSQL
```

Adds a PostgreSQL container, runs `sqlite2pg` migration, and updates services.

---

## Upgrading from RustDesk Rust Server

If you're running the original RustDesk hbbs+hbbr:

### Step 1: Stop Old Services

```bash
sudo systemctl stop rustdesksignal rustdeskrelay
# or
sudo systemctl stop hbbs hbbr
```

### Step 2: Install BetterDesk

```bash
sudo ./betterdesk.sh
# Choose option 1 — New installation
```

### Step 3: Migrate Data

```bash
sudo ./betterdesk.sh
# Choose option M — Migrate databases
# Select "Rust → Go" migration
```

### Step 4: Verify

```bash
# Check device count (the stats route needs the server's API key)
curl -H "X-API-Key: $(sudo cat /opt/betterdesk/.api_key)" http://localhost:21114/api/server/stats
```

Then sign in to the web console at `http://your-server:5000` and check the device list.

### Step 5: Clean Up

```bash
# Remove old services (the installer does this automatically)
sudo systemctl disable rustdesksignal rustdeskrelay
sudo rm /etc/systemd/system/rustdesk*.service
```

---

## Backup Before Migration

Always create a backup before migrating:

```bash
# Using the installer
sudo ./betterdesk.sh
# Choose option 5 — Backup

# Manual backup (SQLite)
cp /opt/betterdesk/db_v2.sqlite3 /opt/betterdesk/db_v2.sqlite3.backup
cp /opt/BetterDeskConsole/.env /opt/BetterDeskConsole/.env.backup
```

Devices, accounts, groups and console settings are all in `db_v2.sqlite3`. An install that began with the older split layout may still have `/opt/BetterDeskConsole/data/auth.db`. The server copies it into `db_v2.sqlite3` once, at its first start, and only reads it, so back it up as well if it is there.

---

## Troubleshooting Migration

### "Migration tool not found"

```bash
# If Go is installed, compile from source
cd betterdesk-server/tools/migrate
go build -o migrate .
```

### "Outdated migration binary"

The installer checks if the binary supports the `-mode` flag. If not:

```bash
# Rebuild
cd betterdesk-server/tools/migrate
go build -o migrate .
```

### PostgreSQL connection refused

```bash
# Check PostgreSQL is running
sudo systemctl status postgresql

# Test connection
psql "postgres://betterdesk:password@localhost:5432/betterdesk"

# Check firewall
sudo ufw allow 5432/tcp
```

### PostgreSQL authentication failed during local installation

On Rocky Linux, RHEL, and similar distributions, PostgreSQL may be running while its default `pg_hba.conf` does not permit the BetterDesk role to authenticate over localhost. A current native installer adds narrow loopback rules automatically. For an older installer or a manually managed database, find the active file and add rules for the BetterDesk database and role:

```bash
sudo -u postgres psql -Atqc 'SHOW hba_file;'
sudoedit /path/from/show/hba_file
```

Add these entries before broader localhost rules, then reload PostgreSQL:

```text
host    betterdesk    betterdesk    127.0.0.1/32    scram-sha-256
host    betterdesk    betterdesk    ::1/128         scram-sha-256
```

```bash
sudo systemctl reload postgresql
```

The rules should remain limited to the BetterDesk database, role, and loopback addresses. Do not replace them with `trust` or a network-wide rule.

### Duplicate key errors

If migrating to a database that already has data, the migration tool uses `INSERT OR IGNORE` (SQLite) or `ON CONFLICT DO NOTHING` (PostgreSQL) to skip duplicates.

---

## See also

- [[Installation]] — PostgreSQL setup
- [[Troubleshooting]] — migration failures
- [Server migration guide](https://github.com/UNITRONIX/BetterDesk/blob/main/docs/troubleshooting/SERVER_MIGRATION.md)
