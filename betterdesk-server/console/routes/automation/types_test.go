package automation_test

import (
	"testing"

	"github.com/unitronix/betterdesk-server/console/routes/internal/replay"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// The fields of a rule take their type only, on a new rule and in a
// change, with 400 and nothing stored otherwise, the same on both
// databases (BUGS AU2): text or null for description, scope_device_id and
// notify_emails, a finite number or null for condition_value, a whole
// number of seconds from 0 or null for cooldown_secs. SQLite stored a
// number in a text field as "5.0", a list as its elements and text in a
// number field; PostgreSQL stored "5" and "true" or failed.
func TestRuleFieldsHaveTheirTypes(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		seedOne(t, db)
		w := replay.NewWorld(t, db, nil)
		const allColumns = "SELECT id, name, description, enabled, condition_type, condition_op, condition_value, severity, scope_device_id, cooldown_secs, notify_emails, created_by FROM alert_rules ORDER BY id"
		before := state(w) + w.Rows(allColumns, nil)
		texts := []string{`5`, `0`, `true`, `false`, `[]`, `["a"]`, `["a","b"]`, `{}`, `{"to":"x"}`}
		numbers := []string{`"12.5"`, `""`, `"high"`, `true`, `false`, `[]`, `[5]`, `{}`, `1e400`, `-1e400`}
		refused := map[string][]string{
			"description":     texts,
			"scope_device_id": texts,
			"notify_emails":   texts,
			"condition_value": numbers,
			"cooldown_secs":   append(numbers, `1.5`, `-1`, `-0.5`, `2147483648`, `1e21`),
		}
		for field, values := range refused {
			want := `{"error":"Invalid ` + field + `"}`
			for _, value := range values {
				got := w.Do(replay.Request{Role: "admin", Method: "POST", Path: "/api/automation/rules",
					Body: replay.Text(`{"name":"Typed","condition_type":"custom","` + field + `":` + value + `}`)})
				if got.Status != 400 || got.Body != want {
					t.Errorf("a new rule with %s %s: %d %s, want 400 %s", field, value, got.Status, got.Body, want)
				}
				got = w.Do(replay.Request{Role: "admin", Method: "PATCH", Path: "/api/automation/rules/1",
					Body: replay.Text(`{"description":"not written","` + field + `":` + value + `}`)})
				if got.Status != 400 || got.Body != want {
					t.Errorf("rule 1 changed to %s %s: %d %s, want 400 %s", field, value, got.Status, got.Body, want)
				}
			}
		}
		if after := state(w) + w.Rows(allColumns, nil); after != before {
			t.Errorf("the refused requests changed something:\nbefore %s\n after %s", before, after)
		}

		// Values of the type are stored as they are, the same on both
		// databases
		got := w.Do(replay.Request{Role: "admin", Method: "POST", Path: "/api/automation/rules",
			Body: replay.Text(`{"name":"Typed","condition_type":"custom","description":"text","condition_value":-12.25,"scope_device_id":"D7","cooldown_secs":2147483647,"notify_emails":"ops@example.com"}`)})
		if got.Status != 201 {
			t.Fatalf("a new rule with every type right: %d %s", got.Status, got.Body)
		}
		got = w.Do(replay.Request{Role: "admin", Method: "PATCH", Path: "/api/automation/rules/2",
			Body: replay.Text(`{"description":null,"condition_value":7,"scope_device_id":null,"cooldown_secs":0,"notify_emails":null}`)})
		if got.Status != 200 {
			t.Fatalf("rule 2 changed with every type right: %d %s", got.Status, got.Body)
		}
		const columns = "SELECT id, description, condition_value, scope_device_id, cooldown_secs, notify_emails FROM alert_rules WHERE id > 1 ORDER BY id"
		want := `[{"id":2,"description":null,"condition_value":7,"scope_device_id":null,"cooldown_secs":0,"notify_emails":null},` +
			`{"id":3,"description":"text","condition_value":-12.25,"scope_device_id":"D7","cooldown_secs":2147483647,"notify_emails":"ops@example.com"}]`
		if stored := w.Rows(columns, nil); stored != want {
			t.Errorf("stored:\n     %s\nwant %s", stored, want)
		}
	})
}
