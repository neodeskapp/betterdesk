package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/backup"
)

// The start puts the database a restore left in place of the server
// database before it opens it (BUGS BK6), and leaves a PostgreSQL DSN
// alone.
func TestStartSwapsInTheRestoredDatabase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.sqlite3")
	for name, content := range map[string]string{path: "old", path + "-wal": "old wal", path + backup.RestoredSuffix: "restored"} {
		if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	swapRestoredDatabase(path)
	if got, err := os.ReadFile(path); err != nil || string(got) != "restored" {
		t.Errorf("the database after the start: %q, %v, want the restored file", got, err)
	}
	if _, err := os.Stat(path + "-wal"); !os.IsNotExist(err) {
		t.Errorf("the old database's -wal file is still there: %v", err)
	}
	if _, err := os.Stat(path + backup.RestoredSuffix); !os.IsNotExist(err) {
		t.Errorf("the restored file is still next to the database: %v", err)
	}

	t.Chdir(dir)
	dsn := "postgres://u:p@db/neodesk"
	if err := os.MkdirAll(filepath.Join(dir, "postgres:"), 0o700); err != nil {
		t.Fatal(err)
	}
	swapRestoredDatabase(dsn)
	entries, err := os.ReadDir(filepath.Join(dir, "postgres:"))
	if err != nil || len(entries) != 0 {
		t.Errorf("a PostgreSQL DSN was treated as a file: %v, %v", entries, err)
	}
}
