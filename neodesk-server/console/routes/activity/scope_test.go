package activity_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/routes/internal/routetest"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// TestReadRoutesKeepToTheScope: an account limited to some devices finds
// only their summaries in the list and gets 403 for the sessions and top
// apps of a device outside its scope; the viewer, whose scope has C, and
// the admin read them (BUGS RV61).
func TestReadRoutesKeepToTheScope(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		api := routetest.NewAPI()
		api.Fallback = func(c routetest.Call) routetest.Reply {
			switch {
			case c.Method == "GET" && c.Target == "/api/peers":
				return routetest.Reply{Status: 200, Body: `{"peers":[{"id":"A","hostname":"a"},{"id":"C","hostname":"c"}]}`}
			case c.Method == "GET" && strings.HasPrefix(c.Target, "/api/remote-targets"):
				return routetest.Reply{Status: 200, Body: `{"targets":[]}`}
			}
			return routetest.Reply{Status: 404, Body: `{"error":"not found"}`}
		}
		w := routetest.New(t, routetest.Options{DB: db, API: api})
		for _, q := range []string{
			`INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES ('g-viewer', 'Viewer only', '', '', 'manual', '')`,
			`INSERT INTO device_group_user_access (device_group_id, user_id) SELECT id, 6 FROM device_groups WHERE guid = 'g-viewer'`,
			`INSERT INTO device_group_members (device_group_id, peer_id) SELECT id, 'C' FROM device_groups WHERE guid = 'g-viewer'`,
			`INSERT INTO activity_summaries (device_id, idle_seconds, session_count, total_active_secs, reported_at) VALUES ('A', 1, 1, 60, '2026-01-01 10:00:00'), ('C', 2, 1, 70, '2026-01-01 11:00:00')`,
			`INSERT INTO activity_sessions (device_id, app_name, window_title, started_at, ended_at, duration_secs) VALUES ('A', 'editor.exe', 'Notes', '2026-01-01 10:00:00', '2026-01-01 10:01:00', 60), ('C', 'bank.exe', 'Secret window title of C', '2026-01-01 11:00:00', '2026-01-01 11:01:10', 70)`,
		} {
			if _, err := db.Exec(context.Background(), q); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
		operator := w.As("operator")
		if got := operator.Do("GET", "/api/activity", ""); got.Status != http.StatusOK || strings.Contains(got.Body, `"device_id":"C"`) || !strings.Contains(got.Body, `"device_id":"A"`) || !strings.Contains(got.Body, `"total":1}`) {
			t.Errorf("the operator's list: %d %s", got.Status, got.Body)
		}
		for _, path := range []string{"/api/activity/C", "/api/activity/C/top", "/api/activity/NOPE"} {
			if got := operator.Do("GET", path, ""); got.Status != http.StatusForbidden || strings.Contains(got.Body, "bank.exe") {
				t.Errorf("GET %s as operator: %d %s, want 403", path, got.Status, got.Body)
			}
		}
		for _, path := range []string{"/api/activity/A", "/api/activity/A/top"} {
			if got := operator.Do("GET", path, ""); got.Status != http.StatusOK || !strings.Contains(got.Body, "editor.exe") {
				t.Errorf("GET %s as operator: %d %s", path, got.Status, got.Body)
			}
		}
		for _, role := range []string{"viewer", "admin"} {
			for _, path := range []string{"/api/activity/C", "/api/activity/C/top"} {
				if got := w.As(role).Do("GET", path, ""); got.Status != http.StatusOK || !strings.Contains(got.Body, "bank.exe") {
					t.Errorf("GET %s as %s: %d %s", path, role, got.Status, got.Body)
				}
			}
			if got := w.As(role).Do("GET", "/api/activity", ""); !strings.Contains(got.Body, `"device_id":"C"`) || !strings.Contains(got.Body, `"total":2}`) {
				t.Errorf("GET /api/activity as %s: %d %s", role, got.Status, got.Body)
			}
		}
	})
}
