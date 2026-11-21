package backup

import (
	"bytes"
	"context"
	"database/sql"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
// puts the archive's file next to it; the start swaps it in and keeps the
// old file with its WAL as DB_PATH + BeforeRestoreSuffix (RV18).
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
	if err != nil || len(res.Warnings) != 0 || !res.RequiresRestart || !slices.Contains(res.Restored, "goserver-db") {
		t.Fatalf("restore: %+v %v", res, err)
	}
	var name string
	if err := dst.db.QueryRow(ctx, `SELECT name FROM folders`).Scan(&name); err != nil || name != "live" {
		t.Errorf("the open database after the restore: %q %v", name, err)
	}
	if data, err := os.ReadFile(dst.dbPath + RestoredSuffix); err != nil || !bytes.Equal(data, fromArchive) {
		t.Fatalf("the file for the start: %d bytes %v", len(data), err)
	}
	entries, _ := os.ReadDir(filepath.Dir(dst.dbPath))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".restore-check-") {
			t.Errorf("the check left %s", e.Name())
		}
	}

	if err := dst.db.Close(); err != nil {
		t.Fatal(err)
	}
	old, err := os.ReadFile(dst.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, side := range []string{"-wal", "-shm"} {
		if err := os.WriteFile(dst.dbPath+side, []byte("of the old file"+side), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if swapped, err := SwapInRestoredDatabase(dst.dbPath); err != nil || !swapped {
		t.Fatalf("swap: %v %v", swapped, err)
	}
	if data, _ := os.ReadFile(dst.dbPath); !bytes.Equal(data, fromArchive) {
		t.Error("the database is not the restored file")
	}
	keep := dst.dbPath + BeforeRestoreSuffix
	for path, want := range map[string]string{keep: string(old), keep + "-wal": "of the old file-wal", keep + "-shm": "of the old file-shm"} {
		if data, err := os.ReadFile(path); err != nil || string(data) != want {
			t.Errorf("%s: %d bytes %v, want the old file's", filepath.Base(path), len(data), err)
		}
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

// RV18: the database kept by an earlier swap gives way to the one replaced
// now, and the -wal and -shm files of the earlier one go with it.
func TestSwapReplacesTheDatabaseKeptBefore(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "db_v2.sqlite3")
	keep := dbPath + BeforeRestoreSuffix
	for name, content := range map[string]string{dbPath: "current", dbPath + RestoredSuffix: "restored",
		keep: "older", keep + "-wal": "older wal", keep + "-shm": "older shm"} {
		if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if swapped, err := SwapInRestoredDatabase(dbPath); err != nil || !swapped {
		t.Fatalf("swap: %v %v", swapped, err)
	}
	got := filesBelow(t, dir)
	want := map[string]string{"db_v2.sqlite3": "restored", "db_v2.sqlite3" + BeforeRestoreSuffix: "current"}
	if !maps.Equal(got, want) {
		t.Errorf("files %v, want %v", got, want)
	}
}

// A swap that fails leaves the database and its -wal and -shm files as
// they were, and the restored file waiting.
func TestFailedSwapKeepsTheDatabase(t *testing.T) {
	setup := func(t *testing.T) string {
		dbPath := filepath.Join(t.TempDir(), "db_v2.sqlite3")
		for name, content := range map[string]string{dbPath: "old file", dbPath + "-wal": "old wal", dbPath + "-shm": "old shm"} {
			if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return dbPath
	}
	unchanged := func(t *testing.T, dbPath string) {
		for name, content := range map[string]string{dbPath: "old file", dbPath + "-wal": "old wal", dbPath + "-shm": "old shm"} {
			if data, err := os.ReadFile(name); err != nil || string(data) != content {
				t.Errorf("%s: %q %v", filepath.Base(name), data, err)
			}
		}
	}
	t.Run("the restored file is a folder", func(t *testing.T) {
		dbPath := setup(t)
		if err := os.MkdirAll(dbPath+RestoredSuffix, 0o755); err != nil {
			t.Fatal(err)
		}
		if swapped, err := SwapInRestoredDatabase(dbPath); err == nil || swapped {
			t.Fatalf("swap: %v %v", swapped, err)
		}
		unchanged(t, dbPath)
	})
	t.Run("the old file cannot be kept", func(t *testing.T) {
		dbPath := setup(t)
		if err := os.WriteFile(dbPath+RestoredSuffix, []byte("restored"), 0o600); err != nil {
			t.Fatal(err)
		}
		// a folder that holds a file cannot take the old file
		if err := os.MkdirAll(filepath.Join(dbPath+BeforeRestoreSuffix, "inside"), 0o755); err != nil {
			t.Fatal(err)
		}
		if swapped, err := SwapInRestoredDatabase(dbPath); err == nil || swapped {
			t.Fatalf("swap: %v %v", swapped, err)
		}
		unchanged(t, dbPath)
		if data, err := os.ReadFile(dbPath + RestoredSuffix); err != nil || string(data) != "restored" {
			t.Errorf("the restored file: %q %v", data, err)
		}
	})
}

// withServerDatabase returns the archive with the server's database entry
// holding data.
func withServerDatabase(t *testing.T, archive, data []byte) []byte {
	t.Helper()
	files, err := ExtractTarGz(archive, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	var entries []Entry
	for _, name := range files.Names() {
		content, _ := files.Get(name)
		if name == "goserver/db_v2.sqlite3" {
			content = data
		}
		entries = append(entries, Entry{Name: name, Data: content, Mode: 0o600})
	}
	out, err := CreateTarGz(entries, 0)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// indexOutOfStep is a SQLite file that opens and reads, and whose index
// holds other values than its schema names: PRAGMA integrity_check lists
// the rows missing from it.
func indexOutOfStep(t *testing.T) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "out-of-step.sqlite3")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	for _, q := range []string{
		`CREATE TABLE t (a TEXT, b TEXT)`,
		`CREATE INDEX t_a ON t (a)`,
		`INSERT INTO t VALUES ('1', 'one'), ('2', 'two')`,
		`PRAGMA writable_schema = ON`,
		`UPDATE sqlite_master SET sql = 'CREATE INDEX t_a ON t (b)' WHERE name = 't_a'`,
		`PRAGMA writable_schema = OFF`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// RV18: a server database that is no SQLite file, or one that fails
// PRAGMA integrity_check, is not left for the start.
func TestRestoreRefusesAServerDatabaseThatFailsTheCheck(t *testing.T) {
	ctx := context.Background()
	src := newInstall(t, "sqlite")
	src.seed(t)
	archive, _, err := src.service(t, "x").CreateFullBackup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	files, err := ExtractTarGz(archive, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	good, _ := files.Get("goserver/db_v2.sqlite3")
	pageSize := int(good[16])<<8 | int(good[17])
	if pageSize == 1 {
		pageSize = 65536
	}
	if len(good) < 3*pageSize {
		t.Fatalf("the database has %d bytes, pages of %d", len(good), pageSize)
	}
	// the header and the schema page stay, the pages of the tables do not
	broken := bytes.Clone(good)
	for i := pageSize; i < len(broken); i++ {
		broken[i] = 0xA5
	}
	cases := map[string][]byte{"not a database": []byte("not a database"), "empty": {}, "broken pages": broken,
		"index out of step": indexOutOfStep(t)}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			dst := newInstall(t, "sqlite")
			res, err := dst.service(t, "x").RestoreFullBackup(ctx, withServerDatabase(t, archive, data), RestoreOptions{GoDB: true})
			if err != nil {
				t.Fatal(err)
			}
			if slices.Contains(res.Restored, "goserver-db") || res.RequiresRestart || len(res.Warnings) != 1 ||
				!strings.HasPrefix(res.Warnings[0], "Go server DB restore failed: ") {
				t.Errorf("restore: %+v", res)
			}
			if _, err := os.Stat(dst.dbPath + RestoredSuffix); err == nil {
				t.Error("the file is left for the start")
			}
			left := filesBelow(t, dst.keysPath)
			delete(left, filepath.Base(dst.dbPath))
			delete(left, filepath.Base(dst.dbPath)+"-wal")
			delete(left, filepath.Base(dst.dbPath)+"-shm")
			if len(left) != 0 {
				t.Errorf("files next to the database: %v", slices.Collect(maps.Keys(left)))
			}
		})
	}
}

// RV18: the restore leaves the server's database next to the file the
// server opens (ServerDB, its DSN), not next to the console's DB_PATH.
// A server on PostgreSQL has no such file: the part is skipped and no
// file is written, also where a stale file lies at DB_PATH.
func TestRestoreFollowsTheServersDatabase(t *testing.T) {
	ctx := context.Background()
	src := newInstall(t, "sqlite")
	archive, _, err := src.service(t, "x").CreateFullBackup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	opts := RestoreOptions{GoDB: true}

	dst := newInstall(t, "sqlite")
	s := dst.service(t, "x")
	s.o.DBPath = filepath.Join(t.TempDir(), "console-db_path.sqlite3")
	s.o.ServerDB = dst.dbPath
	res, err := s.RestoreFullBackup(ctx, archive, opts)
	if err != nil || !slices.Contains(res.Restored, "goserver-db") || len(res.Warnings) != 0 {
		t.Fatalf("restore: %+v %v", res, err)
	}
	if _, err := os.Stat(dst.dbPath + RestoredSuffix); err != nil {
		t.Errorf("nothing next to the server's database: %v", err)
	}
	if _, err := os.Stat(s.o.DBPath + RestoredSuffix); err == nil {
		t.Error("a file next to the console's DB_PATH")
	}

	s.o.ServerDB = "postgres://betterdesk@localhost/betterdesk"
	if res, err := s.RestoreFullBackup(ctx, archive, opts); err != nil || !slices.Contains(res.Skipped, "goserver-db") || res.RequiresRestart {
		t.Errorf("a PostgreSQL server: %+v %v", res, err)
	}

	dialects(t, func(t *testing.T, dialect string) {
		if dialect != "postgres" {
			return
		}
		pg := newInstall(t, "postgres")
		s := pg.service(t, "x")
		if err := os.WriteFile(s.o.DBPath, []byte("a stale file"), 0o600); err != nil {
			t.Fatal(err)
		}
		res, err := s.RestoreFullBackup(ctx, archive, opts)
		if err != nil || !slices.Contains(res.Skipped, "goserver-db") || res.RequiresRestart {
			t.Errorf("a console on PostgreSQL: %+v %v", res, err)
		}
		if _, err := os.Stat(s.o.DBPath + RestoredSuffix); err == nil {
			t.Error("a file next to the stale one")
		}
	})
}
