package inventory_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// scopedWorld is a console whose API server knows the devices A and C,
// with C in a device group only e2e_viewer may see, so e2e_operator may
// not see it.
func scopedWorld(t *testing.T, db *store.DB) *routetest.World {
	t.Helper()
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
	} {
		if _, err := db.Exec(context.Background(), q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	return w
}

// TestReadRoutesKeepToTheScope: an account limited to some devices finds
// only those in the inventory list, also in the list of known devices
// shown while none has reported, and gets 403 for the report of a device
// outside its scope; the viewer, whose scope has C, and the admin read
// C's report (BUGS RV60).
func TestReadRoutesKeepToTheScope(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := scopedWorld(t, db)
		operator := w.As("operator")
		if got := operator.Do("GET", "/api/inventory", ""); got.Status != http.StatusOK || strings.Contains(got.Body, `"device_id":"C"`) || !strings.Contains(got.Body, `"device_id":"A"`) || !strings.Contains(got.Body, `"total":1,`) {
			t.Errorf("the operator's list of known devices: %d %s", got.Status, got.Body)
		}
		ctx := context.Background()
		for _, q := range []string{
			`INSERT INTO device_inventory (device_id, hardware, software) VALUES ('A', '{"hostname":"host-a"}', '{}'), ('C', '{"hostname":"secret-host-c"}', '{"apps":[{"name":"SecretApp"}]}')`,
			`INSERT INTO device_telemetry (device_id, cpu_usage_percent) VALUES ('C', 5)`,
		} {
			if _, err := db.Exec(ctx, q); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
		if got := operator.Do("GET", "/api/inventory", ""); got.Status != http.StatusOK || strings.Contains(got.Body, "secret-host-c") || !strings.Contains(got.Body, "host-a") || !strings.Contains(got.Body, `"total":1,`) {
			t.Errorf("the operator's list: %d %s", got.Status, got.Body)
		}
		for _, path := range []string{"/api/inventory/C", "/api/inventory/NOPE"} {
			if got := operator.Do("GET", path, ""); got.Status != http.StatusForbidden || strings.Contains(got.Body, "Secret") {
				t.Errorf("GET %s as operator: %d %s, want 403", path, got.Status, got.Body)
			}
		}
		if got := operator.Do("GET", "/api/inventory/A", ""); got.Status != http.StatusOK || !strings.Contains(got.Body, "host-a") {
			t.Errorf("GET /api/inventory/A as operator: %d %s", got.Status, got.Body)
		}
		for _, role := range []string{"viewer", "admin"} {
			if got := w.As(role).Do("GET", "/api/inventory/C", ""); got.Status != http.StatusOK || !strings.Contains(got.Body, "SecretApp") {
				t.Errorf("GET /api/inventory/C as %s: %d %s", role, got.Status, got.Body)
			}
			if got := w.As(role).Do("GET", "/api/inventory", ""); !strings.Contains(got.Body, "secret-host-c") || !strings.Contains(got.Body, `"total":2,`) {
				t.Errorf("GET /api/inventory as %s: %d %s", role, got.Status, got.Body)
			}
		}
	})
}
