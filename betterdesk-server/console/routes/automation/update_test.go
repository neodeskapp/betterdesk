package automation_test

import (
	"testing"

	"github.com/unitronix/betterdesk-server/console/routes/internal/replay"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// A change of a rule checks what the create checks (BUGS AU4): a name
// that is blank, a severity that is none of the three and a
// condition_value of null are refused with 400 and change nothing, and
// enabled is read as on a new rule, so "false" and "0" turn a rule off.
// The old route stored a severity as it came, failed on null with 500
// and turned a rule on for every value that is true in JavaScript.
func TestUpdateChecksLikeCreate(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		seedOne(t, db)
		w := replay.NewWorld(t, db, nil)
		const columns = "SELECT id, name, enabled, severity, condition_value FROM alert_rules ORDER BY id"
		before := w.Rows(columns, nil)
		for body, want := range map[string]string{
			`{"name":""}`:                                  "Rule name is required",
			`{"name":" \t  "}`:                             "Rule name is required",
			`{"severity":"anything goes"}`:                 "Invalid severity",
			`{"severity":"Critical"}`:                      "Invalid severity",
			`{"severity":""}`:                              "Invalid severity",
			`{"severity":null}`:                            "Invalid severity",
			`{"severity":5}`:                               "Invalid severity",
			`{"severity":["info"]}`:                        "Invalid severity",
			`{"condition_value":null}`:                     "Invalid condition_value",
			`{"description":"not written","severity":"x"}`: "Invalid severity",
			`{"enabled":false,"condition_value":null}`:     "Invalid condition_value",
			`{"name":"Renamed","severity":"info","enabled":0,"condition_value":null}`: "Invalid condition_value",
		} {
			got := w.Do(replay.Request{Role: "admin", Method: "PATCH", Path: "/api/automation/rules/1", Body: replay.Text(body)})
			if got.Status != 400 || got.Body != `{"error":"`+want+`"}` {
				t.Errorf("rule 1 changed with %s: %d %s, want 400 %s", body, got.Status, got.Body, want)
			}
		}
		if after := w.Rows(columns, nil); after != before {
			t.Errorf("the refused changes changed something:\nbefore %s\n after %s", before, after)
		}

		on := db.Pick("1", "true")
		off := db.Pick("0", "false")
		for _, tt := range []struct{ enabled, stored string }{
			{`"false"`, off}, {`true`, on}, {`"0"`, off}, {`"no"`, on}, {`0`, off}, {`null`, on}, {`false`, off}, {`"False"`, on}, {`[]`, on},
		} {
			got := w.Do(replay.Request{Role: "admin", Method: "PATCH", Path: "/api/automation/rules/2", Body: replay.Text(`{"enabled":` + tt.enabled + `,"severity":"critical"}`)})
			if got.Status != 200 {
				t.Fatalf("rule 2 with enabled %s: %d %s", tt.enabled, got.Status, got.Body)
			}
			if stored, want := w.Rows("SELECT enabled, severity FROM alert_rules WHERE id = 2", nil), `[{"enabled":`+tt.stored+`,"severity":"critical"}]`; stored != want {
				t.Errorf("enabled %s: stored %s, want %s", tt.enabled, stored, want)
			}
		}
	})
}
