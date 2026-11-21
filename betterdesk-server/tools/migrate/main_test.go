package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// execSQLite runs statements on the SQLite file at path, creating it.
func execSQLite(t *testing.T, path string, stmts ...string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
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
