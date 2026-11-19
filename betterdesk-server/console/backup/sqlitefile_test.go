package backup

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/unitronix/betterdesk-server/console/store"
)

// BK6: the SQLite file in a full backup is one consistent copy with what
// the WAL still holds, and the console's and the server's entry are that
// same copy.
func TestFullBackupCopiesTheWAL(t *testing.T) {
	ctx := context.Background()
	in := newInstall(t, "sqlite")
	in.seed(t) // ends with a checkpoint
	mustExec(t, in.db, `INSERT INTO folders (name) VALUES ('only in the WAL')`)
	if info, err := os.Stat(in.dbPath + "-wal"); err != nil || info.Size() == 0 {
		t.Fatalf("the WAL holds nothing: %v", err)
	}
	archive, _, err := in.service(t, "x").CreateFullBackup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	files, err := ExtractTarGz(archive, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	server, ok := files.Get("goserver/db_v2.sqlite3")
	console, ok2 := files.Get("console/auth.db")
	if !ok || !ok2 || !bytes.Equal(server, console) {
		t.Fatalf("database entries: %v %v, the same bytes: %v", ok, ok2, bytes.Equal(server, console))
	}
	copyPath := filepath.Join(t.TempDir(), "copy.sqlite3")
	if err := os.WriteFile(copyPath, server, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := store.OpenSQLite(copyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM folders WHERE name = 'only in the WAL'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("the copy lacks the row in the WAL: %d %v", n, err)
	}
}

// BK6: a restore of the server's SQLite file leaves the open file alone and
// puts the archive's file next to it; the start swaps it in and drops the
// old file's WAL.
func TestRestoredDatabaseFileWaitsForTheStart(t *testing.T) {
	ctx := context.Background()
	src := newInstall(t, "sqlite")
	mustExec(t, src.db, `INSERT INTO folders (name) VALUES ('from the archive')`)
	archive, _, err := src.service(t, "x").CreateFullBackup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	files, err := ExtractTarGz(archive, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	fromArchive, _ := files.Get("goserver/db_v2.sqlite3")

	dst := newInstall(t, "sqlite")
	mustExec(t, dst.db, `INSERT INTO folders (name) VALUES ('live')`)
	res, err := dst.service(t, "x").RestoreFullBackup(ctx, archive, RestoreOptions{GoDB: true})
	if err != nil || len(res.Warnings) != 0 || !res.RequiresRestart {
		t.Fatalf("restore: %+v %v", res, err)
	}
	var name string
	if err := dst.db.QueryRow(ctx, `SELECT name FROM folders`).Scan(&name); err != nil || name != "live" {
		t.Errorf("the open database after the restore: %q %v", name, err)
	}
	if data, err := os.ReadFile(dst.dbPath + RestoredSuffix); err != nil || !bytes.Equal(data, fromArchive) {
		t.Fatalf("the file for the start: %d bytes %v", len(data), err)
	}

	if err := dst.db.Close(); err != nil {
		t.Fatal(err)
	}
	for _, side := range []string{"-wal", "-shm"} {
		if err := os.WriteFile(dst.dbPath+side, []byte("of the old file"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if swapped, err := SwapInRestoredDatabase(dst.dbPath); err != nil || !swapped {
		t.Fatalf("swap: %v %v", swapped, err)
	}
	if data, _ := os.ReadFile(dst.dbPath); !bytes.Equal(data, fromArchive) {
		t.Error("the database is not the restored file")
	}
	for _, path := range []string{dst.dbPath + "-wal", dst.dbPath + "-shm", dst.dbPath + RestoredSuffix} {
		if _, err := os.Stat(path); err == nil {
			t.Errorf("%s is still there", filepath.Base(path))
		}
	}
	if swapped, err := SwapInRestoredDatabase(dst.dbPath); err != nil || swapped {
		t.Errorf("second swap: %v %v", swapped, err)
	}
	db, err := store.OpenSQLite(dst.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.QueryRow(ctx, `SELECT name FROM folders`).Scan(&name); err != nil || name != "from the archive" {
		t.Errorf("the database after the swap: %q %v", name, err)
	}
}

// A swap that fails leaves the database and its -wal and -shm files as
// they were.
func TestFailedSwapKeepsTheDatabase(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "db_v2.sqlite3")
	for name, content := range map[string]string{dbPath: "old file", dbPath + "-wal": "old wal", dbPath + "-shm": "old shm"} {
		if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// a folder that holds a file cannot take the database's place
	if err := os.MkdirAll(filepath.Join(dbPath+RestoredSuffix, "inside"), 0o755); err != nil {
		t.Fatal(err)
	}
	if swapped, err := SwapInRestoredDatabase(dbPath); err == nil || swapped {
		t.Fatalf("swap: %v %v", swapped, err)
	}
	for name, content := range map[string]string{dbPath: "old file", dbPath + "-wal": "old wal", dbPath + "-shm": "old shm"} {
		if data, err := os.ReadFile(name); err != nil || string(data) != content {
			t.Errorf("%s: %q %v", filepath.Base(name), data, err)
		}
	}
}
