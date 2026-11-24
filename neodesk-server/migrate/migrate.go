// Package migrate is the server's database migration tool, the command
// neodesk-server migrate (Main).
//
// Supports multiple migration modes:
//
//	rust2go   — Rust hbbs-patch-v2 SQLite → NeoDesk Go schema (SQLite or PG)
//	sqlite2pg — move a whole install (server and console) from SQLite to PostgreSQL
//	pg2sqlite — move a whole install from PostgreSQL to SQLite
//	backup    — Create timestamped backup of a SQLite file
//
// Usage:
//
//	neodesk-server migrate -mode rust2go   -src /opt/rustdesk/db_v2.sqlite3
//	neodesk-server migrate -mode rust2go   -src db_v2.sqlite3 -dst postgres://user:pass@host/db
//	neodesk-server migrate -mode sqlite2pg -src ./db_v2.sqlite3 -dst postgres://user:pass@host/db
//	neodesk-server migrate -mode pg2sqlite -src postgres://user:pass@host/db -dst ./db_v2.sqlite3
//	neodesk-server migrate -mode backup    -src db_v2.sqlite3
//
// An existing SQLite file at -dst is replaced only with -overwrite, and
// never when it is one of the sources. sqlite2pg writes into a PostgreSQL
// database that has rows only with -overwrite.
package migrate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "modernc.org/sqlite"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// ── Types ─────────────────────────────────────────────────────────────

// rustPeer represents a row from the Rust hbbs-patch-v2 database.
type rustPeer struct {
	GUID        []byte
	ID          string
	UUID        []byte
	PK          []byte
	CreatedAt   string
	User        []byte
	Status      *int64
	Note        *string
	Info        string
	PreviousIDs *string
	IDChangedAt *string
	IsDeleted   *int64
	IsBanned    *int64
	LastOnline  *string
}

// peerInfo is the JSON structure stored in the Rust "info" field.
type peerInfo struct {
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	Version  string `json:"version"`
	Username string `json:"username"`
}

// ── Main / CLI ────────────────────────────────────────────────────────

// Main runs the tool with the arguments after the command's name and
// returns the exit status: 0 when the mode ran (and for -h, which prints
// the usage), 1 when it failed or names no mode, 2 for flags it cannot
// read. The usage goes to stdout, flag errors to stderr, and what the
// modes do to the log.
func Main(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("neodesk-server migrate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { printUsage(stdout) }
	mode := flags.String("mode", "", "Migration mode: rust2go, sqlite2pg, pg2sqlite, backup")
	src := flags.String("src", "", "Source: SQLite file path or postgres:// DSN")
	dst := flags.String("dst", "", "Destination: SQLite file path or postgres:// DSN (default: auto)")
	backupOnly := flags.Bool("backup-only", false, "Create backup only (legacy flag, same as -mode backup)")
	overwrite := flags.Bool("overwrite", false, "Replace an existing SQLite file at -dst, or everything in the PostgreSQL database at -dst")
	skipLeftovers := flags.Bool("skip-leftovers", false, "sqlite2pg, pg2sqlite: leave out source tables and columns the server's schema does not have, instead of failing")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	// Legacy compatibility: -src without -mode assumes rust2go
	if *mode == "" && *src != "" {
		if *backupOnly {
			*mode = "backup"
		} else {
			*mode = "rust2go"
		}
	}

	var err error
	switch *mode {
	case "rust2go":
		err = runRust2Go(*src, *dst, *overwrite)
	case "sqlite2pg":
		err = runSQLite2PG(*src, *dst, moveOptions{overwrite: *overwrite, skipLeftovers: *skipLeftovers})
	case "pg2sqlite":
		err = runPG2SQLite(*src, *dst, moveOptions{overwrite: *overwrite, skipLeftovers: *skipLeftovers})
	case "backup":
		if *src == "" {
			err = errors.New("usage: neodesk-server migrate -mode backup -src <path>")
			break
		}
		var bp string
		if bp, err = createBackup(*src); err == nil {
			log.Printf("Backup created: %s", bp)
		}
	default:
		printUsage(stdout)
		return 1
	}
	if err != nil {
		log.Print(err)
		return 1
	}
	return 0
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "NeoDesk Database Migration Tool")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage: neodesk-server migrate -mode <mode> -src <source> [-dst <dest>] [-overwrite] [-skip-leftovers] [options]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Modes:")
	fmt.Fprintln(w, "  rust2go   - Migrate Rust hbbs-patch-v2 → NeoDesk Go schema")
	fmt.Fprintln(w, "  sqlite2pg - Move the whole install (every table of the server and the console) from SQLite to PostgreSQL")
	fmt.Fprintln(w, "  pg2sqlite - Move the whole install from PostgreSQL to SQLite")
	fmt.Fprintln(w, "  backup    - Create timestamped backup of a SQLite file")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "A SQLite destination is written to a temporary file and renamed at the end.")
	fmt.Fprintln(w, "An existing file at -dst is replaced only with -overwrite; a source is never.")
	fmt.Fprintln(w, "sqlite2pg writes into a PostgreSQL database with rows in it only with -overwrite,")
	fmt.Fprintln(w, "and both moves stop on tables or columns the server's schema lacks unless -skip-leftovers.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Examples:")
	fmt.Fprintln(w, "  neodesk-server migrate -mode rust2go   -src /opt/rustdesk/db_v2.sqlite3")
	fmt.Fprintln(w, "  neodesk-server migrate -mode sqlite2pg -src ./db_v2.sqlite3 -dst postgres://user:pass@host/neodesk")
	fmt.Fprintln(w, "  neodesk-server migrate -mode pg2sqlite -src postgres://user:pass@host/neodesk -dst ./db_v2.sqlite3")
	fmt.Fprintln(w, "  neodesk-server migrate -mode backup    -src db_v2.sqlite3")
}

// isPG returns true if the DSN looks like a PostgreSQL connection string.
func isPG(dsn string) bool {
	lower := strings.ToLower(dsn)
	return strings.HasPrefix(lower, "postgres://") || strings.HasPrefix(lower, "postgresql://")
}

// ── Mode: rust2go ─────────────────────────────────────────────────────

func runRust2Go(srcPath, dstPath string, overwrite bool) error {
	if srcPath == "" {
		return errors.New("usage: neodesk-server migrate -mode rust2go -src <rust-db-path> [-dst <dest>] [-overwrite]")
	}
	if err := requireFile(srcPath); err != nil {
		return err
	}
	if dstPath == "" {
		dstPath = filepath.Join(filepath.Dir(srcPath), "neodesk.sqlite3")
	}
	if !isPG(dstPath) {
		if err := checkSQLiteDst(dstPath, overwrite, srcPath); err != nil {
			return err
		}
	}
	bp, err := createBackup(srcPath)
	if err != nil {
		return err
	}
	log.Printf("Backup created: %s", bp)

	peers, err := readRustDB(srcPath)
	if err != nil {
		return fmt.Errorf("read source database: %w", err)
	}
	log.Printf("Read %d peers from Rust database", len(peers))

	if isPG(dstPath) {
		count, err := writeRustPeersPG(dstPath, peers)
		if err != nil {
			return fmt.Errorf("write to PostgreSQL: %w", err)
		}
		log.Printf("Migration complete: %d peers copied to PostgreSQL, %d already there and left as they were", count.copied, count.existed)
		return nil
	}
	count, err := writeRustPeersSQLite(dstPath, overwrite, peers)
	if err != nil {
		return fmt.Errorf("write to SQLite: %w", err)
	}
	log.Printf("Migration complete: %d peers → %s", count, dstPath)
	return nil
}

// readRustDB reads all peers from the old Rust hbbs-patch-v2 database.
func readRustDB(path string) ([]rustPeer, error) {
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(5000)", path))
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	defer db.Close()

	var tableName string
	err = db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='peer'").Scan(&tableName)
	if err != nil {
		return nil, fmt.Errorf("source database has no 'peer' table — is this a Rust hbbs database? %w", err)
	}

	cols := getColumns(db, "peer")
	hasPreviousIDs := contains(cols, "previous_ids")
	hasIDChangedAt := contains(cols, "id_changed_at")
	hasIsDeleted := contains(cols, "is_deleted")
	hasIsBanned := contains(cols, "is_banned")
	hasLastOnline := contains(cols, "last_online")

	query := "SELECT guid, id, uuid, pk, created_at, user, status, note, info"
	if hasPreviousIDs {
		query += ", previous_ids"
	}
	if hasIDChangedAt {
		query += ", id_changed_at"
	}
	if hasIsDeleted {
		query += ", is_deleted"
	}
	if hasIsBanned {
		query += ", is_banned"
	}
	if hasLastOnline {
		query += ", last_online"
	}
	query += " FROM peer"

	rows, err := db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("query peers: %w", err)
	}
	defer rows.Close()

	var peers []rustPeer
	for rows.Next() {
		var p rustPeer
		scanArgs := []any{&p.GUID, &p.ID, &p.UUID, &p.PK, &p.CreatedAt, &p.User, &p.Status, &p.Note, &p.Info}
		if hasPreviousIDs {
			scanArgs = append(scanArgs, &p.PreviousIDs)
		}
		if hasIDChangedAt {
			scanArgs = append(scanArgs, &p.IDChangedAt)
		}
		if hasIsDeleted {
			scanArgs = append(scanArgs, &p.IsDeleted)
		}
		if hasIsBanned {
			scanArgs = append(scanArgs, &p.IsBanned)
		}
		if hasLastOnline {
			scanArgs = append(scanArgs, &p.LastOnline)
		}
		if err := rows.Scan(scanArgs...); err != nil {
			return nil, fmt.Errorf("read peer row %d: %w", len(peers)+1, err)
		}
		peers = append(peers, p)
	}
	return peers, rows.Err()
}

// writeRustPeersSQLite writes Rust peers to a new NeoDesk Go SQLite
// database at path.
func writeRustPeersSQLite(path string, overwrite bool, peers []rustPeer) (int, error) {
	return writeSQLiteFile(path, overwrite, func(tmp string) (int, error) {
		return fillSQLite(tmp, func(db *sql.DB) (int, error) {
			return copyRustPeers2SQLite(db, peers)
		})
	})
}

func copyRustPeers2SQLite(db *sql.DB, peers []rustPeer) (int, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO peers
		(id, uuid, pk, user, hostname, os, version, status, last_online, created_at,
		 banned, soft_deleted, note)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	count := 0
	for _, p := range peers {
		info, uuid, status, user, lastOnline, note, banned, deleted := convertRustPeer(p)
		_, err := stmt.Exec(p.ID, uuid, p.PK, user, info.Hostname, info.OS, info.Version,
			status, lastOnline, p.CreatedAt, banned, deleted, note)
		if err != nil {
			return count, fmt.Errorf("peer %s: %w", p.ID, err)
		}
		count++
	}
	return count, tx.Commit()
}

// insertCount is what a copy into PostgreSQL did with the rows it read: those
// it inserted, and those it left as they were because a row with their
// key was there already (ON CONFLICT DO NOTHING).
type insertCount struct {
	copied, existed int
}

// note counts one row by the rows its insert affected.
func (c *insertCount) note(affected int64) {
	if affected == 0 {
		c.existed++
	} else {
		c.copied++
	}
}

// add counts the rows of another copy.
func (c *insertCount) add(o insertCount) {
	c.copied += o.copied
	c.existed += o.existed
}

// read is how many rows were counted.
func (c insertCount) read() int { return c.copied + c.existed }

// writeRustPeersPG writes Rust peers to a NeoDesk Go PostgreSQL database.
func writeRustPeersPG(dsn string, peers []rustPeer) (insertCount, error) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return insertCount{}, fmt.Errorf("connect: %w", err)
	}
	defer pool.Close()

	for _, stmt := range pgSchemaStatements() {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			return insertCount{}, fmt.Errorf("schema: %w", err)
		}
	}

	var count insertCount
	for _, p := range peers {
		info, uuid, status, user, lastOnline, note, banned, deleted := convertRustPeer(p)
		tag, err := pool.Exec(ctx, `
			INSERT INTO peers (id, uuid, pk, "user", hostname, os, version,
			                   status, last_online, created_at, banned, soft_deleted, note)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
			ON CONFLICT (id) DO NOTHING`,
			p.ID, uuid, p.PK, user, info.Hostname, info.OS, info.Version,
			status, parseTime(lastOnline), parseTime(p.CreatedAt),
			banned != 0, deleted != 0, note)
		if err != nil {
			return count, fmt.Errorf("peer %s: %w", p.ID, err)
		}
		count.note(tag.RowsAffected())
	}
	return count, nil
}

// convertRustPeer extracts common fields from a Rust peer struct.
func convertRustPeer(p rustPeer) (info peerInfo, uuid, status, user, lastOnline, note string, banned, deleted int) {
	if p.Info != "" {
		_ = json.Unmarshal([]byte(p.Info), &info)
	}
	uuid = fmt.Sprintf("%x", p.UUID)
	status = "OFFLINE"
	if p.Status != nil && *p.Status == 1 {
		status = "ONLINE"
	}
	if p.User != nil {
		user = string(p.User)
	}
	if p.LastOnline != nil {
		lastOnline = *p.LastOnline
	}
	if p.Note != nil {
		note = *p.Note
	}
	if p.IsBanned != nil && *p.IsBanned == 1 {
		banned = 1
	}
	if p.IsDeleted != nil && *p.IsDeleted == 1 {
		deleted = 1
	}
	return
}

// ── Schema Definitions ────────────────────────────────────────────────

// pgSchemaStatements returns PostgreSQL DDL for all NeoDesk Go tables.
func pgSchemaStatements() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS peers (
			id            TEXT PRIMARY KEY,
			uuid          TEXT NOT NULL DEFAULT '',
			pk            BYTEA DEFAULT NULL,
			ip            TEXT NOT NULL DEFAULT '',
			"user"        TEXT NOT NULL DEFAULT '',
			hostname      TEXT NOT NULL DEFAULT '',
			os            TEXT NOT NULL DEFAULT '',
			version       TEXT NOT NULL DEFAULT '',
			status        TEXT NOT NULL DEFAULT 'OFFLINE',
			nat_type      INTEGER NOT NULL DEFAULT 0,
			last_online   TIMESTAMPTZ,
			created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			disabled      BOOLEAN NOT NULL DEFAULT FALSE,
			banned        BOOLEAN NOT NULL DEFAULT FALSE,
			ban_reason    TEXT NOT NULL DEFAULT '',
			banned_at     TIMESTAMPTZ,
			soft_deleted  BOOLEAN NOT NULL DEFAULT FALSE,
			deleted_at    TIMESTAMPTZ,
			note          TEXT NOT NULL DEFAULT '',
			tags          TEXT NOT NULL DEFAULT '',
			heartbeat_seq BIGINT NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_peers_uuid ON peers(uuid)`,
		`CREATE INDEX IF NOT EXISTS idx_peers_status ON peers(status)`,
		`CREATE INDEX IF NOT EXISTS idx_peers_banned ON peers(banned) WHERE banned = TRUE`,

		`CREATE TABLE IF NOT EXISTS server_config (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL DEFAULT ''
		)`,

		`CREATE TABLE IF NOT EXISTS id_change_history (
			id         BIGSERIAL PRIMARY KEY,
			old_id     TEXT NOT NULL,
			new_id     TEXT NOT NULL,
			changed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			reason     TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_id_history_old ON id_change_history(old_id)`,
		`CREATE INDEX IF NOT EXISTS idx_id_history_new ON id_change_history(new_id)`,

		`CREATE TABLE IF NOT EXISTS users (
			id            BIGSERIAL PRIMARY KEY,
			username      TEXT UNIQUE NOT NULL,
			password_hash TEXT NOT NULL,
			role          TEXT NOT NULL DEFAULT 'viewer',
			totp_secret   TEXT NOT NULL DEFAULT '',
			totp_enabled  BOOLEAN NOT NULL DEFAULT FALSE,
			created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			last_login    TIMESTAMPTZ
		)`,

		`CREATE TABLE IF NOT EXISTS api_keys (
			id         BIGSERIAL PRIMARY KEY,
			key_hash   TEXT UNIQUE NOT NULL,
			key_prefix TEXT NOT NULL DEFAULT '',
			name       TEXT NOT NULL DEFAULT '',
			role       TEXT NOT NULL DEFAULT 'viewer',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			expires_at TIMESTAMPTZ,
			last_used  TIMESTAMPTZ
		)`,
	}
}

// sqliteSchemaStatements returns SQLite DDL for all NeoDesk Go tables.
func sqliteSchemaStatements() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS peers (
			id TEXT PRIMARY KEY,
			uuid TEXT DEFAULT '',
			pk BLOB DEFAULT NULL,
			ip TEXT DEFAULT '',
			user TEXT DEFAULT '',
			hostname TEXT DEFAULT '',
			os TEXT DEFAULT '',
			version TEXT DEFAULT '',
			status TEXT DEFAULT 'OFFLINE',
			nat_type INTEGER DEFAULT 0,
			last_online TEXT DEFAULT '',
			created_at TEXT DEFAULT (datetime('now')),
			disabled INTEGER DEFAULT 0,
			banned INTEGER DEFAULT 0,
			ban_reason TEXT DEFAULT '',
			banned_at TEXT DEFAULT NULL,
			soft_deleted INTEGER DEFAULT 0,
			deleted_at TEXT DEFAULT NULL,
			note TEXT DEFAULT '',
			tags TEXT DEFAULT '',
			heartbeat_seq INTEGER DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_peers_uuid ON peers(uuid)`,
		`CREATE INDEX IF NOT EXISTS idx_peers_status ON peers(status)`,
		`CREATE INDEX IF NOT EXISTS idx_peers_banned ON peers(banned)`,

		`CREATE TABLE IF NOT EXISTS server_config (
			key TEXT PRIMARY KEY,
			value TEXT DEFAULT ''
		)`,

		`CREATE TABLE IF NOT EXISTS id_change_history (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			old_id TEXT NOT NULL,
			new_id TEXT NOT NULL,
			changed_at TEXT DEFAULT (datetime('now')),
			reason TEXT DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_id_history_old ON id_change_history(old_id)`,
		`CREATE INDEX IF NOT EXISTS idx_id_history_new ON id_change_history(new_id)`,

		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT UNIQUE NOT NULL,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'viewer',
			totp_secret TEXT DEFAULT '',
			totp_enabled INTEGER DEFAULT 0,
			created_at TEXT DEFAULT (datetime('now')),
			last_login TEXT DEFAULT ''
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_users_username ON users(username)`,

		`CREATE TABLE IF NOT EXISTS api_keys (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			key_hash TEXT UNIQUE NOT NULL,
			key_prefix TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL DEFAULT '',
			role TEXT NOT NULL DEFAULT 'viewer',
			created_at TEXT DEFAULT (datetime('now')),
			expires_at TEXT DEFAULT '',
			last_used TEXT DEFAULT ''
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_api_keys_hash ON api_keys(key_hash)`,
	}
}

// ── Backup & Utilities ────────────────────────────────────────────────

// createBackup writes a backup of the SQLite database at srcPath next to it,
// named with a timestamp. The backup is one self-contained file made with
// VACUUM INTO, a consistent snapshot even while a server writes to the
// source and its -wal. It holds password hashes and secrets, so it gets
// the source's mode with everything beyond 0600 taken away. An existing
// file of the same name is never overwritten, and when a step fails the
// partial backup is removed.
func createBackup(srcPath string) (string, error) {
	info, err := os.Stat(srcPath)
	if err != nil {
		return "", fmt.Errorf("backup: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("backup: %s is not a regular file", srcPath)
	}
	ext := filepath.Ext(srcPath)
	stem := fmt.Sprintf("%s_backup_%s", strings.TrimSuffix(srcPath, ext), time.Now().Format("20060102_150405"))
	// A second backup in the same second gets a number after the time.
	var f *os.File
	var backupPath string
	for i := 1; ; i++ {
		backupPath = stem + ext
		if i > 1 {
			backupPath = fmt.Sprintf("%s_%d%s", stem, i, ext)
		}
		f, err = os.OpenFile(backupPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if !errors.Is(err, fs.ErrExist) || i == 100 {
			break
		}
	}
	if err != nil {
		return "", fmt.Errorf("backup: create %s: %w", backupPath, err)
	}
	err = f.Close()
	if err == nil {
		err = writeBackup(srcPath, backupPath, info.Mode().Perm()&0o600)
	}
	if err != nil {
		_ = os.Remove(backupPath)
		return "", fmt.Errorf("backup to %s: %w", backupPath, err)
	}
	return backupPath, nil
}

// writeBackup fills the empty file at backupPath with a snapshot of the
// database at srcPath and gives it mode perm.
func writeBackup(srcPath, backupPath string, perm os.FileMode) error {
	src, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(5000)", srcPath))
	if err != nil {
		return err
	}
	_, err = src.Exec("VACUUM INTO ?", backupPath)
	if cerr := src.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("vacuum into: %w", err)
	}

	// VACUUM INTO does not sync the file it wrote.
	f, err := os.OpenFile(backupPath, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Chmod(backupPath, perm)
}

// sqliteSidecars are the files SQLite keeps next to a database. Left next
// to a new file of the same name, SQLite would apply them to it.
var sqliteSidecars = []string{"-wal", "-shm", "-journal"}

// checkSQLiteDst refuses a SQLite destination that is one of the sources,
// under any spelling. Unless overwrite is set it also refuses one that
// exists, or that has a -wal, -shm or -journal of an earlier database next
// to it.
func checkSQLiteDst(dstPath string, overwrite bool, srcPaths ...string) error {
	dstInfo, err := os.Stat(dstPath)
	if errors.Is(err, fs.ErrNotExist) {
		if overwrite {
			return nil
		}
		for _, suffix := range sqliteSidecars {
			if _, err := os.Stat(dstPath + suffix); err == nil {
				return fmt.Errorf("%s exists, from an earlier database: pass -overwrite to remove it", dstPath+suffix)
			}
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("destination: %w", err)
	}
	for _, src := range srcPaths {
		if srcInfo, err := os.Stat(src); err == nil && os.SameFile(srcInfo, dstInfo) {
			return fmt.Errorf("-dst %s is the source %s: write to a new file", dstPath, src)
		}
	}
	if !dstInfo.Mode().IsRegular() {
		return fmt.Errorf("-dst %s is not a regular file", dstPath)
	}
	if !overwrite {
		return fmt.Errorf("-dst %s exists: pass -overwrite to replace it", dstPath)
	}
	return nil
}

// writeSQLiteFile builds a new SQLite database for dstPath under a
// temporary name in the same folder with build, which gets that name. Only
// when every step succeeded does it take dstPath's place, after the -wal,
// -shm and -journal of the database there are removed. On a failure
// dstPath is left as it was. A replaced file keeps its mode; a new one
// gets 0600, as it holds password hashes.
func writeSQLiteFile(dstPath string, overwrite bool, build func(tmpPath string) (int, error)) (int, error) {
	perm := os.FileMode(0o600)
	if info, err := os.Stat(dstPath); err == nil {
		perm = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(dstPath), filepath.Base(dstPath)+".migrate-*")
	if err != nil {
		return 0, fmt.Errorf("create temporary file: %w", err)
	}
	tmpPath := tmp.Name()
	placed := false
	defer func() {
		if !placed {
			_ = os.Remove(tmpPath)
			for _, suffix := range sqliteSidecars {
				_ = os.Remove(tmpPath + suffix)
			}
		}
	}()
	if err := tmp.Close(); err != nil {
		return 0, err
	}

	n, err := build(tmpPath)
	if err != nil {
		return n, err
	}
	for _, suffix := range []string{"-wal", "-journal"} {
		if _, err := os.Stat(tmpPath + suffix); err == nil {
			return n, fmt.Errorf("the new database left %s behind: it is not complete in one file", tmpPath+suffix)
		}
	}
	if err := syncFile(tmpPath, perm); err != nil {
		return n, err
	}
	if overwrite {
		for _, suffix := range sqliteSidecars {
			if err := os.Remove(dstPath + suffix); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return n, err
			}
		}
	}
	if err := os.Rename(tmpPath, dstPath); err != nil {
		return n, err
	}
	placed = true
	return n, nil
}

// fillSQLite creates the tool's schema in the SQLite file at path and runs
// fill on it. The file stays in rollback-journal mode, so after Close it
// is one file.
func fillSQLite(path string, fill func(*sql.DB) (int, error)) (int, error) {
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)", path))
	if err != nil {
		return 0, fmt.Errorf("open destination: %w", err)
	}
	n, err := func() (int, error) {
		for _, stmt := range sqliteSchemaStatements() {
			if _, err := db.Exec(stmt); err != nil {
				return 0, fmt.Errorf("create schema: %w", err)
			}
		}
		return fill(db)
	}()
	if cerr := db.Close(); err == nil && cerr != nil {
		err = fmt.Errorf("close destination: %w", cerr)
	}
	return n, err
}

// syncFile gives the file at path mode perm and syncs it to disk.
func syncFile(path string, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	err = f.Chmod(perm)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// requireFile verifies that a local file exists.
func requireFile(path string) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("source: %w", err)
	}
	return nil
}

// tableExists checks if a table exists in a SQLite database.
func tableExists(db *sql.DB, name string) bool {
	var n string
	err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", name).Scan(&n)
	return err == nil
}

// safeIdentifier validates that a string is a safe SQL identifier (letters, digits, underscores).
func safeIdentifier(name string) bool {
	if len(name) == 0 || len(name) > 64 {
		return false
	}
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_') {
			return false
		}
	}
	return true
}

// getColumns returns column names for a table.
func getColumns(db *sql.DB, table string) []string {
	if !safeIdentifier(table) {
		return nil
	}
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return nil
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull int
		var dflt *string
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			continue
		}
		cols = append(cols, name)
	}
	return cols
}

func contains(s []string, e string) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}

// ── Time Conversion Helpers ───────────────────────────────────────────

// parseTime converts a SQLite TEXT timestamp to *time.Time for PostgreSQL:
// every form the console writes (store.ParseTime), or a bare date. Text
// without a zone is UTC. Empty or unreadable text gives nil (NULL in PG).
func parseTime(s string) *time.Time {
	if s == "" {
		return nil
	}
	if t, ok := store.ParseTime(s); ok {
		return &t
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return &t
	}
	return nil
}

// parseTimeDefault converts a SQLite TEXT to time.Time, defaulting to now().
func parseTimeDefault(s string) time.Time {
	t := parseTime(s)
	if t == nil {
		return time.Now()
	}
	return *t
}

// ── Nullable Pointer Helpers ──────────────────────────────────────────

func ptrStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func ptrIntBool(i *int64) int {
	if i != nil && *i == 1 {
		return 1
	}
	return 0
}

func nullStr(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}
