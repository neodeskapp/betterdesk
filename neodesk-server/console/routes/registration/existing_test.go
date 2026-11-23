package registration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// collisionAPI is an API server with the device DEV-OLD (display name
// Front desk), the deleted device DEV-GONE and the device DEV-HID, which
// only the accounts of the device group dg-closed see (seedCollisions).
func collisionAPI() *routetest.API {
	api := routetest.NewAPI()
	api.Fallback = func(c routetest.Call) routetest.Reply {
		path, query, _ := strings.Cut(c.Target, "?")
		switch {
		case c.Method != http.MethodGet:
			return routetest.Reply{Status: http.StatusOK, Body: `{"success":true}`}
		case path == "/api/peers" && query == "include_deleted=true":
			return routetest.Reply{Status: http.StatusOK, Body: `[{"id":"DEV-OLD","hostname":"desk-7","display_name":"Front desk"},{"id":"DEV-GONE","hostname":"old-laptop","soft_deleted":true},{"id":"DEV-HID","hostname":"hidden"},{"id":"DEV-DONE","hostname":"done"}]`}
		case path == "/api/peers":
			return routetest.Reply{Status: http.StatusOK, Body: `[{"id":"DEV-OLD","hostname":"desk-7","display_name":"Front desk"},{"id":"DEV-HID","hostname":"hidden"},{"id":"DEV-DONE","hostname":"done"}]`}
		case path == "/api/peers/DEV-OLD":
			return routetest.Reply{Status: http.StatusOK, Body: `{"id":"DEV-OLD","hostname":"desk-7","display_name":"Front desk"}`}
		case path == "/api/peers/DEV-HID":
			return routetest.Reply{Status: http.StatusOK, Body: `{"id":"DEV-HID","hostname":"hidden"}`}
		case path == "/api/remote-targets":
			return routetest.Reply{Status: http.StatusOK, Body: `[]`}
		}
		return routetest.Reply{Status: http.StatusNotFound, Body: `{"error":"not found"}`}
	}
	return api
}

// seedCollisions adds five registrations: four waiting ones for DEV-OLD,
// DEV-GONE, DEV-NEW (no device yet) and DEV-HID, and an approved one for
// DEV-DONE, a device since; the folder Lab; and the device group
// dg-closed, which holds DEV-HID and lets only account 1 see it.
func seedCollisions(t *testing.T, db *store.DB) {
	t.Helper()
	for _, stmt := range []string{
		"INSERT INTO pending_registrations (id, device_id, status) VALUES (1, 'DEV-OLD', 'pending'), (2, 'DEV-GONE', 'pending'), (3, 'DEV-NEW', 'pending'), (4, 'DEV-HID', 'pending'), (5, 'DEV-DONE', 'approved')",
		"INSERT INTO folders (id, name) VALUES (1, 'Lab')",
		"INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES ('dg-closed', 'Closed', '', '', 'manual', '')",
		"INSERT INTO device_group_user_access (device_group_id, user_id) SELECT id, 1 FROM device_groups WHERE guid = 'dg-closed'",
		"INSERT INTO device_group_members (device_group_id, peer_id) SELECT id, 'DEV-HID' FROM device_groups WHERE guid = 'dg-closed'",
	} {
		if _, err := db.Exec(context.Background(), stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}

// The registration list marks a waiting request whose device id is
// already a device of the server, also a deleted one, with the device's
// name; for a device outside the approver's scope it says only that the
// id is taken. A new id and a decided request get no mark (BUGS L55).
func TestListMarksRequestsForExistingDevices(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := routetest.New(t, routetest.Options{DB: db, API: collisionAPI(), Roles: routetest.Overrides{"operator": {"enrollment.approve": true}}})
		seedCollisions(t, db)
		marks := func(role string) map[string]string {
			t.Helper()
			got := w.As(role).Do(http.MethodGet, "/api/registrations", "")
			var page struct {
				Data []struct {
					ID       float64         `json:"id"`
					Existing json.RawMessage `json:"existing_device"`
				} `json:"data"`
			}
			if err := json.Unmarshal([]byte(got.Body), &page); err != nil || got.Status != http.StatusOK || len(page.Data) != 5 {
				t.Fatalf("list as %s: %d %s", role, got.Status, got.Body)
			}
			out := map[string]string{}
			for _, row := range page.Data {
				out[strconv.Itoa(int(row.ID))] = string(row.Existing)
			}
			return out
		}
		want := map[string]string{
			"1": `{"id":"DEV-OLD","name":"Front desk","deleted":false,"in_scope":true}`,
			"2": `{"id":"DEV-GONE","name":"old-laptop","deleted":true,"in_scope":true}`,
			"3": ``,
			"4": `{"id":"DEV-HID","name":"hidden","deleted":false,"in_scope":true}`,
			"5": ``,
		}
		for id, mark := range marks("admin") {
			if mark != want[id] {
				t.Errorf("admin, request %s: %s, want %s", id, mark, want[id])
			}
		}
		want["4"] = `{"id":"DEV-HID","name":"","deleted":false,"in_scope":false}`
		for id, mark := range marks("operator") {
			if mark != want[id] {
				t.Errorf("operator, request %s: %s, want %s", id, mark, want[id])
			}
		}
	})
}

// Approving a request whose device id is already a device, also a deleted
// one, is refused with 409 until the request says the approver confirmed
// it, and nothing is stored or changed before. The answer names the
// device (BUGS L55).
func TestApprovalOfAnExistingDeviceNeedsConfirmation(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		api := collisionAPI()
		w := routetest.New(t, routetest.Options{DB: db, API: api})
		seedCollisions(t, db)
		ctx := context.Background()
		admin := w.As("admin")
		body := `{"folder_id":1,"display_name":"Moved","tags":"moved"}`
		for _, c := range []struct{ id, device string }{
			{"1", `{"id":"DEV-OLD","name":"Front desk","deleted":false,"in_scope":true}`},
			{"2", `{"id":"DEV-GONE","name":"old-laptop","deleted":true,"in_scope":true}`},
		} {
			api.Calls()
			got := admin.Do(http.MethodPut, "/api/registrations/"+c.id+"/approve", body)
			if got.Status != http.StatusConflict || !strings.Contains(got.Body, `"code":"existing_device","existing_device":`+c.device) {
				t.Errorf("request %s without confirmation: %d %s", c.id, got.Status, got.Body)
			}
			for _, call := range api.Calls() {
				if !strings.HasPrefix(call, "GET ") {
					t.Errorf("request %s: the API server was asked to change something: %s", c.id, call)
				}
			}
			var status string
			var folders int
			if err := db.QueryRow(ctx, "SELECT status, (SELECT COUNT(*) FROM device_folder_assignments) FROM pending_registrations WHERE id = ?", c.id).Scan(&status, &folders); err != nil || status != "pending" || folders != 0 {
				t.Errorf("request %s: status %q, %d folder assignments (%v)", c.id, status, folders, err)
			}
		}
		if got := admin.Do(http.MethodPut, "/api/registrations/1/approve", `{"folder_id":1,"confirm_existing_device":"yes"}`); got.Status != http.StatusConflict {
			t.Errorf("a confirmation that is not true: %d %s", got.Status, got.Body)
		}
		if got := admin.Do(http.MethodPut, "/api/registrations/1/approve", `{"folder_id":1,"confirm_existing_device":true}`); got.Status != http.StatusOK {
			t.Errorf("request 1 confirmed: %d %s", got.Status, got.Body)
		}
		if got := admin.Do(http.MethodPut, "/api/registrations/3/approve", `{"folder_id":1}`); got.Status != http.StatusOK {
			t.Errorf("a new device: %d %s", got.Status, got.Body)
		}
		var approved int
		for _, row := range w.AuditRows() {
			if strings.HasPrefix(row, "registration_approved|") {
				approved++
			}
		}
		if approved != 2 {
			t.Errorf("%d approvals audited, want 2", approved)
		}
	})
}

// A deleted device outside the approver's scope is refused like an
// active one, confirmed or not (BUGS L55).
func TestApprovalOfADeletedDeviceAsksForTheScope(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		api := routetest.NewAPI()
		api.Fallback = func(c routetest.Call) routetest.Reply {
			if path, query, _ := strings.Cut(c.Target, "?"); path == "/api/peers" && query == "include_deleted=true" {
				return routetest.Reply{Status: http.StatusOK, Body: `[{"id":"DEV-HID","soft_deleted":true}]`}
			}
			return routetest.Reply{Status: http.StatusNotFound, Body: `{"error":"not found"}`}
		}
		w := routetest.New(t, routetest.Options{DB: db, API: api, Roles: routetest.Overrides{"operator": {"enrollment.approve": true}}})
		seedCollisions(t, db)
		got := w.As("operator").Do(http.MethodPut, "/api/registrations/4/approve", `{"folder_id":1,"confirm_existing_device":true}`)
		if got.Status != http.StatusForbidden {
			t.Errorf("a deleted device outside the scope: %d %s", got.Status, got.Body)
		}
	})
}
