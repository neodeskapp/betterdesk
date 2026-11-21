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

	if err := runPG2SQLite(dsn, dst, false); err == nil {
		t.Error("an existing -dst was replaced without -overwrite")
	}
	broken := pgDSN(t, `INSERT INTO peers (id) VALUES ('123456789')`, `DROP TABLE api_keys`)
	if err := runPG2SQLite(broken, dst, true); err == nil {
		t.Error("a copy that failed on api_keys succeeded")
	}
	if !tableExistsIn(t, dst, "keep") || countRows(t, dst, "keep") != 1 {
		t.Fatal("a refused or failed run changed the destination")
	}

	if err := runPG2SQLite(dsn, dst, true); err != nil {
		t.Fatalf("with -overwrite: %v", err)
	}
	if tableExistsIn(t, dst, "keep") || countRows(t, dst, "peers") != 1 {
		t.Error("with -overwrite the destination is not the copy")
	}
	if left := leftovers(t, dir, "db_v2.sqlite3"); len(left) != 0 {
		t.Errorf("files left behind: %v", left)
	}
}
