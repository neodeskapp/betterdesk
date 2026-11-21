package registration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
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

// A registration request for a new device counts against the limit of
// device registrations per address (BD_REGISTER_RATE_LIMIT_MAX), on the
// root mount and at /api/bd alike; a request for a device that has its
// registration counts not (BUGS RV8).
func TestRegisterRequestIsLimited(t *testing.T) {
	w := routetest.New(t, routetest.Options{Env: map[string]string{"BD_REGISTER_RATE_LIMIT_MAX": "2", "RATE_LIMIT_MAX": "1000"}})
	for i, c := range []struct {
		path string
		want int
	}{{"/api/bd/register-request", 200}, {"/register-request", 200}, {"/api/bd/register-request", 429}} {
		got := w.SignedOut().Do(http.MethodPost, c.path, `{"device_id":"limited-`+strconv.Itoa(i)+`"}`)
		if got.Status != c.want {
			t.Errorf("request %d at %s: %d %s, want %d", i+1, c.path, got.Status, got.Body, c.want)
		}
	}
	if got := w.SignedOut().Do(http.MethodPost, "/api/bd/register-request", `{"device_id":"limited-0"}`); got.Status != 200 {
		t.Errorf("a device that has its registration: %d %s", got.Status, got.Body)
	}
}

// New registrations are refused while 50 from the same address or 500 in
// all wait for a decision; a device that already has its registration
// still gets it, and decided ones do not count. The list holds at most
// 500 rows a page and counts all of them in total (BUGS RV8).
func TestPendingRegistrationsAreCapped(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := routetest.New(t, routetest.Options{DB: db, Env: map[string]string{"BD_REGISTER_RATE_LIMIT_MAX": "100000", "RATE_LIMIT_MAX": "100000"}})
		ctx := context.Background()
		insert := func(prefix, address, status string, n int) {
			t.Helper()
			var values []string
			var args []any
			for i := 0; i < n; i++ {
				values = append(values, "(?, ?, ?)")
				args = append(args, prefix+strconv.Itoa(i), address, status)
			}
			if _, err := db.Exec(ctx, "INSERT INTO pending_registrations (device_id, ip_address, status) VALUES "+strings.Join(values, ", "), args...); err != nil {
				t.Fatal(err)
			}
		}
		request := func(id string) (int, string) {
			got := w.SignedOut().Do(http.MethodPost, "/api/bd/register-request", `{"device_id":"`+id+`"}`)
			return got.Status, got.Body
		}
		insert("decided-", routetest.ClientIP, "rejected", 60)
		insert("local-", routetest.ClientIP, "pending", 49)
		if status, body := request("local-last"); status != 200 {
			t.Fatalf("the 50th from the address: %d %s", status, body)
		}
		if status, body := request("local-over"); status != 429 || body != `{"success":false,"error":"Too many pending registrations"}` {
			t.Errorf("the 51st from the address: %d %s", status, body)
		}
		if status, body := request("local-3"); status != 200 || !strings.Contains(body, `"status":"pending"`) {
			t.Errorf("a device that has its registration: %d %s", status, body)
		}

		insert("far-", "10.0.0.9", "pending", 450)
		var pending int
		if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM pending_registrations WHERE status = 'pending'").Scan(&pending); err != nil || pending != 500 {
			t.Fatalf("%d pending (%v)", pending, err)
		}
		if _, err := db.Exec(ctx, "UPDATE pending_registrations SET status = 'rejected' WHERE device_id = 'local-0'"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, "UPDATE pending_registrations SET status = 'pending', ip_address = '10.0.0.10' WHERE device_id = 'decided-0'"); err != nil {
			t.Fatal(err)
		}
		if status, body := request("local-new"); status != 429 {
			t.Errorf("a new device with 500 pending in all: %d %s", status, body)
		}

		admin := w.As("admin")
		var page struct {
			Data  []map[string]any `json:"data"`
			Total float64          `json:"total"`
		}
		got := admin.Do(http.MethodGet, "/api/registrations", "")
		if err := json.Unmarshal([]byte(got.Body), &page); err != nil || len(page.Data) != 500 || page.Total != 560 {
			t.Errorf("the list: %d rows of %v (%v)", len(page.Data), page.Total, err)
		}
		got = admin.Do(http.MethodGet, "/api/registrations?status=rejected&limit=2&offset=1", "")
		if err := json.Unmarshal([]byte(got.Body), &page); err != nil || len(page.Data) != 2 || page.Total != 60 {
			t.Errorf("a page of two: %d rows of %v (%v) %s", len(page.Data), page.Total, err, got.Body)
		}
	})
}

// An approval whose folder cannot be assigned is stored and audited, but
// answered as a failure that says what is missing, for a LAN registration
// and for the API server's enrollment queue alike (BUGS RV31).
func TestApprovalSaysWhenTheFolderFailed(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		api := routetest.NewAPI().On("POST /api/enrollment/approve/DEV-E", http.StatusOK, `{"success":true,"device_id":"DEV-E"}`)
		w := routetest.New(t, routetest.Options{DB: db, API: api})
		ctx := context.Background()
		for _, stmt := range []string{
			"INSERT INTO pending_registrations (id, device_id, status) VALUES (1, 'DEV-F', 'pending')",
			"INSERT INTO folders (id, name) VALUES (1, 'Lab')",
			"DROP TABLE device_folder_assignments",
		} {
			if _, err := db.Exec(ctx, stmt); err != nil {
				t.Fatalf("%s: %v", stmt, err)
			}
		}
		admin := w.As("admin")
		const want = `"success":false,"error":"Approved, but the folder could not be assigned"`
		got := admin.Do(http.MethodPut, "/api/registrations/1/approve", `{"folder_id":1}`)
		if got.Status != http.StatusInternalServerError || !strings.Contains(got.Body, want) || !strings.Contains(got.Body, `"status":"approved"`) {
			t.Errorf("LAN approval: %d %s", got.Status, got.Body)
		}
		got = admin.Do(http.MethodPost, "/api/enrollment/approve/DEV-E", `{"folder_id":1}`)
		if got.Status != http.StatusInternalServerError || !strings.Contains(got.Body, want) {
			t.Errorf("enrollment approval: %d %s", got.Status, got.Body)
		}
		var approved int
		for _, row := range w.AuditRows() {
			if strings.HasPrefix(row, "registration_approved|") || strings.HasPrefix(row, "enrollment_approved|") {
				approved++
			}
		}
		if approved != 2 {
			t.Errorf("%d approvals audited, want 2: %q", approved, w.AuditRows())
		}
	})
}
