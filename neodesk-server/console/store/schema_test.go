package store_test

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// sessionTable belongs to the session package: Migrate leaves it out, the
// Node dumps have it.
const sessionTable = "panel_sessions"

// A database the Go console sets up is the database the Node console set
// up: same tables, columns, defaults, constraints and indexes, and on
// SQLite the same stored statement text. On PostgreSQL
// alert_rules.condition_value is double precision (BUGS AE4), access_tokens
// has token_kind (BUGS RV1) and users has disabled (L107). The Go console
// adds the tables of goTables.
func TestMigrateFreshEqualsNodeDump(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		mustMigrate(t, db)
		got := readCatalog(t, db)

		node := storetest.SQLite(t)
		if db.IsPostgres() {
			node = storetest.Postgres(t)
		}
		storetest.NodeSchema(t, node)
		createGoTables(t, node)
		want := readCatalog(t, node).without(sessionTable).wideConditionValue().withTokenKind(db.IsPostgres()).withDisabled(db.IsPostgres()).withLater(db.IsPostgres())

		if n := len(want.tables()); n != 55+len(goTables) {
			t.Fatalf("the Node dump and the Go tables are %d tables besides %s, want %d", n, sessionTable, 55+len(goTables))
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
// AE4), adding access_tokens.token_kind, "account" in every row (BUGS
// RV1), keeping each access token as its hash only (BUGS RV128), and
// adding users.disabled, every account enabled (L107), and creating the
// tables of goTables. Running it again changes nothing.
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
		rowsAfter := snapshot{}
		for table, rows := range rowsBefore {
			rowsAfter[table] = rows
		}
		rowsAfter["access_tokens"] = nil
		for _, row := range rowsBefore["access_tokens"] {
			// id | token | token_hash | ...
			columns := strings.Split(row, " | ")
			hash := store.HashAccessToken(columns[1])
			columns[1], columns[2] = store.StoredAccessToken(hash), hash
			rowsAfter["access_tokens"] = append(rowsAfter["access_tokens"], strings.Join(columns, " | ")+" | account")
		}
		sort.Strings(rowsAfter["access_tokens"])
		enabled := " | 0"
		if db.IsPostgres() {
			enabled = " | false"
		}
		rowsAfter["users"] = nil
		for _, row := range rowsBefore["users"] {
			rowsAfter["users"] = append(rowsAfter["users"], row+enabled)
		}

		for table, rows := range rowsAfter {
			rowsAfter[table] = append([]string(nil), rows...)
		}
		withLaterRows(rowsAfter, db.IsPostgres())
		for run := 1; run <= 2; run++ {
			out := captureLog(t, func() { mustMigrate(t, db) })
			if run == 1 {
				want := []string{"[DB] Migration: added access_tokens.token_kind", "[DB] Migration: kept 2 access tokens as their hash only", "[DB] Migration: added users.disabled", "[DB] Migration: added audit_log.target_user_id", "[DB] Migration: created 1 tables: user_preferences"}
				if db.IsPostgres() {
					want = append([]string{"[DB] Migration: widened alert_rules.condition_value to double precision"}, want...)
				}
				got := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
				same := len(got) == len(want)
				for i := 0; same && i < len(got); i++ {
					same = strings.HasSuffix(got[i], want[i])
				}
				if !same {
					t.Errorf("run 1: Migrate on a Node database logged %q, want only %q", out, want)
				}
				out = ""
			}
			if out != "" {
				t.Errorf("run %d: Migrate found something to do on a Node database:\n%s", run, out)
			}
			requireSameCatalog(t, "catalog of a Node database after Migrate", catalogBefore.withGoTables(t, db).wideConditionValue().withTokenKind(db.IsPostgres()).withDisabled(db.IsPostgres()).withLater(db.IsPostgres()), readCatalog(t, db))
			requireSameSnapshot(t, "rows of a Node database after Migrate", rowsAfter, readSnapshot(t, db, tables))
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
		// The console writes a token as its hash only (BUGS RV128)
		mustExec(t, db, `UPDATE access_tokens SET token = 'sha256 ' || token_hash`)
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
	if n := len(storetest.Migrated(t).Shape().Tables()); n != 55+len(goTables) {
		t.Errorf("storetest.Migrated has %d tables, want %d", n, 55+len(goTables))
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

// The scheduled backup's network share login, with its password in clear,
// is deleted at the start, once, and the other settings stay (L123).
func TestMigrateDeletesTheBackupShareLogin(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		mustMigrate(t, db)
		mustExec(t, db, `INSERT INTO settings (key, value) VALUES ('scheduled_backup_smb', '{"host":"nas","share":"b","password":"hunter2"}'), ('scheduled_backup_config', '{"enabled":true}')`)
		out := captureLog(t, func() { mustMigrate(t, db) })
		if !strings.HasSuffix(strings.TrimSuffix(out, "\n"), "[DB] Migration: deleted the scheduled backup's network share login") {
			t.Errorf("Migrate logged %q", out)
		}
		var keys []string
		rows, err := db.Query(context.Background(), `SELECT key FROM settings ORDER BY key`)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var k string
			if err := rows.Scan(&k); err != nil {
				t.Fatal(err)
			}
			keys = append(keys, k)
		}
		rows.Close()
		if strings.Join(keys, ",") != "scheduled_backup_config" {
			t.Errorf("settings after Migrate: %v", keys)
		}
		if again := captureLog(t, func() { mustMigrate(t, db) }); again != "" {
			t.Errorf("a second Migrate logged %q", again)
		}
	})
}
