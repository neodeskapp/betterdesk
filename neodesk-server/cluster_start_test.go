package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/cluster"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

func openMigratedSQLite(t *testing.T) (db.Database, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "db_v2.sqlite3")
	database, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.Migrate(); err != nil {
		t.Fatal(err)
	}
	return database, path
}

// TestASecondInstanceOnSQLiteRefusesToStart: a server that finds another
// live instance on its SQLite database stops with what it lacks, and
// leaves no row of its own behind.
func TestASecondInstanceOnSQLiteRefusesToStart(t *testing.T) {
	database, path := openMigratedSQLite(t)
	now := time.Now()
	if err := database.TouchServerInstance(db.ServerInstance{Name: "first", StartedAt: now, LastSeen: now}); err != nil {
		t.Fatal(err)
	}

	rt := &clusterRuntime{cfg: cluster.Config{Name: "second"}, leases: cluster.Solo{}}
	_, err := rt.join(database, path, now)
	if err == nil {
		t.Fatal("a second instance started on SQLite without Valkey")
	}
	for _, want := range []string{"first", "PostgreSQL", "VALKEY_URL"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q doesn't name %q", err, want)
		}
	}
	rows, _ := database.ListServerInstances(now.Add(-time.Minute))
	if len(rows) != 1 || rows[0].Name != "first" {
		t.Fatalf("rows after the refusal: %+v", rows)
	}
}

// TestAServerAloneStartsAsBefore: one instance, no Valkey, SQLite: the
// start goes on and every lease is held.
func TestAServerAloneStartsAsBefore(t *testing.T) {
	database, path := openMigratedSQLite(t)
	rt := &clusterRuntime{cfg: cluster.Config{Name: "only"}, leases: cluster.Solo{}}
	others, err := rt.join(database, path, time.Now())
	if err != nil || others != 0 {
		t.Fatalf("join = %d, %v", others, err)
	}
	rt.startLeases(database)
	if !rt.leases.Held("anything") {
		t.Fatal("a lone server doesn't hold its leases")
	}
	if ok, why := rt.ready(); !ok {
		t.Fatalf("a lone server isn't ready: %s", why)
	}
	rt.draining.Store(true)
	if ok, _ := rt.ready(); ok {
		t.Fatal("a draining server still reports ready")
	}
}

// TestARestartIsNotASecondInstance: the row a previous run of the same
// instance left doesn't block its restart.
func TestARestartIsNotASecondInstance(t *testing.T) {
	database, path := openMigratedSQLite(t)
	now := time.Now()
	_ = database.TouchServerInstance(db.ServerInstance{Name: "same", StartedAt: now.Add(-time.Hour), LastSeen: now.Add(-time.Second)})
	rt := &clusterRuntime{cfg: cluster.Config{Name: "same"}, leases: cluster.Solo{}}
	if _, err := rt.join(database, path, now); err != nil {
		t.Fatalf("restart refused: %v", err)
	}
}
