package store_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// goTables are the tables the Go console adds on top of what Node set up.
var goTables = []string{"user_preferences"}

// createGoTables creates the tables the Go console adds on a reference
// database, from the same schema files Migrate runs, so that its catalog
// is what Migrate leaves.
func createGoTables(t *testing.T, db *store.DB) {
	t.Helper()
	for _, table := range goTables {
		text, err := os.ReadFile(filepath.Join("schema", db.Dialect().String(), table+".sql"))
		if err != nil {
			t.Fatal(err)
		}
		for _, stmt := range store.SplitStatements(string(text)) {
			mustExec(t, db, stmt)
		}
	}
}

// withGoTables is the catalog with the tables of goTables as Migrate makes
// them on a database of db's dialect that the Node console set up.
func (c catalog) withGoTables(t *testing.T, db *store.DB) catalog {
	t.Helper()
	ref := storetest.SQLite(t)
	if db.IsPostgres() {
		ref = storetest.Postgres(t)
	}
	storetest.NodeSchema(t, ref)
	createGoTables(t, ref)
	out := catalog{}
	for k, v := range c {
		out[k] = v
	}
	for k, v := range readCatalog(t, ref).only(goTables) {
		out[k] = v
	}
	return out
}

// Migrate makes user_preferences, on a new database and on one the Node
// console set up. A row goes with its account.
func TestMigrateAddsUserPreferences(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		storetest.NodeSchema(t, db)
		mustMigrate(t, db)
		mustExec(t, db, `INSERT INTO users (id, username, password_hash, role) VALUES (41, 'prefs', 'x', 'viewer')`)
		mustExec(t, db, `INSERT INTO user_preferences (user_id, accessibility) VALUES (41, '{"underlineLinks":true}')`)
		rows := queryStrings(t, db, `SELECT user_id, accessibility FROM user_preferences`)
		if len(rows) != 1 || rows[0][1] != `{"underlineLinks":true}` {
			t.Fatalf("user_preferences = %v", rows)
		}
		mustExec(t, db, `DELETE FROM users WHERE id = 41`)
		if rows := queryStrings(t, db, `SELECT user_id FROM user_preferences`); len(rows) != 0 {
			t.Errorf("the row stays after its account went: %v", rows)
		}
	})
}
