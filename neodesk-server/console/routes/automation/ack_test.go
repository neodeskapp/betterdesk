package automation_test

import (
	"testing"

	"github.com/unitronix/betterdesk-server/console/routes/internal/replay"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// Acknowledging an alert that does not exist is 404, and an alert that is
// acknowledged keeps who acknowledged it first and when (BUGS AU5). The
// old route answered success for any id and wrote over the first
// acknowledgement.
func TestAcknowledgeKeepsTheFirst(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		seedOne(t, db)
		w := replay.NewWorld(t, db, nil)
		const ack = "SELECT id, acknowledged_by, CASE WHEN acknowledged_at < '2020-01-01' THEN 'before 2020' WHEN acknowledged_at IS NULL THEN 'never' ELSE 'now' END AS acknowledged_at FROM alert_history ORDER BY id"
		for _, id := range []string{"2", "999", "0", "2147483647"} {
			got := w.Do(replay.Request{Role: "admin", Method: "POST", Path: "/api/automation/alerts/" + id + "/ack", Body: replay.Text(`{}`)})
			if want := `{"error":"Alert not found"}`; got.Status != 404 || got.Body != want {
				t.Errorf("alert %s: %d %s, want 404 %s", id, got.Status, got.Body, want)
			}
		}
		if got := w.Rows(ack, nil); got != `[{"id":1,"acknowledged_by":null,"acknowledged_at":"never"}]` {
			t.Errorf("after the missing alerts: %s", got)
		}

		if got := w.Do(replay.Request{Role: "admin", Method: "POST", Path: "/api/automation/alerts/1/ack", Body: replay.Text(`{}`)}); got.Status != 200 || got.Body != `{"success":true}` {
			t.Fatalf("the first acknowledgement: %d %s", got.Status, got.Body)
		}
		if got := w.Rows(ack, nil); got != `[{"id":1,"acknowledged_by":"e2e_admin","acknowledged_at":"now"}]` {
			t.Errorf("after the first acknowledgement: %s", got)
		}
		w.Exec("UPDATE alert_history SET acknowledged_at = '2019-05-06 07:08:09' WHERE id = 1", nil)
		if got := w.Do(replay.Request{Role: "super_admin", Method: "POST", Path: "/api/automation/alerts/1/ack", Body: replay.Text(`{}`)}); got.Status != 200 || got.Body != `{"success":true}` {
			t.Errorf("an acknowledgement again: %d %s, want 200", got.Status, got.Body)
		}
		if got := w.Rows(ack, nil); got != `[{"id":1,"acknowledged_by":"e2e_admin","acknowledged_at":"before 2020"}]` {
			t.Errorf("after an acknowledgement again: %s", got)
		}
	})
}
