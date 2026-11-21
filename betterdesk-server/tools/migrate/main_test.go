package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// execSQLite runs statements on the SQLite file at path, creating it in WAL
// mode as the servers and the Node console do.
func execSQLite(t *testing.T, path string, stmts ...string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}

// countRows counts the rows of table in the SQLite file at path. The file
// is opened as immutable, so a -wal next to it is not read.
func countRows(t *testing.T, path, table string) int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&immutable=1")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatalf("count %s in %s: %v", table, path, err)
	}
	return n
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// tableExistsIn reports whether the SQLite file at path has the table.
func tableExistsIn(t *testing.T, path, table string) bool {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&immutable=1")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	return tableExists(db, table)
}

// rustPeerTable is the peer table of hbbs, which the Node console used too.
const rustPeerTable = `CREATE TABLE peer (
	guid BLOB PRIMARY KEY NOT NULL, id VARCHAR(100) NOT NULL, uuid BLOB NOT NULL,
	pk BLOB NOT NULL, created_at DATETIME NOT NULL DEFAULT (CURRENT_TIMESTAMP),
	user BLOB, status TINYINT, note VARCHAR(300), info TEXT NOT NULL)`

// nodePeerTable is the peer table with the columns the Node console added.
const nodePeerTable = `CREATE TABLE peer (
	guid BLOB PRIMARY KEY NOT NULL, id VARCHAR(100) NOT NULL, uuid BLOB NOT NULL,
	pk BLOB NOT NULL, created_at DATETIME NOT NULL DEFAULT (CURRENT_TIMESTAMP),
	user BLOB, status TINYINT, note VARCHAR(300), info TEXT NOT NULL,
	status_online TINYINT, last_online DATETIME, is_deleted TINYINT,
	is_banned TINYINT, banned_at DATETIME, banned_reason TEXT)`

// oldDestination writes a database standing for one a user wants to keep:
// table keep with one row.
func oldDestination(t *testing.T, path string) {
	t.Helper()
	execSQLite(t, path, "CREATE TABLE keep (x)", "INSERT INTO keep VALUES ('mine')")
}

// leftovers lists the files in dir other than the ones named.
func leftovers(t *testing.T, dir string, keep ...string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		known := false
		for _, k := range keep {
			if name == k || (strings.HasPrefix(name, strings.TrimSuffix(k, ".sqlite3")+"_backup_") && strings.HasSuffix(name, ".sqlite3")) {
				known = true
			}
		}
		if !known {
			out = append(out, name)
		}
	}
	return out
}

func TestBackupIsOwnerOnlyAtMost(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes are a Unix matter")
	}
	for _, tc := range []struct{ src, want os.FileMode }{
		{0o644, 0o600},
		{0o666, 0o600},
		{0o640, 0o600},
		{0o600, 0o600},
		{0o400, 0o400},
	} {
		path := filepath.Join(t.TempDir(), "db_v2.sqlite3")
		execSQLite(t, path, "CREATE TABLE users (password_hash TEXT)", "INSERT INTO users VALUES ('hash')")
		if err := os.Chmod(path, tc.src); err != nil {
			t.Fatal(err)
		}
		bp, err := createBackup(path)
		if err != nil {
			t.Fatalf("source %o: %v", tc.src, err)
		}
		info, err := os.Stat(bp)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != tc.want {
			t.Errorf("source %o: backup has mode %o, want %o", tc.src, got, tc.want)
		}
	}
}

func TestBackupOfLiveDatabaseIsOneConsistentFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db_v2.sqlite3")
	live, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	live.SetMaxOpenConns(1)
	for _, stmt := range []string{
		"CREATE TABLE peers (id TEXT PRIMARY KEY)",
		"INSERT INTO peers VALUES ('a'), ('b'), ('c')",
	} {
		if _, err := live.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	// The connection stays open, so the rows sit in the -wal file.
	if info, err := os.Stat(path + "-wal"); err != nil || info.Size() == 0 {
		t.Fatalf("the test needs a -wal with the rows in it: %v", err)
	}

	bp, err := createBackup(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if exists(bp + suffix) {
			t.Errorf("the backup is more than one file: %s exists", bp+suffix)
		}
	}
	if n := countRows(t, bp, "peers"); n != 3 {
		t.Errorf("backup file holds %d peers, want 3", n)
	}
	db, err := sql.Open("sqlite", "file:"+bp+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var check string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&check); err != nil || check != "ok" {
		t.Errorf("integrity_check of the backup: %q, %v", check, err)
	}
}

func TestBackupsInTheSameSecondKeepEachOther(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db_v2.sqlite3")
	execSQLite(t, path, "CREATE TABLE peers (id TEXT)")
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		bp, err := createBackup(path)
		if err != nil {
			t.Fatalf("backup %d: %v", i+1, err)
		}
		if seen[bp] {
			t.Fatalf("backup %d reused the name %s", i+1, bp)
		}
		seen[bp] = true
	}
	for bp := range seen {
		if !exists(bp) {
			t.Errorf("%s is gone", bp)
		}
	}
}

func TestBackupFailsWhenTheSourceIsNoDatabase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "db_v2.sqlite3")
	junk := make([]byte, 8192)
	for i := range junk {
		junk[i] = byte('a' + i%26)
	}
	if err := os.WriteFile(path, junk, 0o600); err != nil {
		t.Fatal(err)
	}
	if bp, err := createBackup(path); err == nil {
		t.Fatalf("backup of a file that is no database succeeded: %s", bp)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("a failed backup left files behind: %v", names)
	}
}

func TestNodeJS2GoRefusesASourceAsDestination(t *testing.T) {
	for _, overwrite := range []bool{false, true} {
		dir := t.TempDir()
		peerDB := filepath.Join(dir, "db_v2.sqlite3")
		authDB := filepath.Join(dir, "auth.sqlite3")
		execSQLite(t, peerDB, nodePeerTable,
			`INSERT INTO peer (guid, id, uuid, pk, info) VALUES (x'01', '123456789', x'02', x'03', '{}')`)
		execSQLite(t, authDB, "CREATE TABLE users (username TEXT, password_hash TEXT, role TEXT, created_at TEXT, last_login TEXT)",
			"INSERT INTO users VALUES ('admin', 'hash', 'admin', NULL, NULL)")

		if err := runNodeJS2Go(peerDB, "", peerDB, overwrite); err == nil {
			t.Errorf("overwrite %v: -dst equal to -src was accepted", overwrite)
		}
		// The same file under another spelling.
		authAgain := dir + "/./auth.sqlite3"
		if err := runNodeJS2Go(peerDB, authDB, authAgain, overwrite); err == nil {
			t.Errorf("overwrite %v: -dst equal to -node-auth was accepted", overwrite)
		}
		if !tableExistsIn(t, peerDB, "peer") || countRows(t, peerDB, "peer") != 1 {
			t.Errorf("overwrite %v: the source lost its peer", overwrite)
		}
		if !tableExistsIn(t, authDB, "users") || countRows(t, authDB, "users") != 1 {
			t.Errorf("overwrite %v: the auth database lost its user", overwrite)
		}
	}
}

func TestRust2GoReplacesAnExistingDestinationOnlyWithOverwrite(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "db_v2.sqlite3")
	dst := filepath.Join(dir, "betterdesk.sqlite3")
	execSQLite(t, src, rustPeerTable,
		`INSERT INTO peer (guid, id, uuid, pk, info) VALUES (x'01', '123456789', x'02', x'03', '{}')`)
	oldDestination(t, dst)

	if err := runRust2Go(src, dst, false); err == nil {
		t.Error("an existing -dst was replaced without -overwrite")
	}
	if err := runRust2Go(src, "", false); err == nil {
		t.Error("the default destination, which exists, was replaced without -overwrite")
	}
	if !tableExistsIn(t, dst, "keep") || countRows(t, dst, "keep") != 1 {
		t.Fatal("the refused run changed the destination")
	}

	if err := os.Chmod(dst, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := runRust2Go(src, dst, true); err != nil {
		t.Fatalf("with -overwrite: %v", err)
	}
	if tableExistsIn(t, dst, "keep") {
		t.Error("with -overwrite the old database is still there")
	}
	if n := countRows(t, dst, "peers"); n != 1 {
		t.Errorf("with -overwrite the destination has %d peers, want 1", n)
	}
	fresh := filepath.Join(dir, "fresh.sqlite3")
	if err := runRust2Go(src, fresh, false); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(dst); err != nil || info.Mode().Perm() != 0o640 {
			t.Errorf("the replaced file did not keep its mode 0640: %v", info.Mode())
		}
		if info, err := os.Stat(fresh); err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("a new destination has mode %v, want 0600", info.Mode())
		}
	}
	if left := leftovers(t, dir, "db_v2.sqlite3", "db_v2.sqlite3-wal", "db_v2.sqlite3-shm", "betterdesk.sqlite3", "fresh.sqlite3"); len(left) != 0 {
		t.Errorf("files left behind: %v", left)
	}
}

func TestFailedMigrationLeavesTheDestinationAlone(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "db_v2.sqlite3")
	dst := filepath.Join(dir, "betterdesk.sqlite3")
	// A peer table without the info column: the copy fails on it.
	execSQLite(t, src, "CREATE TABLE peer (id TEXT, uuid TEXT, pk BLOB, note TEXT, created_at TEXT)",
		"INSERT INTO peer VALUES ('123456789', 'u', x'00', '', '2026-01-01 00:00:00')")
	oldDestination(t, dst)

	if err := runNodeJS2Go(src, "", dst, true); err == nil {
		t.Fatal("the migration of a broken source succeeded")
	}
	if !tableExistsIn(t, dst, "keep") || countRows(t, dst, "keep") != 1 {
		t.Error("the failed migration changed the destination")
	}
	if left := leftovers(t, dir, "db_v2.sqlite3", "db_v2.sqlite3-wal", "db_v2.sqlite3-shm", "betterdesk.sqlite3", "fresh.sqlite3"); len(left) != 0 {
		t.Errorf("files left behind: %v", left)
	}
}

func TestOverwriteDropsTheOldDatabasesWAL(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "db_v2.sqlite3")
	dst := filepath.Join(dir, "betterdesk.sqlite3")
	execSQLite(t, src, rustPeerTable,
		`INSERT INTO peer (guid, id, uuid, pk, info) VALUES (x'01', '123456789', x'02', x'03', '{}')`)

	// The destination as a server leaves it when it is killed: the last
	// writes are in its -wal and -shm only.
	live := filepath.Join(t.TempDir(), "live.sqlite3")
	conn, err := sql.Open("sqlite", "file:"+live+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	conn.SetMaxOpenConns(1)
	for _, stmt := range []string{"CREATE TABLE keep (x)", "INSERT INTO keep VALUES ('mine')"} {
		if _, err := conn.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		b, err := os.ReadFile(live + suffix)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst+suffix, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_ = conn.Close()

	if err := os.Rename(dst, dst+".moved"); err != nil {
		t.Fatal(err)
	}
	if err := runRust2Go(src, dst, false); err == nil {
		t.Error("a -wal of an earlier database next to -dst was accepted without -overwrite")
	}
	if err := os.Rename(dst+".moved", dst); err != nil {
		t.Fatal(err)
	}

	if err := runRust2Go(src, dst, true); err != nil {
		t.Fatalf("with -overwrite: %v", err)
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if exists(dst + suffix) {
			t.Errorf("%s of the old database is still next to the new one", suffix)
		}
	}
	db, err := sql.Open("sqlite", "file:"+dst)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM peers").Scan(&n); err != nil || n != 1 {
		t.Errorf("new destination: %d peers, %v; want 1", n, err)
	}
	if tableExists(db, "keep") {
		t.Error("the old database's -wal was applied to the new file")
	}
}

// pgDSN returns the DSN of a fresh schema on the server named by
// BETTERDESK_TEST_POSTGRES_DSN, with the tool's PostgreSQL schema in it
// plus the statements given, or skips the test when no server is set.
func pgDSN(t *testing.T, stmts ...string) string {
	t.Helper()
	base := os.Getenv(storetest.PostgresEnv)
	if base == "" {
		t.Skipf("%s is not set", storetest.PostgresEnv)
	}
	ctx := context.Background()
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	schema := "t_" + hex.EncodeToString(suffix)
	admin, err := pgxpool.New(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	dsn := u.String()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	for _, stmt := range append(pgSchemaStatements(), stmts...) {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	return dsn
}

func TestPG2SQLiteReplacesAnExistingDestinationOnlyWhenTheCopySucceeds(t *testing.T) {
	dsn := pgDSN(t, `INSERT INTO peers (id) VALUES ('123456789')`)
	dir := t.TempDir()
	dst := filepath.Join(dir, "db_v2.sqlite3")
	oldDestination(t, dst)

	if err := runPG2SQLite(dsn, dst, moveOptions{overwrite: false}); err == nil {
		t.Error("an existing -dst was replaced without -overwrite")
	}
	// A user without password hash, which SQLite's users table refuses.
	broken := pgDSN(t, `INSERT INTO peers (id) VALUES ('123456789')`,
		`ALTER TABLE users ALTER COLUMN password_hash DROP NOT NULL`,
		`INSERT INTO users (username, password_hash) VALUES ('broken', NULL)`)
	if err := runPG2SQLite(broken, dst, moveOptions{overwrite: true}); err == nil {
		t.Error("a copy that failed on a user succeeded")
	}
	if !tableExistsIn(t, dst, "keep") || countRows(t, dst, "keep") != 1 {
		t.Fatal("a refused or failed run changed the destination")
	}

	if err := runPG2SQLite(dsn, dst, moveOptions{overwrite: true}); err != nil {
		t.Fatalf("with -overwrite: %v", err)
	}
	if tableExistsIn(t, dst, "keep") || countRows(t, dst, "peers") != 1 {
		t.Error("with -overwrite the destination is not the copy")
	}
	if left := leftovers(t, dir, "db_v2.sqlite3"); len(left) != 0 {
		t.Errorf("files left behind: %v", left)
	}
}

// countPG counts the rows of table in the PostgreSQL schema dsn names.
func countPG(t *testing.T, dsn, table string) int {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var n int
	if err := pool.QueryRow(context.Background(), "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func TestNodeJS2GoCopiesAPeerWithoutNote(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "db_v2.sqlite3")
	dst := filepath.Join(dir, "betterdesk.sqlite3")
	execSQLite(t, src, nodePeerTable,
		`INSERT INTO peer (guid, id, uuid, pk, note, info) VALUES (x'01', '123456789', x'02', x'03', NULL, '{}')`)
	if err := runNodeJS2Go(src, "", dst, false); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, dst, "peers"); n != 1 {
		t.Errorf("destination has %d peers, want the one without a note", n)
	}
}

func TestNodeJS2GoFailsOnAPeerItCannotRead(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "db_v2.sqlite3")
	dst := filepath.Join(dir, "betterdesk.sqlite3")
	execSQLite(t, src, strings.Replace(nodePeerTable, "info TEXT NOT NULL", "info TEXT", 1),
		`INSERT INTO peer (guid, id, uuid, pk, info) VALUES (x'01', '111111111', x'02', x'03', '{}')`,
		`INSERT INTO peer (guid, id, uuid, pk, info) VALUES (x'04', '222222222', x'05', x'06', NULL)`)
	if err := runNodeJS2Go(src, "", dst, false); err == nil {
		t.Error("a peer that could not be read was skipped and the migration succeeded")
	}
	if exists(dst) {
		t.Error("the failed migration left a destination")
	}
}

func TestNodeJS2GoFailsOnAUserItCannotWrite(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "db_v2.sqlite3")
	auth := filepath.Join(dir, "auth.sqlite3")
	dst := filepath.Join(dir, "betterdesk.sqlite3")
	execSQLite(t, src, nodePeerTable)
	execSQLite(t, auth, "CREATE TABLE users (username TEXT, password_hash TEXT, role TEXT, created_at TEXT, last_login TEXT)",
		"INSERT INTO users VALUES ('admin', 'hash', 'admin', NULL, NULL)",
		"INSERT INTO users VALUES ('broken', NULL, 'viewer', NULL, NULL)")
	if err := runNodeJS2Go(src, auth, dst, false); err == nil {
		t.Error("a user the destination refused was dropped and the migration succeeded")
	}
	if exists(dst) {
		t.Error("the failed migration left a destination")
	}
}

func TestRust2GoFailsOnAPeerItCannotRead(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "db_v2.sqlite3")
	dst := filepath.Join(dir, "betterdesk.sqlite3")
	execSQLite(t, src, strings.Replace(rustPeerTable, "info TEXT NOT NULL", "info TEXT", 1),
		`INSERT INTO peer (guid, id, uuid, pk, info) VALUES (x'01', '111111111', x'02', x'03', '{}')`,
		`INSERT INTO peer (guid, id, uuid, pk, info) VALUES (x'04', '222222222', x'05', x'06', NULL)`)
	if err := runRust2Go(src, dst, false); err == nil {
		t.Error("a peer that could not be read was skipped and the migration succeeded")
	}
	if exists(dst) {
		t.Error("the failed migration left a destination")
	}
}

func TestSQLite2PGCopiesRowsWithNulls(t *testing.T) {
	dsn := pgDSN(t)
	src := filepath.Join(t.TempDir(), "db_v2.sqlite3")
	execSQLite(t, src, sqliteSchemaStatements()...)
	execSQLite(t, src,
		`INSERT INTO peers (id, uuid, pk, ip, user, hostname, os, version, status, nat_type,
			last_online, created_at, disabled, banned, ban_reason, banned_at,
			soft_deleted, deleted_at, note, tags, heartbeat_seq)
		 VALUES ('123456789', NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL,
			NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL)`,
		`INSERT INTO server_config (key, value) VALUES ('relay', NULL)`,
		`INSERT INTO id_change_history (old_id, new_id, changed_at, reason) VALUES ('1', '2', NULL, NULL)`,
		`INSERT INTO users (username, password_hash, role, totp_secret, totp_enabled, created_at, last_login)
		 VALUES ('never-signed-in', 'hash', 'viewer', NULL, NULL, NULL, NULL)`,
		`INSERT INTO api_keys (key_hash, key_prefix, name, role, created_at, expires_at, last_used)
		 VALUES ('kh', 'p', 'n', 'viewer', NULL, NULL, NULL)`)

	if err := runSQLite2PG(src, dsn, moveOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"peers", "server_config", "id_change_history", "users", "api_keys"} {
		if n := countPG(t, dsn, table); n != 1 {
			t.Errorf("%s: %d rows in PostgreSQL, want 1", table, n)
		}
	}
}

func TestSQLite2PGFailsOnARowItCannotCopy(t *testing.T) {
	for name, rows := range map[string][]string{
		// A user without password hash cannot be read into the tool's shape.
		"user without password": {
			"INSERT INTO users (username, password_hash) VALUES ('fine', 'hash')",
			"INSERT INTO users (username, password_hash) VALUES ('broken', NULL)",
		},
		// PostgreSQL refuses text that is not UTF-8.
		"peer PostgreSQL refuses": {
			"INSERT INTO peers (id, hostname) VALUES ('111111111', 'fine')",
			"INSERT INTO peers (id, hostname) VALUES ('222222222', CAST(x'41ff42' AS TEXT))",
		},
		"setting PostgreSQL refuses": {
			"CREATE TABLE server_config (key TEXT PRIMARY KEY, value TEXT)",
			"INSERT INTO server_config VALUES ('fine', 'yes')",
			"INSERT INTO server_config VALUES ('broken', CAST(x'41ff42' AS TEXT))",
		},
	} {
		t.Run(name, func(t *testing.T) {
			dsn := pgDSN(t)
			src := filepath.Join(t.TempDir(), "db_v2.sqlite3")
			execSQLite(t, src,
				"CREATE TABLE users (username TEXT, password_hash TEXT, role TEXT DEFAULT 'viewer', totp_secret TEXT, totp_enabled INTEGER, created_at TEXT, last_login TEXT)",
				sqliteSchemaStatements()[0]) // peers
			execSQLite(t, src, rows...)
			if err := runSQLite2PG(src, dsn, moveOptions{}); err == nil {
				t.Error("a row that could not be copied was dropped and the migration succeeded")
			}
		})
	}
}

// consoleShapedPG is what the console's own PostgreSQL schema allows on
// top of the tool's: NULL in columns the tool declares NOT NULL.
var consoleShapedPG = []string{
	`ALTER TABLE users ALTER COLUMN totp_secret DROP NOT NULL, ALTER COLUMN totp_enabled DROP NOT NULL,
		ALTER COLUMN created_at DROP NOT NULL`,
	`ALTER TABLE peers ALTER COLUMN uuid DROP NOT NULL, ALTER COLUMN note DROP NOT NULL,
		ALTER COLUMN nat_type DROP NOT NULL, ALTER COLUMN banned DROP NOT NULL,
		ALTER COLUMN created_at DROP NOT NULL, ALTER COLUMN heartbeat_seq DROP NOT NULL`,
	`ALTER TABLE server_config ALTER COLUMN value DROP NOT NULL`,
	`ALTER TABLE id_change_history ALTER COLUMN reason DROP NOT NULL`,
	`ALTER TABLE api_keys ALTER COLUMN created_at DROP NOT NULL`,
}

func TestPG2SQLiteCopiesRowsWithNulls(t *testing.T) {
	dsn := pgDSN(t, append(consoleShapedPG,
		`INSERT INTO peers (id, uuid, note, nat_type, banned, created_at, heartbeat_seq)
		 VALUES ('123456789', NULL, NULL, NULL, NULL, NULL, NULL)`,
		`INSERT INTO server_config (key, value) VALUES ('relay', NULL)`,
		`INSERT INTO id_change_history (old_id, new_id, reason) VALUES ('1', '2', NULL)`,
		`INSERT INTO users (username, password_hash, totp_secret, totp_enabled, created_at, last_login)
		 VALUES ('never-signed-in', 'hash', NULL, NULL, NULL, NULL)`,
		`INSERT INTO api_keys (key_hash, created_at) VALUES ('kh', NULL)`)...)
	dst := filepath.Join(t.TempDir(), "db_v2.sqlite3")

	if err := runPG2SQLite(dsn, dst, moveOptions{overwrite: false}); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"peers", "server_config", "id_change_history", "users", "api_keys"} {
		if n := countRows(t, dst, table); n != 1 {
			t.Errorf("%s: %d rows in SQLite, want 1", table, n)
		}
	}
}

func TestPG2SQLiteFailsOnARowItCannotRead(t *testing.T) {
	dsn := pgDSN(t,
		`ALTER TABLE users ALTER COLUMN password_hash DROP NOT NULL`,
		`INSERT INTO users (username, password_hash) VALUES ('fine', 'hash'), ('broken', NULL)`)
	dst := filepath.Join(t.TempDir(), "db_v2.sqlite3")
	if err := runPG2SQLite(dsn, dst, moveOptions{overwrite: false}); err == nil {
		t.Error("a user that could not be read was dropped and the migration succeeded")
	}
	if exists(dst) {
		t.Error("the failed migration left a destination")
	}
}

func TestTimesGoToSQLiteAsUTC(t *testing.T) {
	// pgx hands timestamptz values over in the process's zone.
	east := time.FixedZone("UTC+1", 3600)
	at := time.Date(2026, 1, 1, 13, 0, 0, 123456000, east)
	timestamptz := column{kind: kTime, declared: "timestamp with time zone"}
	for field, want := range map[string]string{
		"peers.last_online":             "2026-01-01 12:00:00",
		"studio_flows.updated_at":       "2026-01-01T12:00:00.123Z",
		"access_tokens.expires_at":      "2026-01-01 12:00:00.123",
		"org_users.created_at":          "2026-01-01T12:00:00Z",
		"dlp_events.created_at":         "2026-01-01 12:00:00",
		"tickets.sla_due_at":            "2026-01-01T12:00:00.123Z",
		"account_lockouts.locked_until": "2026-01-01 12:00:00.123",
	} {
		dst := column{kind: kText, declared: "TEXT"}
		if field == "dlp_events.created_at" {
			dst = column{kind: kDateTimeText, declared: "DATETIME"}
		}
		got, err := convert(at, timestamptz, dst, false, field)
		if err != nil || got != want {
			t.Errorf("%s: %v (%v), want the UTC time %q", field, got, err, want)
		}
	}
}

func TestParseTimeReadsTheConsolesTimeText(t *testing.T) {
	for text, want := range map[string]time.Time{
		"2026-11-17 01:05:51.694":       time.Date(2026, 11, 17, 1, 5, 51, 694000000, time.UTC),
		"2026-11-17 01:05:51":           time.Date(2026, 11, 17, 1, 5, 51, 0, time.UTC),
		"2026-11-17T01:05:51.694Z":      time.Date(2026, 11, 17, 1, 5, 51, 694000000, time.UTC),
		"2026-11-17T02:05:51+01:00":     time.Date(2026, 11, 17, 1, 5, 51, 0, time.UTC),
		"2026-11-17T01:05:51.123456789": time.Date(2026, 11, 17, 1, 5, 51, 123456789, time.UTC),
		"2026-11-17":                    time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC),
	} {
		got := parseTime(text)
		if got == nil || !got.Equal(want) {
			t.Errorf("parseTime(%q) = %v, want %v", text, got, want)
		}
	}
	if got := parseTime("not a time"); got != nil {
		t.Errorf("parseTime of junk = %v, want nil", got)
	}
}

func TestSQLite2PGKeepsTimesTheConsoleWrites(t *testing.T) {
	dsn := pgDSN(t)
	src := filepath.Join(t.TempDir(), "db_v2.sqlite3")
	execSQLite(t, src, sqliteSchemaStatements()...)
	execSQLite(t, src,
		`INSERT INTO api_keys (key_hash, created_at, expires_at) VALUES ('kh', '2026-11-17 01:05:51.694', '2026-12-01T00:00:00.5')`)
	if err := runSQLite2PG(src, dsn, moveOptions{}); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var created time.Time
	var expires *time.Time
	if err := pool.QueryRow(context.Background(), "SELECT created_at, expires_at FROM api_keys").Scan(&created, &expires); err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 11, 17, 1, 5, 51, 694000000, time.UTC); !created.Equal(want) {
		t.Errorf("created_at = %v, want %v", created, want)
	}
	if want := time.Date(2026, 12, 1, 0, 0, 0, 500000000, time.UTC); expires == nil || !expires.Equal(want) {
		t.Errorf("expires_at = %v, want %v", expires, want)
	}
}

// rollbackJournal writes a SQLite file in rollback-journal mode, as a copied
// or restored database can be.
func rollbackJournal(t *testing.T, path string, stmts ...string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(DELETE)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	var mode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "delete" {
		t.Fatalf("journal mode %q, %v; the test needs delete", mode, err)
	}
}

func TestRust2GoReadsASourceInRollbackJournalMode(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "db_v2.sqlite3")
	dst := filepath.Join(dir, "betterdesk.sqlite3")
	rollbackJournal(t, src, rustPeerTable,
		`INSERT INTO peer (guid, id, uuid, pk, info) VALUES (x'01', '123456789', x'02', x'03', '{}')`)
	if err := runRust2Go(src, dst, false); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, dst, "peers"); n != 1 {
		t.Errorf("destination has %d peers, want 1", n)
	}
}

func TestSQLite2PGReadsASourceInRollbackJournalMode(t *testing.T) {
	dsn := pgDSN(t)
	src := filepath.Join(t.TempDir(), "db_v2.sqlite3")
	rollbackJournal(t, src, append(sqliteSchemaStatements(),
		`INSERT INTO peers (id) VALUES ('123456789')`)...)
	if err := runSQLite2PG(src, dsn, moveOptions{}); err != nil {
		t.Fatal(err)
	}
	if n := countPG(t, dsn, "peers"); n != 1 {
		t.Errorf("PostgreSQL has %d peers, want 1", n)
	}
}
