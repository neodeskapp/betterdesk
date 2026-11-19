package tenants_test

import (
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/routes/internal/replay"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// TestNodeAnswers replays what the Node console's tenant routes answered
// with its real database adapter, on SQLite and on PostgreSQL
// (testdata/node_sqlite.json, testdata/node_postgres.json): every route
// for every role, every field with values of every JSON type, ids in
// every spelling. The Go console has to answer the same, byte for byte,
// on the same database, except for the steps the bug fixes change, which
// tenantFixes lists (fixed_test.go).
func TestNodeAnswers(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		defer tenantFixes.Unused(t, db)
		w.Run(replay.Load(t, db, "node"), tenantFixes.Check(w, nil))
	})
}

// Someone who is not signed in gets the console's 401 on every tenant
// route, reads included, where the routes answered 403 (BUGS TN6).
func TestSignedOutGets401(t *testing.T) {
	w := replay.NewWorld(t, storetest.Migrated(t), nil)
	for _, route := range []string{"GET /api/tenants", "GET /api/tenants/1", "POST /api/tenants", "PATCH /api/tenants/1", "DELETE /api/tenants/1",
		"GET /api/tenants/1/devices", "POST /api/tenants/1/devices", "DELETE /api/tenants/1/devices/x", "GET /api/tenants/1/users",
		"POST /api/tenants/1/users", "DELETE /api/tenants/1/users/1", "GET /api/tenants/1/stats"} {
		method, path, _ := strings.Cut(route, " ")
		req := replay.Request{Method: method, Path: path}
		if method == "POST" || method == "PATCH" {
			req.Body = replay.Text(`{"name":"x"}`)
		}
		if got := w.Do(req); got.Status != 401 || got.Body != sessionRequired {
			t.Errorf("%s by nobody: %d %s", route, got.Status, got.Body)
		}
	}
	if got := w.Do(replay.Request{Role: "operator", Path: "/api/tenants"}); got.Status != 403 || got.Body != `{"error":"Admin access required"}` {
		t.Errorf("an operator: %d %s", got.Status, got.Body)
	}
}

// A tenant's texts are text and its device limit a whole number from 0,
// so a value goes in the same on both databases or not at all (BUGS TN1).
func TestCreateRefusesMembersOfTheWrongType(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		for body, want := range map[string]string{
			`{"name":"x","slug":{},"max_devices":[5,"x"]}`: `{"error":"Invalid slug"}`,
			`{"name":"x","slug":77}`:                       `{"error":"Invalid slug"}`,
			`{"name":"x","contact_name":5}`:                `{"error":"Invalid contact_name"}`,
			`{"name":"x","contact_email":true}`:            `{"error":"Invalid contact_email"}`,
			`{"name":"x","notes":{"a":1}}`:                 `{"error":"Invalid notes"}`,
			`{"name":"x","max_devices":"12"}`:              `{"error":"Invalid max_devices"}`,
			`{"name":"x","max_devices":1.5}`:               `{"error":"Invalid max_devices"}`,
			`{"name":"x","max_devices":-1}`:                `{"error":"Invalid max_devices"}`,
			`{"name":"x","max_devices":2147483648}`:        `{"error":"Invalid max_devices"}`,
		} {
			if got := w.Do(replay.Request{Role: "admin", Method: "POST", Path: "/api/tenants", Body: replay.Text(body)}); got.Status != 400 || got.Body != want {
				t.Errorf("POST %s: %d %s, want 400 %s", body, got.Status, got.Body, want)
			}
		}
		if rows := w.Rows("SELECT COUNT(*) AS n FROM tenants", nil); rows != `[{"n":0}]` {
			t.Fatalf("tenants stored: %s", rows)
		}
		got := w.Do(replay.Request{Role: "admin", Method: "POST", Path: "/api/tenants", Body: replay.Text(`{"name":"Kept","slug":null,"notes":null,"max_devices":25}`)})
		if got.Status != 201 || !strings.Contains(got.Body, `"slug":"kept","contact_name":"","contact_email":"","max_devices":25,"notes":""`) {
			t.Errorf("a create: %d %s", got.Status, got.Body)
		}
	})
}

// A change checks every member as the create does, keeps a name, and takes
// active only as a flag: "no" no longer switches a tenant on (BUGS TN7).
func TestUpdateChecksEveryMember(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		if got := w.Do(replay.Request{Role: "admin", Method: "POST", Path: "/api/tenants", Body: replay.Text(`{"name":"Kept","max_devices":3}`)}); got.Status != 201 {
			t.Fatalf("create: %d %s", got.Status, got.Body)
		}
		if got := w.Do(replay.Request{Role: "admin", Method: "PATCH", Path: "/api/tenants/1", Body: replay.Text(`{"active":false}`)}); got.Status != 200 {
			t.Fatalf("switch off: %d %s", got.Status, got.Body)
		}
		before := w.Rows("SELECT name, slug, contact_email, max_devices, notes, CASE WHEN active THEN 1 ELSE 0 END AS active FROM tenants", nil)
		for body, want := range map[string]string{
			`{"name":null}`:        `{"error":"Tenant name is required"}`,
			`{"name":""}`:          `{"error":"Tenant name is required"}`,
			`{"name":5}`:           `{"error":"Tenant name is required"}`,
			`{"slug":null}`:        `{"error":"Invalid slug"}`,
			`{"contact_email":7}`:  `{"error":"Invalid contact_email"}`,
			`{"notes":true}`:       `{"error":"Invalid notes"}`,
			`{"max_devices":"40"}`: `{"error":"Invalid max_devices"}`,
			`{"max_devices":null}`: `{"error":"Invalid max_devices"}`,
			`{"active":"no"}`:      `{"error":"Invalid active"}`,
			`{"active":"false"}`:   `{"error":"Invalid active"}`,
			`{"active":null}`:      `{"error":"Invalid active"}`,
			`{"active":2}`:         `{"error":"Invalid active"}`,
		} {
			if got := w.Do(replay.Request{Role: "admin", Method: "PATCH", Path: "/api/tenants/1", Body: replay.Text(body)}); got.Status != 400 || got.Body != want {
				t.Errorf("PATCH %s: %d %s, want 400 %s", body, got.Status, got.Body, want)
			}
		}
		if after := w.Rows("SELECT name, slug, contact_email, max_devices, notes, CASE WHEN active THEN 1 ELSE 0 END AS active FROM tenants", nil); after != before {
			t.Errorf("the tenant after the refused changes: %s, before %s", after, before)
		}
		if got := w.Do(replay.Request{Role: "admin", Method: "PATCH", Path: "/api/tenants/1", Body: replay.Text(`{"name":" Renamed ","notes":null,"active":1,"max_devices":0}`)}); got.Status != 200 || !strings.Contains(got.Body, `"name":"Renamed"`) {
			t.Errorf("a change: %d %s", got.Status, got.Body)
		}
	})
}
