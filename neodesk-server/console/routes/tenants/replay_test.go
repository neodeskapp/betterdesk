package tenants_test

import (
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/replay"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// TestNodeAnswers replays what the Node console's tenant routes answered
// with its real database adapter, on SQLite and on PostgreSQL
// (testdata/node_sqlite.json, testdata/node_postgres.json): every route
// for every role, every field with values of every JSON type, ids in
// every spelling. The Go console has to answer the same, byte for byte,
// on the same database, except for the steps the bug fixes change, which
// tenantFixes and tenantSteps list (fixed_test.go).
func TestNodeAnswers(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newWorld(t, db)
		steps, done := stepsCheck(db)
		defer done(t)
		defer tenantFixes.Unused(t, db)
		w.Run(replay.Load(t, db, "node"), tenantFixes.Check(w, steps))
	})
}

// Someone who is not signed in gets the console's 401 on every tenant
// route, reads included, where the routes answered 403 (BUGS TN6).
func TestSignedOutGets401(t *testing.T) {
	w := newWorld(t, storetest.Migrated(t))
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
		w := newWorld(t, db)
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
		w := newWorld(t, db)
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

// A tenant id, and the user id of an assignment, is the digits of a
// positive 32-bit number as the client wrote them; anything else is not
// found, on both databases (BUGS TN3).
func TestTenantIDsThatAreNoWholeNumber(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newWorld(t, db)
		if got := w.Do(replay.Request{Role: "admin", Method: "POST", Path: "/api/tenants", Body: replay.Text(`{"name":"One"}`)}); got.Status != 201 {
			t.Fatalf("create: %d %s", got.Status, got.Body)
		}
		notFound := `{"error":"Tenant not found"}`
		for _, id := range []string{"1.0", "%201", "%31", "1e0", "0x1", "abc", "Infinity", "3000000000", "0", "-1", "1.5"} {
			for _, path := range []string{"/api/tenants/" + id, "/api/tenants/" + id + "/devices", "/api/tenants/" + id + "/users", "/api/tenants/" + id + "/stats"} {
				if got := w.Do(replay.Request{Role: "admin", Path: path}); got.Status != 404 || got.Body != notFound {
					t.Errorf("GET %s: %d %s", path, got.Status, got.Body)
				}
			}
			if got := w.Do(replay.Request{Role: "admin", Method: "PATCH", Path: "/api/tenants/" + id, Body: replay.Text(`{"notes":"x"}`)}); got.Status != 404 || got.Body != notFound {
				t.Errorf("PATCH %s: %d %s", id, got.Status, got.Body)
			}
			if got := w.Do(replay.Request{Role: "admin", Method: "DELETE", Path: "/api/tenants/1/users/" + id}); got.Status != 404 || got.Body != `{"error":"Assignment not found"}` {
				t.Errorf("DELETE user %s: %d %s", id, got.Status, got.Body)
			}
		}
		if got := w.Do(replay.Request{Role: "admin", Path: "/api/tenants/01"}); got.Status != 200 {
			t.Errorf("GET /api/tenants/01: %d %s", got.Status, got.Body)
		}
		if rows := w.Rows("SELECT notes FROM tenants", nil); rows != `[{"notes":""}]` {
			t.Errorf("the tenant after the refused changes: %s", rows)
		}
	})
}

// A slug that is taken is 409 on both databases, in a create and in a
// change, decided by the error's code and not its text (BUGS TN2).
func TestTakenSlugIs409(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newWorld(t, db)
		post := func(body string) replay.Answer {
			return w.Do(replay.Request{Role: "admin", Method: "POST", Path: "/api/tenants", Body: replay.Text(body)})
		}
		taken := `{"error":"Tenant slug already exists"}`
		for _, body := range []string{`{"name":"Acme"}`, `{"name":"Other","slug":"UNIQUE"}`} {
			if got := post(body); got.Status != 201 {
				t.Fatalf("create %s: %d %s", body, got.Status, got.Body)
			}
		}
		if got := post(`{"name":"ACME!"}`); got.Status != 409 || got.Body != taken {
			t.Errorf("a taken slug: %d %s", got.Status, got.Body)
		}
		if got := w.Do(replay.Request{Role: "admin", Method: "PATCH", Path: "/api/tenants/2", Body: replay.Text(`{"slug":"acme"}`)}); got.Status != 409 || got.Body != taken {
			t.Errorf("a change to a taken slug: %d %s", got.Status, got.Body)
		}
		if got := post(`{"name":"Third","notes":"UNIQUE"}`); got.Status != 201 {
			t.Errorf("a text that says UNIQUE: %d %s", got.Status, got.Body)
		}
	})
}

// Assigning a device or a user a second time leaves the assignment as it
// is, with its id and the time it was made, on both databases (BUGS TN8).
func TestAssigningAgainKeepsTheRow(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newWorld(t, db)
		if got := w.Do(replay.Request{Role: "admin", Method: "POST", Path: "/api/tenants", Body: replay.Text(`{"name":"One"}`)}); got.Status != 201 {
			t.Fatalf("create: %d %s", got.Status, got.Body)
		}
		assign := func() {
			t.Helper()
			for path, body := range map[string]string{"/api/tenants/1/devices": `{"device_id":"D1"}`, "/api/tenants/1/users": `{"user_id":2}`} {
				if got := w.Do(replay.Request{Role: "admin", Method: "POST", Path: path, Body: replay.Text(body)}); got.Status != 200 || !strings.HasPrefix(got.Body, `{"ok":true`) {
					t.Fatalf("POST %s %s: %d %s", path, body, got.Status, got.Body)
				}
			}
		}
		assign()
		w.Exec("UPDATE tenant_devices SET assigned_at = '2019-03-04 05:06:07'", nil)
		w.Exec("UPDATE tenant_users SET assigned_at = '2019-03-04 05:06:07'", nil)
		const rows = "SELECT id, CASE WHEN assigned_at < '2020-01-01' THEN 'first' ELSE 'again' END AS assigned FROM "
		before := w.Rows(rows+"tenant_devices", nil) + w.Rows(rows+"tenant_users", nil)
		if before != `[{"id":1,"assigned":"first"}][{"id":1,"assigned":"first"}]` {
			t.Fatalf("the assignments: %s", before)
		}
		assign()
		if after := w.Rows(rows+"tenant_devices", nil) + w.Rows(rows+"tenant_users", nil); after != before {
			t.Errorf("the assignments after they were made again: %s, before %s", after, before)
		}
	})
}
