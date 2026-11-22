package store_test

import (
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// conditionValueType is the type of alert_rules.condition_value on
// PostgreSQL.
func conditionValueType(t *testing.T, db *store.DB) string {
	t.Helper()
	rows := queryStrings(t, db, `SELECT data_type FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'alert_rules' AND column_name = 'condition_value'`)
	if len(rows) != 1 {
		t.Fatalf("condition_value: %v", rows)
	}
	return rows[0][0]
}

// On PostgreSQL alert_rules.condition_value is double precision. The
// Node console made it a four-byte real, which kept 16777217 as 16777216
// and 123456.789 as 123456.79, so a rule compared otherwise than on SQLite
// (BUGS AE4). Migrate widens the column of an existing install and keeps
// every stored limit as the console read it: the shortest number that
// names the four bytes (0.1, not 0.10000000149011612). A second run
// changes nothing.
func TestMigrateWidensConditionValue(t *testing.T) {
	db := storetest.Postgres(t)
	storetest.NodeSchema(t, db)
	if got := conditionValueType(t, db); got != "real" {
		t.Fatalf("the Node schema has condition_value as %s", got)
	}
	limits := []string{"0.1", "123456.789", "16777217", "-2.5", "90", "1e-7", "3.4028235e38", "NaN", "Infinity", "-Infinity"}
	for _, limit := range limits {
		mustExec(t, db, `INSERT INTO alert_rules (name, condition_type, condition_value) VALUES (?, 'cpu_usage', CAST(? AS real))`, "Rule "+limit, limit)
	}
	want := []string{"0.1", "123456.79", "16777216", "-2.5", "90", "1e-07", "3.4028235e+38", "NaN", "Infinity", "-Infinity"}

	for run := 1; run <= 2; run++ {
		logged := captureLog(t, func() { mustMigrate(t, db) })
		// The first run also adds access_tokens.token_kind (BUGS RV1)
		if wantLog := map[int]string{1: "[DB] Migration: widened alert_rules.condition_value to double precision\n", 2: ""}[run]; !strings.Contains(logged, wantLog) || run == 2 && logged != "" {
			t.Errorf("run %d logged %q, want %q", run, logged, wantLog)
		}
		if got := conditionValueType(t, db); got != "double precision" {
			t.Errorf("run %d: condition_value is %s", run, got)
		}
		rows := queryStrings(t, db, `SELECT condition_value::text FROM alert_rules ORDER BY id`)
		for i, row := range rows {
			if row[0] != want[i] {
				t.Errorf("run %d: the limit %s is %s after Migrate, want %s", run, limits[i], row[0], want[i])
			}
		}
		if len(rows) != len(want) {
			t.Fatalf("run %d: %d rules", run, len(rows))
		}
	}
	// A new rule keeps its eight bytes
	mustExec(t, db, `INSERT INTO alert_rules (name, condition_type, condition_value) VALUES ('Exact', 'cpu_usage', ?)`, 16777217.0)
	if rows := queryStrings(t, db, `SELECT condition_value::text FROM alert_rules WHERE name = 'Exact'`); rows[0][0] != "16777217" {
		t.Errorf("a new limit of 16777217 is stored as %s", rows[0][0])
	}

	// A fresh database gets the column with eight bytes from its file, and
	// there is nothing to widen
	fresh := storetest.Postgres(t)
	if out := captureLog(t, func() { mustMigrate(t, fresh) }); strings.Contains(out, "widened") {
		t.Errorf("the first Migrate of a fresh database widened the column:\n%s", out)
	}
	if got := conditionValueType(t, fresh); got != "double precision" {
		t.Errorf("a fresh database has condition_value as %s", got)
	}
}
