package settingsbackup

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/backup"
	"github.com/neodeskapp/neodesk/neodesk-server/console/config"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// RV18: the service NewBackups builds backs up and restores the server's
// database at the DSN it is given, not at the console's DB_PATH.
func TestNewBackupsTakesTheServersDatabase(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	serverDB := filepath.Join(dir, "server.sqlite3")
	db, err := store.OpenSQLite(serverDB)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(ctx, `CREATE TABLE marker (x TEXT)`); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{DataDir: filepath.Join(dir, "data"), KeysPath: dir, DBPath: filepath.Join(dir, "console-db_path.sqlite3")}
	d := &kit.Deps{Config: cfg, Store: db, Log: kit.NewLogger("error", false)}

	backups := NewBackups(d, serverDB)
	archive, _, err := backups.CreateFullBackup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	res, err := backups.RestoreFullBackup(ctx, archive, backup.RestoreOptions{GoDB: true})
	if err != nil || !slices.Contains(res.Restored, "goserver-db") {
		t.Fatalf("restore: %+v %v", res, err)
	}
	if _, err := os.Stat(serverDB + backup.RestoredSuffix); err != nil {
		t.Errorf("nothing next to the server's database: %v", err)
	}
	if _, err := os.Stat(cfg.DBPath + backup.RestoredSuffix); err == nil {
		t.Error("a file next to DB_PATH")
	}
}
