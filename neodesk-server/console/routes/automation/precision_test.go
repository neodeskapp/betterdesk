package automation_test

import (
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/replay"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// nodeLimitColumn gives the console's alert_rules on PostgreSQL the
// four-byte limits of the Node console's column, so the routes are
// compared with Node's answers on the column Node had: a trigger keeps
// every limit as the shortest number that names its four bytes (as
// PostgreSQL prints a real) and refuses one beyond their range, and the
// seeded rules are rounded so. The console's own column keeps eight bytes
// (BUGS AE4); TestALimitKeepsEightBytes checks that.
var nodeLimitColumn = []replay.Seed{
	{SQL: `CREATE FUNCTION four_byte_limit() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			NEW.condition_value := CAST(CAST(CAST(NEW.condition_value AS real) AS text) AS double precision);
			RETURN NEW;
		END $$`},
	{SQL: `CREATE TRIGGER four_byte_limit BEFORE INSERT OR UPDATE ON alert_rules FOR EACH ROW EXECUTE FUNCTION four_byte_limit()`},
	{SQL: `UPDATE alert_rules SET condition_value = condition_value`},
}

// AE4: Node refused these two limits at the column, before the rule had
// an id; the console's insert takes an id before the trigger refuses it,
// and After gives it back so the next rules get Node's ids.
var _ = panelFixes.Add("AE4", map[string]replay.Fix{
	`admin POST /api/automation/rules {"name":"Rule 102","condition_type":"custom","condition_value":3.5e+38}`: {
		Dialect: "postgres", Status: 500, Answer: `{"error":"Internal server error"}`, After: lastIDBack,
	},
	`admin POST /api/automation/rules {"name":"Rule 103","condition_type":"custom","condition_value":1e+39}`: {
		Dialect: "postgres", Status: 500, Answer: `{"error":"Internal server error"}`, After: lastIDBack,
	},
})

// lastIDBack sets the id sequence of alert_rules back to the highest id.
var lastIDBack = replay.Write(`SELECT setval(pg_get_serial_sequence('alert_rules', 'id'), (SELECT MAX(id) FROM alert_rules))`)

// A rule's limit keeps its eight bytes on both databases: it is answered
// and read back as it was sent. Node's PostgreSQL column kept four bytes,
// so 16777217 came back as 16777216 there (BUGS AE4).
func TestALimitKeepsEightBytes(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newWorld(t, db)
		for _, body := range []string{`{"name":"Exact","condition_type":"custom","condition_value":16777217}`, `{"name":"Patched","condition_type":"custom","condition_value":1}`} {
			created := w.Do(replay.Request{Role: "admin", Method: "POST", Path: "/api/automation/rules", Body: replay.Text(body)})
			if created.Status != 201 || strings.Contains(body, "16777217") != strings.Contains(created.Body, `"condition_value":16777217,`) {
				t.Fatalf("create %s: %d %s", body, created.Status, created.Body)
			}
		}
		if got := w.Do(replay.Request{Role: "admin", Method: "PATCH", Path: "/api/automation/rules/2", Body: replay.Text(`{"condition_value":123456.789}`)}); got.Status != 200 {
			t.Fatalf("change: %d %s", got.Status, got.Body)
		}
		got := w.Do(replay.Request{Role: "admin", Method: "GET", Path: "/api/automation/rules"})
		for _, want := range []string{`"condition_value":16777217,`, `"condition_value":123456.789,`} {
			if !strings.Contains(got.Body, want) {
				t.Errorf("the rules lack %s: %s", want, got.Body)
			}
		}
	})
}
