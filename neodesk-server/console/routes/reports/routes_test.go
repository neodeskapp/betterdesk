package reports_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/replay"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
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
// anything that is no date or time is 400 in every route. A from and a to
// that are not true set no bound, so the report covers the year before
// now (RV96). The filters of a report are from and to; device_id and
// limit, which no report reads, are left out.
func TestActivityRangeMustBeTimes(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := routetest.New(t, routetest.Options{DB: db})
		recent := time.Now().UTC().Add(-24 * time.Hour)
		if _, err := db.Exec(context.Background(), `INSERT INTO activity_summaries (device_id, total_active_secs, idle_seconds, reported_at, received_at)
			VALUES ('A', 100, 0, '2019-11-30T10:00:00.000Z', '2019-11-30 10:00:00'), ('B', 50, 0, '2019-12-01T10:00:00.000Z', '2019-12-01 10:00:00'),
			('C', 7, 0, ?, ?)`, recent.Format("2006-01-02T15:04:05.000Z"), recent.Format(time.DateTime)); err != nil {
			t.Fatal(err)
		}
		admin := w.As("admin")
		for body, want := range map[string]string{
			`{"type":"activity","from":"2019-11-30","to":"2019-11-30"}`:                                        `"daily_reports":1,"total_active_secs":100,`,
			`{"type":"activity","to":"2019-11-30T23:59:59.999Z"}`:                                              `"daily_reports":1,"total_active_secs":100,`,
			`{"type":"activity","from":"2019-11-30T11:00:00+01:00","to":"2019-12-31"}`:                         `"daily_reports":2,"total_active_secs":150,`,
			`{"type":"activity","from":"2019-11-30T11:00:01+01:00","to":"2019-12-31"}`:                         `"daily_reports":1,"total_active_secs":50,`,
			`{"type":"activity","from":"2019-12-01 10:00:00","to":"2019-12-01"}`:                               `"daily_reports":1,"total_active_secs":50,`,
			`{"type":"activity","from":0,"to":""}`:                                                             `"daily_reports":1,"total_active_secs":7,`,
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

// TestSavedReportIDsThatNameNoRow: the id of the address is read as a
// JavaScript number, on both databases. A value that is no whole number,
// or one beyond what an id column holds, names no saved report: 404 for a
// read and a delete, where PostgreSQL failed with 500 (BUGS C7).
func TestSavedReportIDsThatNameNoRow(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := routetest.New(t, routetest.Options{DB: db})
		insert := `INSERT INTO saved_reports (id, title, report_type, filters, payload, created_by) VALUES (2, 'Kept', 'devices', '{}', '{}', 'admin')`
		if _, err := db.Exec(context.Background(), insert); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"1.5", "abc", "Infinity", "-Infinity", "NaN", "1e400", "2147483648", "99999999999", "-2147483649", "%C3%BC", "-0", "2x"} {
			if got := w.As("viewer").Do("GET", "/api/reports/saved/"+id, ""); got.Status != 404 || got.Body != `{"error":"Saved report not found"}` {
				t.Errorf("GET %s: %d %s", id, got.Status, got.Body)
			}
			if got := w.As("admin").Do("DELETE", "/api/reports/saved/"+id, ""); got.Status != 404 || got.Body != `{"error":"Report not found"}` {
				t.Errorf("DELETE %s: %d %s", id, got.Status, got.Body)
			}
		}
		for _, id := range []string{"2", "02", "2.0", "0x2", "%20%202%20", "2e0", "0b10"} {
			if got := w.As("viewer").Do("GET", "/api/reports/saved/"+id, ""); got.Status != 200 || !strings.Contains(got.Body, `"title":"Kept"`) {
				t.Errorf("GET %s: %d %s", id, got.Status, got.Body)
			}
		}
		if got := w.As("admin").Do("DELETE", "/api/reports/saved/2.0", ""); got.Status != 200 || got.Body != `{"ok":true}` {
			t.Errorf("DELETE 2.0: %d %s", got.Status, got.Body)
		}
	})
}

// TestSaveRefusesATitleThatIsNoText: a saved report's title is a text; a
// number, a flag, a list or an object is 400 and nothing is stored, and a
// title that is missing, null or empty gets the default title (BUGS.md
// RP10).
func TestSaveRefusesATitleThatIsNoText(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := routetest.New(t, routetest.Options{DB: db})
		admin := w.As("admin")
		for _, title := range []string{`5`, `0`, `true`, `false`, `["In a list"]`, `[]`, `{"a":1}`} {
			body := `{"type":"network","title":` + title + `}`
			if got := admin.Do("POST", "/api/reports/save", body); got.Status != 400 || got.Body != `{"error":"Title must be text"}` {
				t.Errorf("%s: %d %s", body, got.Status, got.Body)
			}
		}
		if rows := savedCount(t, w); rows != 0 {
			t.Fatalf("%d reports saved with a title that is no text", rows)
		}
		for body, want := range map[string]string{
			`{"type":"network","title":"Mine"}`: `"title":"Mine"`,
			`{"type":"network","title":""}`:     `"title":"Network Report — `,
			`{"type":"network","title":null}`:   `"title":"Network Report — `,
			`{"type":"network"}`:                `"title":"Network Report — `,
		} {
			if got := admin.Do("POST", "/api/reports/save", body); got.Status != 201 || !strings.Contains(got.Body, want) {
				t.Errorf("%s: %d %s", body, got.Status, got.Body)
			}
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

// TestReportsKeepToTheDeviceScope closes devices a and c to the viewer
// with a device group whose access list names the operator (BUGS.md
// RV90). The viewer's reports count device b alone: its peer row, data
// guard events, activity, inventory and alerts, and the alerts that name
// no device; the admin's count every device. A saved report is read by
// the account that saved it, or by an account without a scope limit.
func TestReportsKeepToTheDeviceScope(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		api := routetest.NewAPI()
		api.On("GET /api/peers", 200, `[{"id":"a"},{"id":"b"},{"id":"c"}]`)
		w := routetest.New(t, routetest.Options{DB: db, API: api})
		ctx := context.Background()
		day := time.Now().UTC().Add(-48 * time.Hour).Format(time.DateOnly)
		for _, statement := range []string{
			"INSERT INTO device_groups (id, guid, name) VALUES (90, 'closed', 'Closed')",
			"INSERT INTO device_group_members (device_group_id, peer_id) VALUES (90, 'a'), (90, 'c')",
			"INSERT INTO device_group_user_access (device_group_id, user_id) VALUES (90, 5)",
			`INSERT INTO peer (id, note, info, status_online, is_banned, banned_reason, is_deleted) VALUES
				('a', 'Alpha box', '{"os":"Windows"}', TRUE, TRUE, 'Stolen', FALSE),
				('b', 'Beta box', '{"os":"Linux"}', TRUE, FALSE, '', FALSE),
				('c', 'Gamma box', '{"os":"macOS"}', FALSE, TRUE, 'Lost', FALSE)`,
			`INSERT INTO dlp_events (device_id, event_source, action, details, created_at) VALUES
				('a', 'usb', 'block', '{"file":"alpha.txt"}', '2019-11-01 10:00:00'),
				('b', 'file', 'log', '{"file":"beta.txt"}', '2019-11-01 11:00:00'),
				('c', 'usb', 'block', '{"file":"gamma.txt"}', '2019-11-01 12:00:00')`,
			// the activity of two days ago, within the year a report covers
			strings.ReplaceAll(`INSERT INTO activity_summaries (device_id, total_active_secs, idle_seconds, reported_at, received_at) VALUES
				('a', 100, 1, 'DAYT00:00:00.000Z', 'DAY 00:00:00'),
				('b', 200, 2, 'DAYT00:00:00.000Z', 'DAY 00:00:00'),
				('c', 400, 4, 'DAYT00:00:00.000Z', 'DAY 00:00:00')`, "DAY", day),
			strings.ReplaceAll(`INSERT INTO activity_sessions (device_id, app_name, category, started_at, ended_at, duration_secs) VALUES
				('a', 'AlphaApp', 'x', 'DAYT01:00:00.000Z', 'DAYT02:00:00.000Z', 30),
				('b', 'BetaApp', 'y', 'DAYT01:00:00.000Z', 'DAYT02:00:00.000Z', 20),
				('c', 'GammaApp', 'z', 'DAYT01:00:00.000Z', 'DAYT02:00:00.000Z', 10)`, "DAY", day),
			`INSERT INTO device_inventory (device_id, hardware, software) VALUES
				('a', '{"os_name":"Windows 11"}', '{}'), ('b', '{"os_name":"Ubuntu"}', '{}'), ('c', '{"os_name":"macOS 15"}', '{}')`,
			"INSERT INTO alert_rules (id, name, condition_type) VALUES (1, 'Offline', 'offline')",
			`INSERT INTO alert_history (rule_id, device_id, message, triggered_at) VALUES
				(1, 'a', 'alpha offline', '2019-11-01 10:00:00'), (1, 'b', 'beta offline', '2019-11-01 11:00:00'),
				(1, 'c', 'gamma offline', '2019-11-01 12:00:00'), (1, NULL, 'server check', '2019-11-01 13:00:00')`,
			`INSERT INTO saved_reports (id, title, report_type, payload, created_by) VALUES
				(1, 'Admin''s', 'devices', '{"note":"Alpha box"}', 'e2e_admin'), (2, 'Viewer''s', 'devices', '{}', 'e2e_viewer')`,
		} {
			if _, err := db.Exec(ctx, statement); err != nil {
				t.Fatalf("%s: %v", statement, err)
			}
		}
		check := func(role, body string, has, lacks []string) {
			t.Helper()
			path := "/api/reports/generate"
			if strings.Contains(body, `"section"`) {
				path += "/csv"
			}
			got := w.As(role).Do("POST", path, body)
			if got.Status != 200 {
				t.Fatalf("%s %s: %d %s", role, body, got.Status, got.Body)
			}
			for _, part := range has {
				if !strings.Contains(got.Body, part) {
					t.Errorf("%s %s lacks %s: %s", role, body, part, got.Body)
				}
			}
			for _, part := range lacks {
				if strings.Contains(got.Body, part) {
					t.Errorf("%s %s holds %s: %s", role, body, part, got.Body)
				}
			}
		}
		closed := []string{"Alpha", "Gamma", "alpha", "gamma", `"a"`, `"c"`}
		check("viewer", `{"type":"devices"}`, []string{
			`"total_devices":1,"online":1,"offline":0,"banned":0,"platform_distribution":{"Linux":1},"stats":{"total":1,"online":1,"banned":0,"offline":0}`,
		}, nil)
		check("viewer", `{"type":"security"}`, []string{`"stats":{"total":1,`, `"banned_devices":[]`, `beta.txt`}, closed)
		check("viewer", `{"type":"activity","section":"top_applications"}`, []string{"BetaApp"}, closed)
		check("viewer", `{"type":"activity"}`, []string{
			`"devices_with_activity":1,"daily_reports":1,"total_active_secs":200,"total_idle_secs":2,"top_applications":[{"name":"BetaApp"`,
		}, closed)
		check("viewer", `{"type":"inventory"}`, []string{`"total_inventoried":1,"os_distribution":{"Ubuntu":1}`}, nil)
		check("viewer", `{"type":"alerts"}`, []string{`"total_alerts":2,`, "beta offline", "server check"}, closed)
		check("admin", `{"type":"devices"}`, []string{`"total_devices":3,"online":2,"offline":1,"banned":2,`, `"stats":{"total":3,`}, nil)
		check("admin", `{"type":"security"}`, []string{`"stats":{"total":3,`, "Alpha box", "Gamma box", "alpha.txt"}, nil)
		check("admin", `{"type":"activity"}`, []string{`"devices_with_activity":3,`, "AlphaApp", "GammaApp"}, nil)
		check("admin", `{"type":"inventory"}`, []string{`"total_inventoried":3,`}, nil)
		check("admin", `{"type":"alerts"}`, []string{`"total_alerts":4,`, "alpha offline"}, nil)

		// The operator is limited too, although its scope holds every
		// device: like the viewer it reads the reports it saved alone
		for _, role := range []string{"viewer", "operator"} {
			got := w.As(role).Do("GET", "/api/reports/saved", "")
			want := `"title":"Viewer's"`
			if role == "operator" {
				want = `[]`
			}
			if got.Status != 200 || strings.Contains(got.Body, "Admin's") || !strings.Contains(got.Body, want) {
				t.Errorf("saved as %s: %d %s", role, got.Status, got.Body)
			}
			if got := w.As(role).Do("GET", "/api/reports/saved/1", ""); got.Status != 404 || got.Body != `{"error":"Saved report not found"}` {
				t.Errorf("the admin's saved report as %s: %d %s", role, got.Status, got.Body)
			}
		}
		if got := w.As("viewer").Do("GET", "/api/reports/saved/2", ""); got.Status != 200 || !strings.Contains(got.Body, `"title":"Viewer's"`) {
			t.Errorf("its own saved report as viewer: %d %s", got.Status, got.Body)
		}
		if got := w.As("admin").Do("GET", "/api/reports/saved", ""); got.Status != 200 || !strings.Contains(got.Body, "Admin's") || !strings.Contains(got.Body, "Viewer's") {
			t.Errorf("saved as admin: %d %s", got.Status, got.Body)
		}
		if got := w.As("admin").Do("GET", "/api/reports/saved/2", ""); got.Status != 200 {
			t.Errorf("the viewer's saved report as admin: %d %s", got.Status, got.Body)
		}
	})
}

// TestReportsKeepTicketsAndAuditToTheDeviceScope closes the devices
// 100100001 and 100100003 to the viewer with a device group whose access
// list names the operator (L48). The viewer's ticket report counts the
// tickets of 100100002 and the one of no device; its security report
// leaves out the audit entries that name a device outside its scope, the
// deleted 100100004 included, and keeps those about the open device and
// about no device. The admin's reports count everything.
func TestReportsKeepTicketsAndAuditToTheDeviceScope(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		api := routetest.NewAPI()
		api.On("GET /api/peers", 200, `[{"id":"100100001"},{"id":"100100002"},{"id":"100100003"}]`)
		api.On("GET /api/peers?include_deleted=true", 200, `[{"id":"100100001"},{"id":"100100002"},{"id":"100100003"},{"id":"100100004","soft_deleted":true}]`)
		w := routetest.New(t, routetest.Options{DB: db, API: api})
		ctx := context.Background()
		for _, statement := range []string{
			"INSERT INTO device_groups (id, guid, name) VALUES (90, 'closed', 'Closed')",
			"INSERT INTO device_group_members (device_group_id, peer_id) VALUES (90, '100100001'), (90, '100100003')",
			"INSERT INTO device_group_user_access (device_group_id, user_id) VALUES (90, 5)",
			`INSERT INTO tickets (title, status, priority, category, device_id, created_by, created_at) VALUES
				('Alpha broke', 'open', 'high', 'alpha-cat', '100100001', 'e2e_admin', '2019-11-01 10:00:00'),
				('Beta broke', 'in_progress', 'low', 'beta-cat', '100100002', 'e2e_admin', '2019-11-01 11:00:00'),
				('Printer', 'closed', 'normal', 'general', NULL, 'e2e_admin', '2019-11-01 12:00:00'),
				('Gamma broke', 'resolved', 'urgent', 'gamma-cat', '100100003', 'e2e_admin', '2019-11-01 13:00:00')`,
			`INSERT INTO audit_log (user_id, action, details, ip_address, created_at) VALUES
				(1, 'device_banned', 'Device 100100001 banned', '127.0.0.1', '2019-11-01 10:00:00'),
				(1, 'note_added', 'Note added to device 100100002', '127.0.0.1', '2019-11-01 11:00:00'),
				(1, 'user_created', 'Created user: bob (operator)', '127.0.0.1', '2019-11-01 12:00:00'),
				(1, 'device_id_changed', 'Device ID changed from 100100002 to 100100003', '127.0.0.1', '2019-11-01 13:00:00'),
				(1, 'device_restored', 'Device 100100004 restored', '127.0.0.1', '2019-11-01 14:00:00'),
				(1, 'device_group_membership_updated', 'Device 100100002 groups set to [Lab]', '127.0.0.1', '2019-11-01 15:00:00')`,
		} {
			if _, err := db.Exec(ctx, statement); err != nil {
				t.Fatalf("%s: %v", statement, err)
			}
		}
		check := func(role, body string, has, lacks []string) {
			t.Helper()
			got := w.As(role).Do("POST", "/api/reports/generate", body)
			if got.Status != 200 {
				t.Fatalf("%s %s: %d %s", role, body, got.Status, got.Body)
			}
			for _, part := range has {
				if !strings.Contains(got.Body, part) {
					t.Errorf("%s %s lacks %s: %s", role, body, part, got.Body)
				}
			}
			for _, part := range lacks {
				if strings.Contains(got.Body, part) {
					t.Errorf("%s %s holds %s: %s", role, body, part, got.Body)
				}
			}
		}
		check("viewer", `{"type":"tickets"}`, []string{
			`"stats":{"total":2,"open":0,"in_progress":1,"resolved":0,"closed":1}`,
			`"total_tickets":2,"category_distribution":{"general":1,"beta-cat":1},"priority_distribution":{"normal":1,"low":1}`,
		}, []string{"alpha-cat", "gamma-cat"})
		check("viewer", `{"type":"security"}`, []string{
			`"audit_log_count":3,`, "Note added to device 100100002", "Created user: bob", "groups set to [Lab]",
		}, []string{"100100001", "100100003", "100100004"})
		check("admin", `{"type":"tickets"}`, []string{`"stats":{"total":4,"open":1,"in_progress":1,"resolved":1,"closed":1}`, `"total_tickets":4,`, "alpha-cat", "gamma-cat"}, nil)
		check("admin", `{"type":"security"}`, []string{`"audit_log_count":6,`, "Device 100100001 banned", "Device 100100004 restored"}, nil)
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
	if disposition != `attachment; filename="neodesk_inventory_`+before+`.csv"` && disposition != `attachment; filename="neodesk_inventory_`+after+`.csv"` {
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
