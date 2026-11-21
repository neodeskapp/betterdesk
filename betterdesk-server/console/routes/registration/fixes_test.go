package registration_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/routes/internal/routetest"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// An approval for a device the API server knows asks first whether the
// approver may see it: an operator with enrollment.approve and a limited
// scope is refused for a device outside it, and the device keeps its
// folder, name and tags; a request for a device nobody knows yet is
// approved as before (BUGS RV6).
func TestApprovalOfAKnownDeviceAsksForTheScope(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		api := routetest.NewAPI()
		api.Fallback = func(c routetest.Call) routetest.Reply {
			path, _, _ := strings.Cut(c.Target, "?")
			switch {
			case path == "/api/peers":
				return routetest.Reply{Status: http.StatusOK, Body: `[{"id":"DEV-HID"}]`}
			case path == "/api/peers/DEV-HID":
				return routetest.Reply{Status: http.StatusOK, Body: `{"id":"DEV-HID"}`}
			case path == "/api/remote-targets":
				return routetest.Reply{Status: http.StatusOK, Body: `[]`}
			}
			return routetest.Reply{Status: http.StatusNotFound, Body: `{"error":"not found"}`}
		}
		w := routetest.New(t, routetest.Options{DB: db, API: api, Roles: routetest.Overrides{"operator": {"enrollment.approve": true}}})
		ctx := context.Background()
		for _, stmt := range []string{
			"INSERT INTO pending_registrations (id, device_id, status) VALUES (1, 'DEV-HID', 'pending'), (2, 'DEV-NEW', 'pending')",
			"INSERT INTO folders (id, name) VALUES (1, 'Lab')",
			"INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES ('dg-closed', 'Closed', '', '', 'manual', '')",
			"INSERT INTO device_group_user_access (device_group_id, user_id) SELECT id, 1 FROM device_groups WHERE guid = 'dg-closed'",
			"INSERT INTO device_group_members (device_group_id, peer_id) SELECT id, 'DEV-HID' FROM device_groups WHERE guid = 'dg-closed'",
		} {
			if _, err := db.Exec(ctx, stmt); err != nil {
				t.Fatalf("%s: %v", stmt, err)
			}
		}
		operator := w.As("operator")
		body := `{"folder_id":1,"display_name":"Front desk","tags":"moved"}`
		if got := operator.Do(http.MethodPut, "/api/registrations/1/approve", body); got.Status != http.StatusForbidden {
			t.Errorf("a known device outside the scope: %d %s", got.Status, got.Body)
		}
		for _, call := range api.Calls() {
			if !strings.HasPrefix(call, "GET ") {
				t.Errorf("the API server was asked to change something: %s", call)
			}
		}
		var status string
		if err := db.QueryRow(ctx, "SELECT status FROM pending_registrations WHERE id = 1").Scan(&status); err != nil || status != "pending" {
			t.Errorf("status %q (%v), want pending", status, err)
		}
		var n int
		if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM device_folder_assignments WHERE device_id = 'DEV-HID'").Scan(&n); err != nil || n != 0 {
			t.Errorf("DEV-HID has %d folder assignments (%v)", n, err)
		}

		if got := operator.Do(http.MethodPut, "/api/registrations/2/approve", body); got.Status != http.StatusOK {
			t.Errorf("a new device: %d %s", got.Status, got.Body)
		}
		if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM device_folder_assignments WHERE device_id = 'DEV-NEW'").Scan(&n); err != nil || n != 1 {
			t.Errorf("DEV-NEW has %d folder assignments (%v)", n, err)
		}
	})
}
