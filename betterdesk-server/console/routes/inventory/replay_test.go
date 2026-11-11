package inventory_test

import (
	"context"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/routes/internal/replay"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// TestNodeAnswers replays what the Node console's two read routes
// answered with its real database adapter, on SQLite and on PostgreSQL
// (testdata/node_sqlite.json, testdata/node_postgres.json): both routes
// for every role, more than 1,200 reports in which every member the list
// reads holds a value of every JSON type, disks with sizes in every unit
// and of every type, the paging parameters in every form, telemetry with
// what each database can hold in a number column, reports the list
// cannot read, and the list without any report.
//
// Two kinds of answers are not compared byte for byte:
//
//   - Where the recording's stand-in for the rest of the console answered
//     (no route for the address, a path that is no UTF-8), only the
//     status is compared: the console has its own answers for those.
//   - An address with two slashes in a row is no address of the Go
//     console's route table. Express took one of them for the end of the
//     mount, so /api/inventory//PC-1 was the device PC-1.
func TestNodeAnswers(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		elsewhere, slashes := 0, 0
		w.Run(replay.Load(t, db, "node"), func(t *testing.T, step replay.Step, got replay.Answer) bool {
			switch {
			case strings.Contains(step.Path, "//"):
				slashes++
				if got.Status != 404 {
					t.Errorf("%s: status %d, want 404", step.Name, got.Status)
				}
				return true
			case step.Answer == `{"oracle":"no route"}` || step.Answer == `{"success":false,"error":"Internal Server Error"}`:
				elsewhere++
				if got.Status != step.Status {
					t.Errorf("%s: status %d, want %d", step.Name, got.Status, step.Status)
				}
				return true
			}
			return false
		})
		if elsewhere != 2 || slashes != 1 {
			t.Errorf("%d answers were compared by status only and %d had two slashes, want 2 and 1", elsewhere, slashes)
		}
	})
}

// The routes of the desktop agent are not registered: they take a device
// token. And the group answers nothing under /api/bd, where the old
// console mounted it for those routes.
func TestTheGroupHasNoDeviceRoutes(t *testing.T) {
	w := replay.NewWorld(t, storetest.Migrated(t), nil)
	registered := map[string]bool{}
	for _, route := range w.Server.Router().Routes() {
		if strings.HasPrefix(route.Path, "/api/inventory") || strings.HasPrefix(route.Path, "/api/bd") && (strings.Contains(route.Path, "inventory") || strings.Contains(route.Path, "telemetry")) {
			registered[route.Method+" "+route.Path] = true
		}
	}
	for _, route := range []string{"GET /api/inventory", "GET /api/inventory/{id}"} {
		if !registered[route] {
			t.Errorf("%s is not registered", route)
		}
		delete(registered, route)
	}
	for route := range registered {
		t.Errorf("%s is registered: the group has the two read routes and nothing else", route)
	}
	body := replay.Text(`{"device_id":"D1","hardware":{}}`)
	for _, req := range []replay.Request{
		{Role: "admin", Method: "POST", Path: "/api/inventory/inventory", Body: body},
		{Role: "admin", Method: "POST", Path: "/api/inventory/telemetry", Body: body},
		{Role: "admin", Path: "/api/inventory/inventory/D1"},
		{Role: "admin", Method: "POST", Path: "/api/bd/inventory", Body: body},
		{Role: "admin", Method: "POST", Path: "/api/bd/telemetry", Body: body},
		{Role: "admin", Path: "/api/bd/inventory/D1"},
		{Role: "admin", Path: "/api/bd"},
		{Role: "admin", Path: "/api/bd/D1"},
	} {
		if got := w.Do(req); got.Status != 404 || strings.Contains(got.Body, "No inventory data") {
			t.Errorf("%s %s: %d %s, want the console's 404", req.Method, req.Path, got.Status, got.Body)
		}
	}
}

// Both routes ask for a session and for the permission device.view: a
// role that had the permission taken away is refused by its name, also
// before the paging parameters are looked at.
func TestPermissions(t *testing.T) {
	w := replay.NewWorldWithRoles(t, storetest.Migrated(t), nil, replay.RoleOverrides{"operator": {"device.view": false}})
	for _, path := range []string{"/api/inventory", "/api/inventory?page=x", "/api/inventory/D1"} {
		got := w.Do(replay.Request{Role: "operator", Path: path})
		if want := `{"success":false,"error":"Permission denied: device.view"}`; got.Status != 403 || got.Body != want {
			t.Errorf("GET %s without device.view: %d %s, want 403 %s", path, got.Status, got.Body, want)
		}
		got = w.Do(replay.Request{Path: path})
		if want := `{"success":false,"error":"Unauthorized. Please log in.","code":"session_required"}`; got.Status != 401 || got.Body != want {
			t.Errorf("GET %s signed out: %d %s, want 401 %s", path, got.Status, got.Body, want)
		}
	}
	for path, status := range map[string]int{"/api/inventory": 200, "/api/inventory?page=x": 400, "/api/inventory/D1": 404} {
		if got := w.Do(replay.Request{Role: "guest_issuer", Path: path}); got.Status != status {
			t.Errorf("GET %s as a guest issuer: %d %s, want %d", path, got.Status, got.Body, status)
		}
	}
}

// A query that fails is a 500 with the group's error, on the list and on
// one report; so is the telemetry of a device without a report.
func TestFailures(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		w := replay.NewWorld(t, db, nil)
		want := `{"error":"Internal server error"}`
		if _, err := db.Exec(ctx, "DROP TABLE device_telemetry"); err != nil {
			t.Fatal(err)
		}
		if got := w.Do(replay.Request{Role: "admin", Path: "/api/inventory"}); got.Status != 200 {
			t.Errorf("the list without the telemetry table: %d %s, want 200: the list reads no telemetry", got.Status, got.Body)
		}
		if got := w.Do(replay.Request{Role: "admin", Path: "/api/inventory/unknown"}); got.Status != 500 || got.Body != want {
			t.Errorf("a report without the telemetry table: %d %s, want 500 %s", got.Status, got.Body, want)
		}
		if _, err := db.Exec(ctx, "DROP TABLE device_inventory"); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{"/api/inventory", "/api/inventory/D1"} {
			if got := w.Do(replay.Request{Role: "admin", Path: path}); got.Status != 500 || got.Body != want {
				t.Errorf("GET %s without the inventory table: %d %s, want 500 %s", path, got.Status, got.Body, want)
			}
		}
		if got := w.Do(replay.Request{Role: "admin", Path: "/api/inventory?limit=0"}); got.Status != 400 {
			t.Errorf("bad paging without the inventory table: %d %s, want 400: the parameters are checked first", got.Status, got.Body)
		}
	})
}
