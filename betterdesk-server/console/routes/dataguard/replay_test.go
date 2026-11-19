package dataguard_test

import (
	"context"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/routes/internal/replay"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// TestNodeAnswers replays what the Node console's policy and event routes
// answered with its real database adapter at both mounts, on SQLite and on
// PostgreSQL (testdata/node_sqlite.json, testdata/node_postgres.json):
// every route for every role, every field with values of every JSON type,
// ids in every spelling, events with every filter, and rows whose rules
// or details the routes cannot read.
//
// The recording was made without the panel access guard, which sits in
// front of the routes in the console and lets only roles with
// server.config into /api/dataguard. Where the guard refuses a role that
// the routes alone let read, the guard's answer is taken. Node's guard had
// no rule for the routes at /api/bd; the steps there that the guard
// refuses now (DG6) are listed in policyFixes (fixed_test.go) with the
// steps the other bug fixes change.
func TestNodeAnswers(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		guarded := 0
		defer policyFixes.Unused(t, db)
		w.Run(replay.Load(t, db, "node"), policyFixes.Check(w, func(t *testing.T, step replay.Step, got replay.Answer) bool {
			// SQLite summed no event to null; the counts are 0 (BUGS DG4)
			if strings.HasSuffix(step.Path, "/stats") && strings.Contains(step.Answer, ":null") && got.Status == 200 {
				if want := strings.ReplaceAll(step.Answer, ":null", ":0"); got.Body != want {
					t.Errorf("%s: %s, want %s", step.Name, got.Body, want)
				}
				return true
			}
			if strings.HasPrefix(step.Path, "/api/dataguard") && got.Status == 403 && strings.Contains(got.Body, `"code":"panel_access_denied"`) {
				switch step.Role {
				case "global_admin", "operator", "viewer", "guest_issuer":
					guarded++
					return true
				}
			}
			return false
		}))
		if guarded == 0 || guarded > 40 {
			t.Errorf("%d answers were the panel access guard's", guarded)
		}
	})
}

// A database from before policy_type, action and scope were added has
// those columns at the end of the table; a policy goes out with its
// columns in the same order as from a new table (BUGS DG7).
func TestPolicyColumnsHaveOneOrder(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		old := db.Pick(
			`CREATE TABLE dlp_policies (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, description TEXT DEFAULT '', enabled INTEGER DEFAULT 1,
				rules TEXT DEFAULT '[]', created_at DATETIME DEFAULT (datetime('now')), updated_at DATETIME DEFAULT (datetime('now')))`,
			`CREATE TABLE dlp_policies (id SERIAL PRIMARY KEY, name TEXT NOT NULL, description TEXT DEFAULT '', enabled BOOLEAN DEFAULT TRUE,
				rules JSONB DEFAULT '[]', created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW())`)
		if _, err := db.Exec(ctx, old); err != nil {
			t.Fatal(err)
		}
		if err := db.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		w := replay.NewWorld(t, db, nil)
		got := w.Do(replay.Request{Role: "admin", Method: "POST", Path: "/api/bd/policies", Body: replay.Text(`{"name":"old table","policy_type":"usb","scope":"all","rules":[{"b":1,"a":2}]}`)})
		want := db.Pick(
			`{"id":1,"name":"old table","description":"","policy_type":"usb","action":"log","scope":"all","enabled":1,"rules":[{"b":1,"a":2}],"created_at":"<now, SQLite form>","updated_at":"<now, SQLite form>"}`,
			`{"id":1,"name":"old table","description":"","policy_type":"usb","action":"log","scope":"all","enabled":true,"rules":[{"a":2,"b":1}],"created_at":"<now, ISO form>","updated_at":"<now, ISO form>"}`)
		if have := replay.Normalize(got.Body); got.Status != 201 || have != want {
			t.Errorf("create: %d %s\nwant 201 %s", got.Status, have, want)
		}
		if have := replay.Normalize(w.Do(replay.Request{Role: "admin", Path: "/api/dataguard/policies"}).Body); have != "["+want+"]" {
			t.Errorf("list: %s\nwant [%s]", have, want)
		}
	})
}

// Every route of the group is registered at both mounts, the two device
// routes last, as in the Node file.
func TestTheRoutesAtBothMounts(t *testing.T) {
	w := replay.NewWorld(t, storetest.Migrated(t), nil)
	var want []string
	wanted := map[string]bool{}
	for _, mount := range []string{"/api/dataguard", "/api/bd"} {
		for _, route := range []string{"GET /policies", "GET /policies/{id}", "POST /policies", "PATCH /policies/{id}", "DELETE /policies/{id}", "GET /events", "GET /stats", "GET /dlp-policies", "POST /dlp-events"} {
			method, path, _ := strings.Cut(route, " ")
			want = append(want, method+" "+mount+path)
			wanted[method+" "+mount+path] = true
		}
	}
	var got []string
	for _, route := range w.Server.Router().Routes() {
		if entry := route.Method + " " + route.Path; wanted[entry] {
			got = append(got, entry)
		}
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("routes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// A session is no device token
	for _, path := range []string{"/api/bd/dlp-policies", "/api/dataguard/dlp-policies"} {
		if got := w.Do(replay.Request{Role: "admin", Path: path}); got.Status != 401 || got.Body != `{"error":"Bearer access token required"}` {
			t.Errorf("GET %s as admin: %d %s", path, got.Status, got.Body)
		}
	}
}

// TestNodeDeviceAnswers replays what the device routes of the Node file
// (GET dlp-policies, POST dlp-events) answered at both mounts with its
// real database adapter, on SQLite and on PostgreSQL
// (testdata/node_device_sqlite.json, testdata/node_device_postgres.json):
// tokens in every state, policies with every value of enabled and rules a
// database can hold, events with values of every JSON type in every
// field, and the rows they wrote.
//
// One step sends a device token with the session of an operator: at
// /api/dataguard the panel access guard in front of the routes refuses
// that session (the section needs server.config), which the recording,
// made without the guard, does not show.
func TestNodeDeviceAnswers(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		guarded := 0
		defer deviceFixes.Unused(t, db)
		w.Run(replay.Load(t, db, "node_device"), deviceFixes.Check(w, func(t *testing.T, step replay.Step, got replay.Answer) bool {
			if step.Role == "operator" && strings.HasPrefix(step.Path, "/api/dataguard/") && got.Status == 403 &&
				got.Body == `{"success":false,"error":"Panel access denied: dataguard","code":"panel_access_denied"}` {
				guarded++
				return true
			}
			return false
		}))
		if guarded != 1 {
			t.Errorf("%d answers were the panel access guard's, want 1", guarded)
		}
	})
}

// The CSRF gate lets a device's write with its token through without a
// CSRF token when nobody is signed in, at both mounts (BUGS DV2); a
// signed-in session needs the CSRF token at both.
func TestTheCSRFGateAtEachMount(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		w.Exec("INSERT INTO access_tokens (token, token_hash, user_id, client_id, expires_at) VALUES (?, ?, 5, 'device-a', '2999-01-01 00:00:00')",
			[]any{"tok-device-a", "a70dc32ded6e642ec955672a88edb7cc9d6d9e2abb6bf78146a63491adb24155"})
		device := map[string]string{"Authorization": "Bearer tok-device-a"}
		refused := `{"success":false,"error":"Invalid CSRF token. Please refresh the page and try again. If you recently disabled SSL, clear your browser cookies for this site and reload."}`
		event := replay.Text(`{"event_source":"usb"}`)
		for _, c := range []struct {
			name, role, path string
			status           int
			body             string
		}{
			{"at /api/bd without a session", "", "/api/bd/dlp-events", 201, `{"ok":true,"id":1}`},
			{"at /api/dataguard without a session", "", "/api/dataguard/dlp-events", 201, `{"ok":true,"id":2}`},
			{"at /api/bd with a session", "operator", "/api/bd/dlp-events", 403, refused},
			{"at /api/dataguard with a session", "admin", "/api/dataguard/dlp-events", 403, refused},
		} {
			got := w.Do(replay.Request{Role: c.role, Method: "POST", Path: c.path, Body: event, Headers: device, NoCSRF: true})
			if got.Status != c.status || got.Body != c.body {
				t.Errorf("%s: %d %s, want %d %s", c.name, got.Status, got.Body, c.status, c.body)
			}
		}
		if got := w.Do(replay.Request{Method: "POST", Path: "/api/dataguard/dlp-events", Body: event, Headers: device}); got.Status != 201 || got.Body != `{"ok":true,"id":3}` {
			t.Errorf("at /api/dataguard with the CSRF token: %d %s", got.Status, got.Body)
		}
	})
}

// The panel access guard covers the routes for people at both mounts
// (BUGS DG6): every role gets the same answer at /api/bd as at
// /api/dataguard, and a policy row that closes the data guard closes it
// at both.
func TestTheGuardAtBothMounts(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		if got := w.Do(replay.Request{Role: "admin", Method: "POST", Path: "/api/dataguard/policies", Body: replay.Text(`{"name":"one"}`)}); got.Status != 201 {
			t.Fatalf("create: %d %s", got.Status, got.Body)
		}
		routes := []struct{ method, path, body string }{
			{"GET", "/policies", ""}, {"GET", "/policies/1", ""}, {"POST", "/policies", `{"name":"two"}`},
			{"PATCH", "/policies/999", `{"name":"renamed"}`}, {"DELETE", "/policies/999", ""},
			{"GET", "/events", ""}, {"GET", "/stats", ""},
		}
		do := func(role, mount string, route struct{ method, path, body string }) replay.Answer {
			req := replay.Request{Role: role, Method: route.method, Path: mount + route.path}
			if route.body != "" {
				req.Body = replay.Text(route.body)
			}
			return w.Do(req)
		}
		for _, role := range []string{"viewer", "operator", "guest_issuer", "global_admin"} {
			for _, route := range routes {
				want, got := do(role, "/api/dataguard", route), do(role, "/api/bd", route)
				if got.Status != want.Status || got.Body != want.Body {
					t.Errorf("%s %s as %s: %d %s at /api/bd, %d %s at /api/dataguard", route.method, route.path, role, got.Status, got.Body, want.Status, want.Body)
				}
			}
		}
		w.Exec("INSERT INTO panel_access_policies (subject_type, subject_id, resource_key, access_level, updated_at) VALUES ('user', '3', 'dataguard', 'none', '2019-01-01 00:00:00')", nil)
		closed := `{"success":false,"error":"Panel access denied: dataguard","code":"panel_access_denied"}`
		for _, mount := range []string{"/api/dataguard", "/api/bd"} {
			for _, route := range routes {
				if got := do("server_admin", mount, route); got.Status != 403 || got.Body != closed {
					t.Errorf("%s %s%s as a server admin with the data guard closed: %d %s", route.method, mount, route.path, got.Status, got.Body)
				}
			}
		}
		if got := w.Rows("SELECT name FROM dlp_policies ORDER BY id", nil); got != `[{"name":"one"}]` {
			t.Errorf("the policies after the refused requests: %s", got)
		}
	})
}

// Every route asks for its permission. Reading asks device.view or
// audit.view: a role that kept server.config, which lets it past the
// panel access guard, and lost those two is refused by the route, with
// the name of the permission it lacks. Writing asks server.config, which
// the guard asks for writing too: a role without the three permissions is
// refused by the guard, at both mounts.
func TestPermissions(t *testing.T) {
	w := replay.NewWorldWithRoles(t, storetest.Migrated(t), nil, replay.RoleOverrides{
		"operator": {"device.view": false, "audit.view": false, "server.config": true},
		"viewer":   {"device.view": false, "audit.view": false, "server.config": false},
	})
	if got := w.Do(replay.Request{Role: "admin", Method: "POST", Path: "/api/bd/policies", Body: replay.Text(`{"name":"one"}`)}); got.Status != 201 {
		t.Fatalf("create: %d %s", got.Status, got.Body)
	}
	const guarded = `{"success":false,"error":"Panel access denied: dataguard","code":"panel_access_denied"}`
	for _, mount := range []string{"/api/dataguard", "/api/bd"} {
		for _, tt := range []struct{ method, path, body, permission string }{
			{"GET", "/policies", "", "device.view"},
			{"GET", "/policies/1", "", "device.view"},
			{"POST", "/policies", `{"name":"two"}`, "server.config"},
			{"PATCH", "/policies/1", `{"name":"renamed"}`, "server.config"},
			{"DELETE", "/policies/1", "", "server.config"},
			{"GET", "/events", "", "audit.view"},
			{"GET", "/stats", "", "audit.view"},
		} {
			req := replay.Request{Role: "viewer", Method: tt.method, Path: mount + tt.path}
			if tt.body != "" {
				req.Body = replay.Text(tt.body)
			}
			if got := w.Do(req); got.Status != 403 || got.Body != guarded {
				t.Errorf("%s %s without the permissions: %d %s, want 403 %s", tt.method, req.Path, got.Status, got.Body, guarded)
			}
			if tt.permission == "server.config" {
				continue
			}
			req.Role = "operator"
			want := `{"success":false,"error":"Permission denied: ` + tt.permission + `"}`
			if got := w.Do(req); got.Status != 403 || got.Body != want {
				t.Errorf("%s %s with server.config alone: %d %s, want 403 %s", tt.method, req.Path, got.Status, got.Body, want)
			}
		}
	}
	if got := w.Do(replay.Request{Role: "admin", Path: "/api/bd/policies/1"}); got.Status != 200 || !strings.Contains(got.Body, `"name":"one"`) {
		t.Errorf("the policy after the refused writes: %d %s", got.Status, got.Body)
	}
}
