package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

// splitInstall writes the old console's split layout, a main file and an
// auth.db, from the SQL text console/authimport's fixtures took from the
// real Node console, and returns their paths.
func splitInstall(t *testing.T) (mainPath, authPath string) {
	t.Helper()
	dir := t.TempDir()
	mainPath, authPath = filepath.Join(dir, "db_v2.sqlite3"), filepath.Join(dir, "auth.db")
	for path, dump := range map[string]string{mainPath: "main.sql", authPath: "auth.sql"} {
		text, err := os.ReadFile(filepath.Join("console", "authimport", "testdata", "split", dump))
		if err != nil {
			t.Fatal(err)
		}
		handle, err := sql.Open("sqlite", "file:"+path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := handle.Exec(string(text)); err != nil {
			t.Fatalf("%s: %v", dump, err)
		}
		_ = handle.Close()
	}
	// The server has made its tables in the main file
	database, err := db.OpenSQLite(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.Migrate(); err != nil {
		t.Fatal(err)
	}
	return mainPath, authPath
}

// TestMigrateSQLiteAuthFlagImportsAnAuthDB: -migrate-sqlite-auth moves an
// auth.db of the current console into the main file, accounts with their
// console columns, and writes the marker; the dry run leaves both files as
// they are (BUGS D7).
func TestMigrateSQLiteAuthFlagImportsAnAuthDB(t *testing.T) {
	ctx := context.Background()
	t.Run("dry run", func(t *testing.T) {
		mainPath, authPath := splitInstall(t)
		before, err := os.ReadFile(mainPath)
		if err != nil {
			t.Fatal(err)
		}
		outcome, err := migrateSQLiteAuth(ctx, mainPath, authPath, "", true)
		if err != nil {
			t.Fatalf("dry run: %v", err)
		}
		if raw, _ := json.Marshal(outcome); !strings.Contains(string(raw), `"dry_run":true`) || outcome.Report == nil || outcome.UsersAdded == 0 {
			t.Errorf("dry run outcome: %s", raw)
		}
		after, err := os.ReadFile(mainPath)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(before) {
			t.Error("the dry run changed the main file")
		}
	})
	t.Run("import", func(t *testing.T) {
		mainPath, authPath := splitInstall(t)
		if _, err := migrateSQLiteAuth(ctx, mainPath, authPath, filepath.Join(t.TempDir(), "backups"), false); err != nil {
			t.Fatalf("import: %v", err)
		}
		if done, err := db.SQLiteAuthConsolidated(mainPath); err != nil || !done {
			t.Errorf("marker after the import: %v %v", done, err)
		}
		handle, err := sql.Open("sqlite", "file:"+mainPath)
		if err != nil {
			t.Fatal(err)
		}
		defer handle.Close()
		var email string
		if err := handle.QueryRow(`SELECT email FROM users WHERE username = 'alice'`).Scan(&email); err != nil || email != "alice@example.com" {
			t.Errorf("alice after the import: %q %v", email, err)
		}
	})
}
