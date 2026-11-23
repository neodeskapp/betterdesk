package automation_test

import (
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/replay"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// A rule name or a command payload that is no text is refused like a
// missing one, with 400 and nothing stored, also null in a change (BUGS
// AU1). The old routes failed with 500 on name.trim().
func TestANameThatIsNoTextIsRefused(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		seedOne(t, db)
		w := replay.NewWorld(t, db, nil)
		before := state(w)
		for _, name := range []string{`5`, `true`, `false`, `null`, `["List"]`, `{"a":1}`, `0`} {
			got := w.Do(replay.Request{Role: "admin", Method: "POST", Path: "/api/automation/rules", Body: replay.Text(`{"name":` + name + `,"condition_type":"custom"}`)})
			if want := `{"error":"Rule name is required"}`; got.Status != 400 || got.Body != want {
				t.Errorf("a new rule named %s: %d %s, want 400 %s", name, got.Status, got.Body, want)
			}
			got = w.Do(replay.Request{Role: "admin", Method: "PATCH", Path: "/api/automation/rules/1", Body: replay.Text(`{"name":` + name + `}`)})
			if want := `{"error":"Rule name is required"}`; got.Status != 400 || got.Body != want {
				t.Errorf("rule 1 renamed to %s: %d %s, want 400 %s", name, got.Status, got.Body, want)
			}
			got = w.Do(replay.Request{Role: "admin", Method: "POST", Path: "/api/automation/commands", Body: replay.Text(`{"device_id":"D1","payload":` + name + `}`)})
			if want := `{"error":"payload is required"}`; got.Status != 400 || got.Body != want {
				t.Errorf("a command with the payload %s: %d %s, want 400 %s", name, got.Status, got.Body, want)
			}
		}
		if after := state(w); after != before {
			t.Errorf("the refused requests changed something:\nbefore %s\n after %s", before, after)
		}
	})
}
