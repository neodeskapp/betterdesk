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
// the routes alone let read, the guard's answer is taken; /api/bd has no
// guard, so there every answer is compared.
func TestNodeAnswers(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		guarded := 0
		w.Run(replay.Load(t, db, "node"), func(t *testing.T, step replay.Step, got replay.Answer) bool {
			if strings.HasPrefix(step.Path, "/api/dataguard") && got.Status == 403 && strings.Contains(got.Body, `"code":"panel_access_denied"`) {
				switch step.Role {
				case "global_admin", "operator", "viewer", "guest_issuer":
					guarded++
					return true
				}
			}
			return false
		})
		if guarded == 0 || guarded > 40 {
			t.Errorf("%d answers were the panel access guard's", guarded)
		}
	})
}

// A database from before policy_type, action and scope were added has
// those columns at the end of the table, and a policy goes out with its
// columns in the table's order.
func TestPolicyColumnsKeepTheOrderOfTheTable(t *testing.T) {
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
			`{"id":1,"name":"old table","description":"","enabled":1,"rules":[{"b":1,"a":2}],"created_at":"<now, SQLite form>","updated_at":"<now, SQLite form>","policy_type":"usb","action":"log","scope":"all"}`,
			`{"id":1,"name":"old table","description":"","enabled":true,"rules":[{"a":2,"b":1}],"created_at":"<now, ISO form>","updated_at":"<now, ISO form>","policy_type":"usb","action":"log","scope":"all"}`)
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
//
// The recording mounted this group alone. In the console the inventory
// group comes first at /api/bd, and its read route GET /api/bd/{id} steps
// aside only for the mount written in lower case (BUGS DV8): GET
// /API/BD/DLP-POLICIES/ is that route's, which sends a request without a
// session to the sign-in page.
func TestNodeDeviceAnswers(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		guarded, spelled := 0, 0
		w.Run(replay.Load(t, db, "node_device"), func(t *testing.T, step replay.Step, got replay.Answer) bool {
			if step.Role == "operator" && strings.HasPrefix(step.Path, "/api/dataguard/") && got.Status == 403 &&
				got.Body == `{"success":false,"error":"Panel access denied: dataguard","code":"panel_access_denied"}` {
				guarded++
				return true
			}
			if step.Path == "/API/BD/DLP-POLICIES/" {
				spelled++
				if got.Status != 302 || got.Header.Get("Location") != "/login" {
					t.Errorf("%s: %d to %q, want the inventory route's 302 to /login", step.Name, got.Status, got.Header.Get("Location"))
				}
				return true
			}
			return false
		})
		if guarded != 1 || spelled != 1 {
			t.Errorf("%d answers were the panel access guard's and %d the inventory route's, want 1 and 1", guarded, spelled)
		}
	})
}

// The CSRF gate lets a write below /api/bd/ through without a token when
// nobody is signed in; at /api/dataguard a device needs the CSRF token
// like a browser, and below /api/bd/ a signed-in session needs it too.
// The Node console answers the same (contract cases
// dataguard/device-event-*-csrf-*).
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
			{"at /api/dataguard without a session", "", "/api/dataguard/dlp-events", 403, refused},
			{"at /api/bd with a session", "operator", "/api/bd/dlp-events", 403, refused},
			{"at /api/dataguard with a session", "admin", "/api/dataguard/dlp-events", 403, refused},
		} {
			got := w.Do(replay.Request{Role: c.role, Method: "POST", Path: c.path, Body: event, Headers: device, NoCSRF: true})
			if got.Status != c.status || got.Body != c.body {
				t.Errorf("%s: %d %s, want %d %s", c.name, got.Status, got.Body, c.status, c.body)
			}
		}
		if got := w.Do(replay.Request{Method: "POST", Path: "/api/dataguard/dlp-events", Body: event, Headers: device}); got.Status != 201 || got.Body != `{"ok":true,"id":2}` {
			t.Errorf("at /api/dataguard with the CSRF token: %d %s", got.Status, got.Body)
		}
	})
}

// Every route asks for its permission, also where every role of the
// console has it out of the box: a role that had the three permissions
// taken away is refused, with the name of the permission it lacks.
func TestPermissions(t *testing.T) {
	w := replay.NewWorldWithRoles(t, storetest.Migrated(t), nil, replay.RoleOverrides{
		"operator": {"device.view": false, "audit.view": false, "server.config": false},
	})
	if got := w.Do(replay.Request{Role: "admin", Method: "POST", Path: "/api/bd/policies", Body: replay.Text(`{"name":"one"}`)}); got.Status != 201 {
		t.Fatalf("create: %d %s", got.Status, got.Body)
	}
	for _, tt := range []struct{ method, path, body, permission string }{
		{"GET", "/api/bd/policies", "", "device.view"},
		{"GET", "/api/bd/policies/1", "", "device.view"},
		{"POST", "/api/bd/policies", `{"name":"two"}`, "server.config"},
		{"PATCH", "/api/bd/policies/1", `{"name":"renamed"}`, "server.config"},
		{"DELETE", "/api/bd/policies/1", "", "server.config"},
		{"GET", "/api/bd/events", "", "audit.view"},
		{"GET", "/api/bd/stats", "", "audit.view"},
	} {
		req := replay.Request{Role: "operator", Method: tt.method, Path: tt.path}
		if tt.body != "" {
			req.Body = replay.Text(tt.body)
		}
		want := `{"success":false,"error":"Permission denied: ` + tt.permission + `"}`
		if got := w.Do(req); got.Status != 403 || got.Body != want {
			t.Errorf("%s %s without the permissions: %d %s, want 403 %s", tt.method, tt.path, got.Status, got.Body, want)
		}
	}
	// A guest issuer has device.view and nothing else of the three
	for path, status := range map[string]int{"/api/bd/policies": 200, "/api/bd/policies/1": 200, "/api/bd/events": 403, "/api/bd/stats": 403} {
		if got := w.Do(replay.Request{Role: "guest_issuer", Path: path}); got.Status != status {
			t.Errorf("GET %s as a guest issuer: %d %s, want %d", path, got.Status, got.Body, status)
		}
	}
	if got := w.Do(replay.Request{Role: "admin", Path: "/api/bd/policies/1"}); got.Status != 200 || !strings.Contains(got.Body, `"name":"one"`) {
		t.Errorf("the policy after the refused writes: %d %s", got.Status, got.Body)
	}
}
