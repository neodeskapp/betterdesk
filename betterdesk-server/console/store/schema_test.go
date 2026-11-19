package store_test

import (
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// sessionTable belongs to the session package: Migrate leaves it out, the
// Node dumps have it.
const sessionTable = "panel_sessions"

// A database the Go console sets up is the database the Node console set
// up: same tables, columns, defaults, constraints and indexes, and on
// SQLite the same stored statement text. On PostgreSQL
// alert_rules.condition_value is double precision (BUGS AE4).
func TestMigrateFreshEqualsNodeDump(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		mustMigrate(t, db)
		got := readCatalog(t, db)

		node := storetest.SQLite(t)
		if db.IsPostgres() {
			node = storetest.Postgres(t)
		}
		storetest.NodeSchema(t, node)
		want := readCatalog(t, node).without(sessionTable).wideConditionValue()

		if n := len(want.tables()); n != 55 {
			t.Fatalf("the Node dump has %d tables besides %s, want 55", n, sessionTable)
		}
		requireSameCatalog(t, "fresh database against the Node dump", want, got)

		for _, table := range []string{"user_groups", "device_groups"} {
			rows := queryStrings(t, db, `SELECT name, note FROM `+table)
			if len(rows) != 1 || rows[0][0] != "Default" {
				t.Errorf("%s after the first migration = %v, want the one Default row", table, rows)
			}
		}
	})
}

// Migrate on a database the Node console created, with rows in every
// table, changes neither the schema nor a single row, and reports nothing
// done, but for widening alert_rules.condition_value on PostgreSQL (BUGS
// AE4). Running it again changes nothing.
func TestMigrateLeavesNodeDatabaseAlone(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		storetest.NodeSchema(t, db)
		tables := readCatalog(t, db).tables()
		if len(tables) != 56 {
			t.Fatalf("the Node dump has %d tables, want 56", len(tables))
		}
		fillTables(t, db, tables, 2)
		catalogBefore := readCatalog(t, db)
		rowsBefore := readSnapshot(t, db, tables)
		for table, rows := range rowsBefore {
			if len(rows) < 2 {
				t.Fatalf("fixture: %s has %d rows", table, len(rows))
			}
		}

		for run := 1; run <= 2; run++ {
			out := captureLog(t, func() { mustMigrate(t, db) })
			if run == 1 && db.IsPostgres() {
				const widened = "[DB] Migration: widened alert_rules.condition_value to double precision\n"
				if strings.Count(out, "\n") != 1 || !strings.HasSuffix(out, widened) {
					t.Errorf("run 1: Migrate on a Node database logged %q, want only %q", out, widened)
				}
				out = ""
			}
			if out != "" {
				t.Errorf("run %d: Migrate found something to do on a Node database:\n%s", run, out)
			}
			requireSameCatalog(t, "catalog of a Node database after Migrate", catalogBefore.wideConditionValue(), readCatalog(t, db))
			requireSameSnapshot(t, "rows of a Node database after Migrate", rowsBefore, readSnapshot(t, db, tables))
		}
	})
}

// A second Migrate on a database the first one made changes nothing: no
// second default group, no schema change.
func TestMigrateTwiceChangesNothing(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		mustMigrate(t, db)
		tables := readCatalog(t, db).tables()
		fillTables(t, db, tables, 2)
		catalogBefore := readCatalog(t, db)
		rowsBefore := readSnapshot(t, db, tables)

		mustMigrate(t, db)
		requireSameCatalog(t, "catalog after the second Migrate", catalogBefore, readCatalog(t, db))
		requireSameSnapshot(t, "rows after the second Migrate", rowsBefore, readSnapshot(t, db, tables))
	})
}

// The databases storetest hands to other packages have the console's
// schema and a shape to ask.
func TestStoretestMigratedDatabases(t *testing.T) {
	if n := len(storetest.Migrated(t).Shape().Tables()); n != 55 {
		t.Errorf("storetest.Migrated has %d tables, want 55", n)
	}
	ran := map[store.Dialect]bool{}
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ran[db.Dialect()] = true
		if !db.Shape().Table("users").Has("email") || db.Shape().Table("peers").Exists() {
			t.Error("EachMigrated: not the console's schema alone")
		}
	})
	if !ran[store.SQLite] {
		t.Error("EachMigrated did not run on SQLite")
	}
}
