package reports_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/routes/internal/replay"
	"github.com/unitronix/betterdesk-server/console/routes/internal/routetest"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// reads are the routes that need a session and audit.view.
var reads = []struct{ method, path, body string }{
	{"GET", "/api/reports/types", ""},
	{"POST", "/api/reports/generate", `{"type":"devices"}`},
	{"POST", "/api/reports/generate/csv", `{"type":"devices"}`},
	{"GET", "/api/reports/saved", ""},
	{"GET", "/api/reports/saved/1", ""},
}

// TestReadsNeedAuditView: reading and generating reports needs audit.view,
// as the Reports page does. A guest link issuer and a pro account get 403
// everywhere; an account whose role lost audit.view gets the route's own
// 403; roles that have it list the types.
func TestReadsNeedAuditView(t *testing.T) {
	w := routetest.New(t, routetest.Options{Roles: replay.RoleOverrides{"operator": {"audit.view": false}, "admin": {"audit.view": false}}})
	for _, role := range []string{"guest_issuer", "pro"} {
		agent := w.As(role)
		for _, r := range reads {
			if got := agent.Do(r.method, r.path, r.body); got.Status != 403 {
				t.Errorf("%s %s as %s: %d %s", r.method, r.path, role, got.Status, got.Body)
			}
		}
	}
	operator := w.As("operator")
	for _, r := range reads {
		if r.method != "GET" {
			continue
		}
		got := operator.Do(r.method, r.path, r.body)
		if got.Status != 403 || got.Body != `{"success":false,"error":"Permission denied: audit.view"}` {
			t.Errorf("%s %s as operator without audit.view: %d %s", r.method, r.path, got.Status, got.Body)
		}
	}
	for _, role := range []string{"viewer", "server_admin", "global_admin"} {
		if got := w.As(role).Do("GET", "/api/reports/types", ""); got.Status != 200 || !strings.HasPrefix(got.Body, `[{"type":"devices","label":"Device Summary"}`) {
			t.Errorf("types as %s: %d %s", role, got.Status, got.Body)
		}
	}
	// A super admin role has every permission, whatever its overrides say
	admin := w.As("admin")
	if got := admin.Do("GET", "/api/reports/types", ""); got.Status != 200 {
		t.Errorf("types as admin without audit.view: %d %s", got.Status, got.Body)
	}
	if got := admin.Do("POST", "/api/reports/generate", `{"type":"tickets"}`); got.Status != 200 || !strings.Contains(got.Body, `"label":"Ticket Report"`) {
		t.Errorf("generate as admin: %d %s", got.Status, got.Body)
	}
	signedOut := w.SignedOut()
	for _, r := range reads {
		if got := signedOut.Do(r.method, r.path, r.body); got.Status != 401 || got.Body != `{"error":"Authentication required"}` {
			t.Errorf("%s %s signed out: %d %s", r.method, r.path, got.Status, got.Body)
		}
	}
}

// TestSaveAndDeleteCheckTheRoleOnly: saving and deleting look at the role
// in the session and nothing else, so someone who is not signed in gets
// 403 "Admin access required", not 401 (BUGS.md RP1).
func TestSaveAndDeleteCheckTheRoleOnly(t *testing.T) {
	w := routetest.New(t, routetest.Options{})
	signedOut := w.SignedOut()
	for _, r := range []struct{ method, path, body string }{
		{"POST", "/api/reports/save", `{"type":"devices"}`},
		{"DELETE", "/api/reports/saved/1", ""},
	} {
		if got := signedOut.Do(r.method, r.path, r.body); got.Status != 403 || got.Body != `{"error":"Admin access required"}` {
			t.Errorf("%s %s signed out: %d %s", r.method, r.path, got.Status, got.Body)
		}
	}
	admin := w.As("super_admin")
	got := admin.Do("POST", "/api/reports/save", `{"type":"tickets","title":"Mine"}`)
	if got.Status != 201 {
		t.Fatalf("save: %d %s", got.Status, got.Body)
	}
	var saved struct {
		ID        int    `json:"id"`
		Title     string `json:"title"`
		CreatedBy string `json:"created_by"`
		Payload   string `json:"payload"`
	}
	if err := json.Unmarshal([]byte(got.Body), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Title != "Mine" || saved.CreatedBy != "e2e_super_admin" || !strings.HasPrefix(saved.Payload, `{"stats":{"total":0,`) {
		t.Errorf("saved: %s", got.Body)
	}
	path := "/api/reports/saved/" + jsonNumber(saved.ID)
	if got := w.As("viewer").Do("DELETE", path, ""); got.Status != 403 {
		t.Errorf("delete as viewer: %d %s", got.Status, got.Body)
	}
	if got := admin.Do("DELETE", path, ""); got.Status != 200 || got.Body != `{"ok":true}` {
		t.Errorf("delete: %d %s", got.Status, got.Body)
	}
	if got := admin.Do("DELETE", path, ""); got.Status != 404 || got.Body != `{"error":"Report not found"}` {
		t.Errorf("delete again: %d %s", got.Status, got.Body)
	}
}

// TestTheRoleDecidesForSaving: with a panel access policy that lets them
// write in the reports section, global_admin may save and delete reports
// and server_admin may not, whatever its permissions.
func TestTheRoleDecidesForSaving(t *testing.T) {
	w := routetest.New(t, routetest.Options{})
	for _, id := range []string{"3", "4"} {
		if _, err := w.DB.Exec(context.Background(), `INSERT INTO panel_access_policies (subject_type, subject_id, resource_key, access_level, updated_at)
			VALUES ('user', ?, 'reports', 'write', '2019-01-01 00:00:00')`, id); err != nil {
			t.Fatal(err)
		}
	}
	got := w.As("global_admin").Do("POST", "/api/reports/save", `{"type":"alerts","title":"By the global admin"}`)
	if got.Status != 201 || !strings.Contains(got.Body, `"created_by":"e2e_global_admin"`) {
		t.Fatalf("save as global_admin: %d %s", got.Status, got.Body)
	}
	var saved struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal([]byte(got.Body), &saved); err != nil {
		t.Fatal(err)
	}
	path := "/api/reports/saved/" + jsonNumber(saved.ID)
	if got := w.As("server_admin").Do("POST", "/api/reports/save", `{"type":"alerts"}`); got.Status != 403 || got.Body != `{"error":"Admin access required"}` {
		t.Errorf("save as server_admin: %d %s", got.Status, got.Body)
	}
	if got := w.As("server_admin").Do("DELETE", path, ""); got.Status != 403 || got.Body != `{"error":"Admin access required"}` {
		t.Errorf("delete as server_admin: %d %s", got.Status, got.Body)
	}
	if got := w.As("global_admin").Do("DELETE", path, ""); got.Status != 200 || got.Body != `{"ok":true}` {
		t.Errorf("delete as global_admin: %d %s", got.Status, got.Body)
	}
}

// TestDatabaseFailures: a failing query answers each route's own 500.
func TestDatabaseFailures(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := routetest.New(t, routetest.Options{DB: db})
		admin := w.As("admin")
		if _, err := db.Exec(context.Background(), "DROP TABLE saved_reports"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(context.Background(), "DROP TABLE alert_history"); err != nil {
			t.Fatal(err)
		}
		for _, c := range []struct {
			method, path, body string
			status             int
			answer             string
		}{
			{"GET", "/api/reports/saved", "", 500, `{"error":"Failed to fetch saved reports"}`},
			{"GET", "/api/reports/saved/1", "", 500, `{"error":"Failed to fetch report"}`},
			{"POST", "/api/reports/save", `{"type":"tickets"}`, 500, `{"error":"Failed to save report"}`},
			{"DELETE", "/api/reports/saved/1", "", 500, `{"error":"Failed to delete report"}`},
			// the alert report takes a failing history as no alerts
			{"POST", "/api/reports/generate", `{"type":"alerts"}`, 200, ""},
		} {
			got := admin.Do(c.method, c.path, c.body)
			if got.Status != c.status || (c.answer != "" && got.Body != c.answer) {
				t.Errorf("%s %s: %d %s", c.method, c.path, got.Status, got.Body)
			}
			if c.answer == "" && !strings.Contains(got.Body, `"total_alerts":0,"alerts_per_rule":{},"recent_alerts":[]`) {
				t.Errorf("%s %s: %s", c.method, c.path, got.Body)
			}
		}
	})
}

func jsonNumber(n int) string {
	out, _ := json.Marshal(n)
	return string(out)
}

// TestTheExportIsNamedAfterTheDay: the file name of a CSV export holds
// the type as the request gave it and the day in UTC.
func TestTheExportIsNamedAfterTheDay(t *testing.T) {
	w := routetest.New(t, routetest.Options{})
	before := time.Now().UTC().Format("2006-01-02")
	got := w.As("admin").Do("POST", "/api/reports/generate/csv", `{"type":["inventory"]}`)
	after := time.Now().UTC().Format("2006-01-02")
	disposition := got.Header.Get("Content-Disposition")
	if got.Status != 200 || got.Header.Get("Content-Type") != "text/csv; charset=utf-8" || got.Body != "total_inventoried,avg_ram_mb\n0,0" {
		t.Fatalf("export: %d %q %q", got.Status, got.Header.Get("Content-Type"), got.Body)
	}
	if disposition != `attachment; filename="betterdesk_inventory_`+before+`.csv"` && disposition != `attachment; filename="betterdesk_inventory_`+after+`.csv"` {
		t.Errorf("Content-Disposition: %q", disposition)
	}

	// A large export goes out plain with an ETag, also to a client that
	// takes gzip: only JSON answers are compressed.
	for i := 0; i < 30; i++ {
		if _, err := w.DB.Exec(context.Background(), "INSERT INTO audit_log (user_id, action, details, created_at) VALUES (1, 'login', ?, '2019-11-01 10:00:00')",
			strings.Repeat("details of an entry, ", 5)); err != nil {
			t.Fatal(err)
		}
	}
	got = w.As("admin").Do("POST", "/api/reports/generate/csv", `{"type":"security","section":"recent_audit"}`, "Accept-Encoding", "gzip")
	if got.Status != 200 || len(got.Body) < 1024 || got.Header.Get("Content-Encoding") != "" || got.Header.Get("Vary") != "" || got.Header.Get("ETag") == "" {
		t.Errorf("a large export: %d, %d bytes, Content-Encoding %q, Vary %q, ETag %q",
			got.Status, len(got.Body), got.Header.Get("Content-Encoding"), got.Header.Get("Vary"), got.Header.Get("ETag"))
	}
}
