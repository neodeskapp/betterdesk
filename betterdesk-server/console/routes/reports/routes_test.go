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
// everywhere; an account whose role lost audit.view is refused by the
// reports section of the panel access guard, which asks audit.view too
// (PA3, F6); roles that have it list the types.
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
		if got.Status != 403 || got.Body != `{"success":false,"error":"Panel access denied: reports","code":"panel_access_denied"}` {
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

// TestSaveAndDeleteAskTheReadsChecksFirst: saving and deleting ask for a
// session and audit.view as the reads do, and then for the role (BUGS.md
// RP1). Someone who is not signed in gets 401, and a global admin whose
// role lost audit.view saves nothing, also at an address in other letters.
func TestSaveAndDeleteAskTheReadsChecksFirst(t *testing.T) {
	w := routetest.New(t, routetest.Options{Roles: replay.RoleOverrides{"global_admin": {"audit.view": false}}})
	signedOut := w.SignedOut()
	global := w.As("global_admin")
	for _, r := range []struct{ method, path, body string }{
		{"POST", "/api/reports/save", `{"type":"devices"}`},
		{"POST", "/API/Reports/Save", `{"type":"devices"}`},
		{"DELETE", "/api/reports/saved/1", ""},
		{"DELETE", "/Api/Reports/Saved/1", ""},
	} {
		if got := signedOut.Do(r.method, r.path, r.body); got.Status != 401 || got.Body != `{"error":"Authentication required"}` {
			t.Errorf("%s %s signed out: %d %s", r.method, r.path, got.Status, got.Body)
		}
		if got := global.Do(r.method, r.path, r.body); got.Status != 403 {
			t.Errorf("%s %s as a global admin without audit.view: %d %s", r.method, r.path, got.Status, got.Body)
		}
	}
	if rows := savedCount(t, w); rows != 0 {
		t.Fatalf("%d reports saved by the refused requests", rows)
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

// TestReportWritesFollowTheRouteRoles: the reports section asks audit.view
// for writing too (PA3, F6, the guard half of RP1), so a panel access row
// at write lets global_admin and server_admin past the guard, and the
// route's own role check decides: global_admin saves and deletes,
// server_admin is refused.
func TestReportWritesFollowTheRouteRoles(t *testing.T) {
	w := routetest.New(t, routetest.Options{})
	for _, id := range []string{"3", "4"} {
		if _, err := w.DB.Exec(context.Background(), `INSERT INTO panel_access_policies (subject_type, subject_id, resource_key, access_level, updated_at)
			VALUES ('user', ?, 'reports', 'write', '2019-01-01 00:00:00')`, id); err != nil {
			t.Fatal(err)
		}
	}
	got := w.As("admin").Do("POST", "/api/reports/save", `{"type":"alerts","title":"By the admin"}`)
	if got.Status != 201 || !strings.Contains(got.Body, `"created_by":"e2e_admin"`) {
		t.Fatalf("save as admin: %d %s", got.Status, got.Body)
	}
	var saved struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal([]byte(got.Body), &saved); err != nil {
		t.Fatal(err)
	}
	path := "/api/reports/saved/" + jsonNumber(saved.ID)
	server := w.As("server_admin")
	if got := server.Do("POST", "/api/reports/save", `{"type":"alerts"}`); got.Status != 403 || got.Body != `{"error":"Admin access required"}` {
		t.Errorf("save as server_admin: %d %s", got.Status, got.Body)
	}
	if got := server.Do("DELETE", path, ""); got.Status != 403 || got.Body != `{"error":"Admin access required"}` {
		t.Errorf("delete as server_admin: %d %s", got.Status, got.Body)
	}
	global := w.As("global_admin")
	if got := global.Do("POST", "/api/reports/save", `{"type":"alerts"}`); got.Status != 201 || !strings.Contains(got.Body, `"created_by":"e2e_global_admin"`) {
		t.Errorf("save as global_admin: %d %s", got.Status, got.Body)
	}
	if got := global.Do("DELETE", path, ""); got.Status != 200 || got.Body != `{"ok":true}` {
		t.Errorf("delete as global_admin: %d %s", got.Status, got.Body)
	}
}

// TestTypeMustBeTheNameOfAReport: generating, exporting and saving take
// the type only as a text that names a report (BUGS.md RP2). A name every
// JavaScript object has, a list holding a name, and any other type are
// 400 with the text of the type, and nothing is saved.
func TestTypeMustBeTheNameOfAReport(t *testing.T) {
	w := routetest.New(t, routetest.Options{})
	admin := w.As("admin")
	for _, c := range []struct{ typ, answer string }{
		{`"constructor"`, `{"error":"Unknown report type: constructor"}`},
		{`"__proto__"`, `{"error":"Unknown report type: __proto__"}`},
		{`"toString"`, `{"error":"Unknown report type: toString"}`},
		{`["devices"]`, `{"error":"Unknown report type: devices"}`},
		{`[["tickets"]]`, `{"error":"Unknown report type: tickets"}`},
		{`["devices","x"]`, `{"error":"Unknown report type: devices,x"}`},
		{`"nope"`, `{"error":"Unknown report type: nope"}`},
		{`5`, `{"error":"Unknown report type: 5"}`},
		{`{"a":1}`, `{"error":"Unknown report type: [object Object]"}`},
	} {
		body := `{"type":` + c.typ + `,"title":"Odd type"}`
		for _, path := range []string{"/api/reports/generate", "/api/reports/generate/csv", "/api/reports/save"} {
			if got := admin.Do("POST", path, body); got.Status != 400 || got.Body != c.answer {
				t.Errorf("%s %s: %d %s", path, body, got.Status, got.Body)
			}
		}
	}
	if rows := savedCount(t, w); rows != 0 {
		t.Errorf("%d reports saved with an odd type", rows)
	}
	if got := admin.Do("POST", "/api/reports/save", `{"type":"inventory"}`); got.Status != 201 || !strings.Contains(got.Body, `"report_type":"inventory"`) {
		t.Errorf("save the inventory report: %d %s", got.Status, got.Body)
	}
}

// TestActivityRangeMustBeTimes: from and to are read as times (BUGS.md
// RP5). A date alone as to covers that whole day, an offset counts, and
// anything that is no date or time is 400 in every route. The filters of
// a report are from and to; device_id and limit, which no report reads,
// are left out.
func TestActivityRangeMustBeTimes(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := routetest.New(t, routetest.Options{DB: db})
		if _, err := db.Exec(context.Background(), `INSERT INTO activity_summaries (device_id, total_active_secs, idle_seconds, reported_at, received_at)
			VALUES ('A', 100, 0, '2019-11-30T10:00:00.000Z', '2019-11-30 10:00:00'), ('B', 50, 0, '2019-12-01T10:00:00.000Z', '2019-12-01 10:00:00')`); err != nil {
			t.Fatal(err)
		}
		admin := w.As("admin")
		for body, want := range map[string]string{
			`{"type":"activity","from":"2019-11-30","to":"2019-11-30"}`:                                        `"daily_reports":1,"total_active_secs":100,`,
			`{"type":"activity","to":"2019-11-30T23:59:59.999Z"}`:                                              `"daily_reports":1,"total_active_secs":100,`,
			`{"type":"activity","from":"2019-11-30T11:00:00+01:00"}`:                                           `"daily_reports":2,"total_active_secs":150,`,
			`{"type":"activity","from":"2019-11-30T11:00:01+01:00"}`:                                           `"daily_reports":1,"total_active_secs":50,`,
			`{"type":"activity","from":"2019-12-01 10:00:00","to":"2019-12-01"}`:                               `"daily_reports":1,"total_active_secs":50,`,
			`{"type":"activity","from":0,"to":""}`:                                                             `"daily_reports":2,"total_active_secs":150,`,
			`{"type":"tickets","from":"2019-11-01","to":"2019-11-30T23:59:59.999Z","device_id":"x","limit":5}`: `"filters":{"from":"2019-11-01","to":"2019-11-30T23:59:59.999Z"},`,
			`{"type":"devices","device_id":{"a":1},"limit":[1]}`:                                               `"filters":{},`,
		} {
			if got := admin.Do("POST", "/api/reports/generate", body); got.Status != 200 || !strings.Contains(got.Body, want) {
				t.Errorf("%s: %d %s\nwant %s", body, got.Status, got.Body, want)
			}
		}
		for _, bad := range []string{`"garbage"`, `"2019-11"`, `"2019"`, `"2019-02-30"`, `5`, `true`, `["2019-11-01"]`, `{"a":1}`} {
			for _, name := range []string{"from", "to"} {
				body := `{"type":"activity","` + name + `":` + bad + `}`
				answer := `{"error":"` + name + ` must be a date or a time"}`
				for _, path := range []string{"/api/reports/generate", "/api/reports/generate/csv", "/api/reports/save"} {
					if got := admin.Do("POST", path, body); got.Status != 400 || got.Body != answer {
						t.Errorf("%s %s: %d %s", path, body, got.Status, got.Body)
					}
				}
			}
		}
		if rows := savedCount(t, w); rows != 0 {
			t.Errorf("%d reports saved with a range that is no time", rows)
		}
	})
}

// TestSavedReportWithAPayloadThatIsNoJSON: a saved report whose payload
// is text and no JSON is read with the payload {}, where it answered 500
// (BUGS.md RP8).
func TestSavedReportWithAPayloadThatIsNoJSON(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := routetest.New(t, routetest.Options{DB: db})
		if _, err := db.Exec(context.Background(), `INSERT INTO saved_reports (id, title, report_type, filters, payload, created_by)
			VALUES (7, 'Broken', 'devices', '{}', `+db.Pick(`'not json'`, `'"not json"'`)+`, 'admin')`); err != nil {
			t.Fatal(err)
		}
		got := w.As("viewer").Do("GET", "/api/reports/saved/7", "")
		if got.Status != 200 || !strings.Contains(got.Body, `"title":"Broken","report_type":"devices","filters":`) || !strings.Contains(got.Body, `"payload":{},`) {
			t.Errorf("saved report with a payload that is no JSON: %d %s", got.Status, got.Body)
		}
	})
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

// savedCount is the number of saved reports.
func savedCount(t *testing.T, w *routetest.World) int {
	t.Helper()
	var n int
	if err := w.DB.QueryRow(context.Background(), "SELECT COUNT(*) FROM saved_reports").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestTheExportIsNamedAfterTheDay: the file name of a CSV export holds
// the type and the day in UTC.
func TestTheExportIsNamedAfterTheDay(t *testing.T) {
	w := routetest.New(t, routetest.Options{})
	before := time.Now().UTC().Format("2006-01-02")
	got := w.As("admin").Do("POST", "/api/reports/generate/csv", `{"type":"inventory"}`)
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
