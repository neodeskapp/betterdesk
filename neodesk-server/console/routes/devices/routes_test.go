package devices_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/internal/routetest"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// world is a console with a made-up API server that has the devices A
// (open) and C (in a group only e2e_viewer may see), and answers every
// write with success.
func world(t *testing.T, db *store.DB, roles routetest.Overrides, configure func(cfg *config.Config)) (*routetest.World, *routetest.API) {
	t.Helper()
	api := routetest.NewAPI()
	api.Fallback = func(c routetest.Call) routetest.Reply {
		switch {
		case c.Method == "GET" && c.Target == "/api/peers":
			return routetest.Reply{Status: 200, Body: `{"peers":[{"id":"A","hostname":"a"},{"id":"C","hostname":"c"}]}`}
		case c.Method == "GET" && strings.HasPrefix(c.Target, "/api/remote-targets"):
			return routetest.Reply{Status: 200, Body: `{"targets":[]}`}
		case c.Method == "GET" && (c.Target == "/api/peers/A" || c.Target == "/api/peers/C"):
			id := strings.TrimPrefix(c.Target, "/api/peers/")
			return routetest.Reply{Status: 200, Body: `{"id":"` + id + `","hostname":"h"}`}
		case c.Method != "GET":
			return routetest.Reply{Status: 200, Body: `{"status":"ok"}`}
		}
		return routetest.Reply{Status: 404, Body: `{"error":"not found"}`}
	}
	opts := routetest.Options{DB: db, API: api, Env: raisedLimits, Config: func(cfg *config.Config) {
		nodeLanguages(cfg)
		if configure != nil {
			configure(cfg)
		}
	}}
	if roles != nil {
		opts.Roles = roles
	}
	w := routetest.New(t, opts)
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES ('g-viewer', 'Viewer only', '', '', 'manual', '')`,
		`INSERT INTO device_group_user_access (device_group_id, user_id) SELECT id, 6 FROM device_groups WHERE guid = 'g-viewer'`,
		`INSERT INTO device_group_members (device_group_id, peer_id) SELECT id, 'C' FROM device_groups WHERE guid = 'g-viewer'`,
	} {
		if _, err := db.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	return w, api
}

// TestEveryRouteChecksTheScope sends every route that names a device, as
// an account that has every permission but may not see device C: each
// answers 403 and makes no call to the API server that changes anything.
func TestEveryRouteChecksTheScope(t *testing.T) {
	all := map[string]bool{}
	for _, p := range []string{"device.view", "device.edit", "device.delete", "device.ban", "device.change_id", "device.connection_mode", "device.connect"} {
		all[p] = true
	}
	w, api := world(t, storetest.Migrated(t), routetest.Overrides{"operator": all}, nil)
	operator := w.As("operator")
	forbidden := `{"success":false,"error":"You don't have access to this device."}`
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/api/devices/C", ""},
		{"GET", "/api/devices/C/groups", ""},
		{"PUT", "/api/devices/C/groups", `{"groupGuids":[]}`},
		{"GET", "/api/devices/C/availability", ""},
		{"GET", "/api/devices/C/connection-sessions", ""},
		{"POST", "/api/devices/C/telemetry/refresh", `{}`},
		{"POST", "/api/devices/C/telemetry/command", `{"command":"collect.services"}`},
		{"PATCH", "/api/devices/C", `{"note":"x"}`},
		{"DELETE", "/api/devices/C?hard=true", ``},
		{"POST", "/api/devices/C/restore", `{}`},
		{"POST", "/api/devices/C/connection-mode", `{"mode":"normal","reason":"x"}`},
		{"POST", "/api/devices/C/ban", `{}`},
		{"POST", "/api/devices/C/unban", `{}`},
		{"POST", "/api/devices/C/change-id", `{"newId":"NEWID1"}`},
		{"PUT", "/api/devices/C/tags", `{"tags":["x"]}`},
		{"POST", "/api/devices/bulk-delete", `{"ids":["A","C"]}`},
		{"GET", "/api/devices/C/access-policy", ""},
		{"PUT", "/api/devices/C/access-policy", `{}`},
		{"DELETE", "/api/devices/C/access-policy", ``},
		{"GET", "/api/devices/C/connect-password", ""},
		{"POST", "/api/devices/C/rename", `{"display_name":"x"}`},
	} {
		api.Calls()
		got := operator.Do(c.method, c.path, c.body)
		if got.Status != http.StatusForbidden || got.Body != forbidden {
			t.Errorf("%s %s: %d %s, want 403 %s", c.method, c.path, got.Status, got.Body, forbidden)
		}
		for _, call := range api.Calls() {
			if !strings.HasPrefix(call, "GET ") {
				t.Errorf("%s %s made the call %s behind a refusal", c.method, c.path, call)
			}
		}
	}
	// What the account may see goes through
	if got := operator.Do("PATCH", "/api/devices/A", `{"note":"x"}`); got.Status != http.StatusOK {
		t.Errorf("PATCH of a device in scope: %d %s", got.Status, got.Body)
	}
	// The list leaves C out, the viewer sees it
	if got := operator.Do("GET", "/api/devices?sortBy=id&sortOrder=asc", ""); strings.Contains(got.Body, `"id":"C"`) || !strings.Contains(got.Body, `"id":"A"`) {
		t.Errorf("operator's list: %s", got.Body)
	}
	if got := w.As("viewer").Do("GET", "/api/devices?sortBy=id&sortOrder=asc", ""); !strings.Contains(got.Body, `"id":"C"`) {
		t.Errorf("viewer's list: %s", got.Body)
	}
}

// TestBearerRequestsGoToTheClientAPI checks the address GET /api/devices
// shares with the RustDesk client API: a request with a bearer token is
// the client API's, with a session or without; one with a blank token,
// another scheme or none is the console's.
func TestBearerRequestsGoToTheClientAPI(t *testing.T) {
	w, _ := world(t, storetest.Migrated(t), nil, nil)
	admin, out := w.As("admin"), w.SignedOut()

	const client = `{"error":"Authorization required"}`
	const console = `{"success":false,"error":"Unauthorized. Please log in.","code":"session_required"}`
	for _, c := range []struct {
		who    *routetest.Agent
		header string
		want   string
	}{
		{out, "Bearer abc", client},
		{admin, "Bearer abc", client},
		{admin, "Bearer  \t abc  ", client},
		{out, "Bearer ", console},
		{out, "bearer abc", console},
		{out, "Bearerabc", console},
		{out, "Bearer  \xa0", console},
		{out, "Basic YTpi", console},
		{admin, "", `{"success":true,"data":{"devices":[`},
	} {
		got := c.who.Do("GET", "/api/devices?pageSize=5", "", "Authorization", c.header)
		if !strings.HasPrefix(got.Body, c.want) {
			t.Errorf("Authorization %q: %d %s, want %s", c.header, got.Status, got.Body, c.want)
		}
	}
}

// TestAuditFailures checks which routes go on when the audit log cannot
// be written: a change of a device does, a ban answers 500 after the
// server banned.
func TestAuditFailures(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w, api := world(t, db, nil, nil)
		admin := w.As("admin")
		if _, err := db.Exec(context.Background(), `DROP TABLE audit_log`); err != nil {
			t.Fatal(err)
		}
		if got := admin.Do("PATCH", "/api/devices/A", `{"note":"x"}`); got.Status != http.StatusOK || got.Body != `{"success":true,"data":{"changes":1}}` {
			t.Errorf("PATCH without an audit log: %d %s", got.Status, got.Body)
		}
		api.Calls()
		if got := admin.Do("POST", "/api/devices/A/ban", `{"reason":"x"}`); got.Status != http.StatusInternalServerError {
			t.Errorf("ban without an audit log: %d %s", got.Status, got.Body)
		}
		if calls := strings.Join(api.Calls(), "\n"); !strings.Contains(calls, `POST /api/peers/A/ban {"reason":"x"}`) {
			t.Errorf("the ban did not reach the server: %s", calls)
		}
	})
}

// TestPermissions sends each route as an account whose role lacks the
// route's permission (and passes the panel access guard): the guard of
// the route answers, naming the permission. Below /api/devices/{id} the
// device scope check comes first and looks the device up (including the
// deleted ones, where the id is none of an active device), except on the
// bulk delete; nothing else reaches the server.
func TestPermissions(t *testing.T) {
	w, api := world(t, storetest.Migrated(t), nil, nil)
	agents := map[string]*routetest.Agent{}
	for _, c := range []struct{ role, method, path, body, permission string }{
		{"pro", "GET", "/api/devices", "", ""},
		{"operator", "DELETE", "/api/devices/A", "", "device.delete"},
		{"operator", "POST", "/api/devices/A/restore", "{}", "device.delete"},
		{"operator", "POST", "/api/devices/bulk-delete", `{"ids":["A"]}`, "device.delete"},
		{"operator", "POST", "/api/devices/A/ban", "{}", "device.ban"},
		{"operator", "POST", "/api/devices/A/unban", "{}", "device.ban"},
		{"operator", "POST", "/api/devices/A/change-id", `{"newId":"NEWID1"}`, "device.change_id"},
		{"operator", "POST", "/api/remote-targets", "{}", "remote_target.edit"},
		{"operator", "PATCH", "/api/remote-targets/rt_1", "{}", "remote_target.edit"},
		{"operator", "PUT", "/api/remote-targets/rt_1/credentials", "{}", "remote_target.edit"},
		{"operator", "DELETE", "/api/remote-targets/rt_1/credentials", "", "remote_target.edit"},
		{"operator", "DELETE", "/api/remote-targets/rt_1", "", "remote_target.delete"},
		{"viewer", "GET", "/api/devices/A/connect-password", "", "device.connect"},
		// The remote section of the panel access guard refuses an account
		// without remote_target.view first (PA14, F12).
		{"guest_issuer", "GET", "/api/remote-targets", "", "section:remote"},
		{"guest_issuer", "GET", "/api/remote-targets/rt_1", "", "section:remote"},
		{"guest_issuer", "GET", "/api/remote-targets/rt_1/credentials", "", "section:remote"},
		{"guest_issuer", "GET", "/api/remote-targets/readiness", "", "section:remote"},
	} {
		a := agents[c.role]
		if a == nil {
			if c.role == "pro" {
				a = w.SignedOut()
			} else {
				a = w.As(c.role)
			}
			agents[c.role] = a
		}
		api.Calls()
		got := a.Do(c.method, c.path, c.body)
		want := `{"success":false,"error":"Permission denied: ` + c.permission + `"}`
		if section, guarded := strings.CutPrefix(c.permission, "section:"); guarded {
			want = `{"success":false,"error":"Panel access denied: ` + section + `","code":"panel_access_denied"}`
		}
		status := http.StatusForbidden
		if c.permission == "" {
			want, status = `{"success":false,"error":"Unauthorized. Please log in.","code":"session_required"}`, http.StatusUnauthorized
		}
		if got.Status != status || got.Body != want {
			t.Errorf("%s %s as %s: %d %s, want %d %s", c.method, c.path, c.role, got.Status, got.Body, status, want)
		}
		var lookups []string
		if id, below := strings.CutPrefix(c.path, "/api/devices/"); below && id != "bulk-delete" {
			id, _, _ = strings.Cut(id, "/")
			lookups = append(lookups, "GET /api/peers/"+id)
		}
		if calls := api.Calls(); strings.Join(calls, "\n") != strings.Join(lookups, "\n") {
			t.Errorf("%s %s as %s called the server %q behind the refusal, want only the scope check's %q", c.method, c.path, c.role, calls, lookups)
		}
	}
}

// TestScreenshotNeedsDeviceEdit asks for a device's screenshot as each
// built-in role, past the panel access guard, which refuses every role
// without device.edit for it: the route's own check refuses the roles
// that see devices but may not change them, as the guard does (BUGS PA3,
// analysis F13), and lets the others through to the agent, which is not
// connected.
func TestScreenshotNeedsDeviceEdit(t *testing.T) {
	w, _ := world(t, storetest.Migrated(t), nil, nil)
	d := w.Server.Deps()
	for _, c := range []struct {
		role, want string
		status     int
	}{
		{"viewer", `{"success":false,"error":"Permission denied: device.edit"}`, http.StatusForbidden},
		{"guest_issuer", `{"success":false,"error":"Permission denied: device.edit"}`, http.StatusForbidden},
		{"server_admin", `{"success":false,"error":"Permission denied: device.edit"}`, http.StatusForbidden},
		{"operator", `{"success":false,"error":"agent_offline"}`, http.StatusServiceUnavailable},
		{"global_admin", `{"success":false,"error":"agent_offline"}`, http.StatusServiceUnavailable},
		{"admin", `{"success":false,"error":"agent_offline"}`, http.StatusServiceUnavailable},
	} {
		r := kit.Begin(httptest.NewRequest("POST", "/api/devices/A/screenshot", strings.NewReader(`{}`)), httpx.TrustProxy{}, nil)
		r.Header.Set("Content-Type", "application/json")
		loaded := d.Access.Load(r.Context(), access.Session{UserID: routetest.RoleIDs[c.role], HasUser: true, Role: c.role})
		if !loaded.HasPermission("device.view") {
			t.Fatalf("%s lacks device.view", c.role)
		}
		kit.SetAccess(r, loaded)
		got := httptest.NewRecorder()
		if !w.Server.Router().Serve(got, r) {
			t.Fatalf("no route answered POST /api/devices/A/screenshot")
		}
		if got.Code != c.status || got.Body.String() != c.want {
			t.Errorf("screenshot as %s: %d %s, want %d %s", c.role, got.Code, got.Body.String(), c.status, c.want)
		}
	}
}

// TestClipboardNeedsDeviceConnect reads a device's clipboard as each
// built-in role, past the panel access guard: the roles without
// device.connect (viewer, guest_issuer, server_admin) are refused, the
// others reach the route; the processes, services, events and activity of
// the device stay with device.view (BUGS RV67, A82).
func TestClipboardNeedsDeviceConnect(t *testing.T) {
	w, _ := world(t, storetest.Migrated(t), nil, nil)
	d := w.Server.Deps()
	const denied = `{"success":false,"error":"Permission denied: device.connect"}`
	for _, c := range []struct {
		role    string
		refused bool
	}{
		{"viewer", true}, {"guest_issuer", true}, {"server_admin", true},
		{"operator", false}, {"global_admin", false}, {"admin", false},
	} {
		for _, path := range []string{"clipboard", "processes", "services", "events", "activity"} {
			r := kit.Begin(httptest.NewRequest("GET", "/api/devices/A/"+path, nil), httpx.TrustProxy{}, nil)
			kit.SetAccess(r, d.Access.Load(r.Context(), access.Session{UserID: routetest.RoleIDs[c.role], HasUser: true, Role: c.role}))
			got := httptest.NewRecorder()
			if !w.Server.Router().Serve(got, r) {
				t.Fatalf("no route answered GET /api/devices/A/%s", path)
			}
			refused := got.Code == http.StatusForbidden
			if want := c.refused && path == "clipboard"; refused != want || refused && got.Body.String() != denied {
				t.Errorf("%s as %s: %d %s, want refused %v", path, c.role, got.Code, got.Body.String(), want)
			}
		}
	}
}

// TestListSortFields orders two devices by every field the list may be
// ordered by, and by one it may not, which falls back on the last
// contact. P1 comes first by every field but the last contact.
func TestListSortFields(t *testing.T) {
	api := routetest.NewAPI()
	api.On("GET /api/peers", 200, `{"peers":[`+
		`{"id":"P2","hostname":"b","os":"b","version":"b","user":"b","note":"b","created_at":"2020-01-02","last_online":"2020-01-01"},`+
		`{"id":"P1","hostname":"a","os":"a","version":"a","user":"a","note":"a","created_at":"2020-01-01","last_online":"2021-01-01"}]}`)
	api.On("GET /api/remote-targets?org_id=&include_disabled=true", 200, `{"targets":[]}`)
	w := routetest.New(t, routetest.Options{API: api, Env: raisedLimits, Config: nodeLanguages})
	admin := w.As("admin")
	first := func(query string) string {
		body := admin.Do("GET", "/api/devices?"+query, "").Body
		i := strings.Index(body, `"id":"`)
		if i < 0 {
			return body
		}
		return body[i+6 : i+8]
	}
	for _, field := range []string{"id", "hostname", "created_at", "os", "version", "username", "note"} {
		if got := first("sortBy=" + field + "&sortOrder=asc"); got != "P1" {
			t.Errorf("sortBy=%s ascending: %s first, want P1", field, got)
		}
		if got := first("sortBy=" + field + "&sortOrder=DESC"); got != "P2" {
			t.Errorf("sortBy=%s descending: %s first, want P2", field, got)
		}
	}
	if got := first("sortBy=password&sortOrder=asc"); got != "P2" {
		t.Errorf("an unknown field orders by last contact: %s first, want P2", got)
	}
	if got := first(""); got != "P1" {
		t.Errorf("the default order is the newest contact first: %s first, want P1", got)
	}
}
