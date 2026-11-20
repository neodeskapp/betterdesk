package activity_test

import (
	"context"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/routes/internal/replay"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// TestNodeAnswers replays what the Node console's three read routes
// answered with its real database adapter, on SQLite and on PostgreSQL
// (testdata/node_sqlite.json, testdata/node_postgres.json): every route
// for every role, devices whose note and info hold values of every JSON
// type, the window and the limit in every form a query string can give
// them, rows with values only SQLite can hold, and devices that only the
// signal server's table has, with the rows the lookups copy.
//
// Three kinds of answers are not compared byte for byte:
//
//   - The recording was made without the panel access guard, which sits
//     in front of the routes in the console and refuses a guest issuer
//     before the route's own permission check does. The guard's answer is
//     taken there.
//   - Where the recording's stand-in for the rest of the console answered
//     (no route for the address, a path that is no UTF-8), only the
//     status is compared: the console has its own answers for those.
//   - An address with two slashes in a row is no address of the Go
//     console's route table. Express took one of them for the end of the
//     mount, so /api/activity//D1 was the device D1.
//
// The steps the bug fixes change are listed in activityFixes, checked by
// renamed and checkedWindows, or changed by withoutCopies (fixes_test.go).
func TestNodeAnswers(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		guarded, elsewhere, slashes, renames, refused, respelled := 0, 0, 0, 0, 0, 0
		rec := replay.Load(t, db, "node")
		if changed := withoutCopies(&rec, db); changed != map[bool]int{false: 3, true: 4}[db.IsPostgres()] {
			t.Errorf("withoutCopies changed %d steps", changed)
		}
		named := renamed(notesOf(t, rec), &renames)
		windows := checkedWindows(w, &refused, &respelled)
		defer activityFixes.Unused(t, db)
		w.Run(rec, activityFixes.Check(w, func(t *testing.T, step replay.Step, got replay.Answer) bool {
			switch {
			case named(t, step, got):
				return true
			case step.Role == "guest_issuer" && step.Status == 403 && got.Status == 403 && strings.Contains(got.Body, `"code":"panel_access_denied"`):
				guarded++
				return true
			case strings.Contains(step.Path, "//"):
				slashes++
				if got.Status != 404 {
					t.Errorf("%s: status %d, want 404", step.Name, got.Status)
				}
				return true
			case step.Answer == `{"oracle":"no route"}` || step.Status == 400:
				elsewhere++
				if got.Status != step.Status {
					t.Errorf("%s: status %d, want %d", step.Name, got.Status, step.Status)
				}
				return true
			}
			return windows(t, step, got)
		}))
		if guarded != 5 {
			t.Errorf("%d answers were the panel access guard's, want the guest issuer's 5", guarded)
		}
		if elsewhere != 3 || slashes != 2 {
			t.Errorf("%d answers were compared by status only and %d had two slashes, want 3 and 2", elsewhere, slashes)
		}
		// PostgreSQL compared the times already, where they were times in
		// another spelling
		if want := map[bool]int{false: 14, true: 7}[db.IsPostgres()]; refused != 318 || respelled != want {
			t.Errorf("%d windows and limits were refused and %d answers came from times written as ISO text, want 318 and %d", refused, respelled, want)
		}
		if renames == 0 {
			t.Errorf("no answer shows a device under a name the fixes give it")
		}
	})
}

// The console has the group's four routes at /api/bd and at
// /api/activity, in the order of the old file: the route of the desktop
// agent, then the three read routes, after the inventory group at each
// mount. The device route asks for a device token at both mounts, a
// session does not stand for one; the read routes step aside at /api/bd,
// where nothing else of this console answers those addresses.
func TestTheRoutesAtBothMounts(t *testing.T) {
	w := replay.NewWorld(t, storetest.Migrated(t), nil)
	var routes []string
	for _, route := range w.Server.Router().Routes() {
		routes = append(routes, route.Method+" "+route.Path)
	}
	// index finds a route after the position from
	index := func(route string, from int) int {
		for i := from; i < len(routes); i++ {
			if routes[i] == route {
				return i
			}
		}
		return -1
	}
	for _, mount := range []string{"/api/bd", "/api/activity"} {
		// At /api/bd the inventory group has GET /api/bd and GET /api/bd/{id} too, before this one
		at := index("POST "+mount+"/activity", 0)
		if at < 0 {
			t.Errorf("POST %s/activity is not registered", mount)
			continue
		}
		for _, route := range []string{"GET " + mount, "GET " + mount + "/{id}", "GET " + mount + "/{id}/top"} {
			next := index(route, at)
			if next < 0 {
				t.Errorf("%s is not registered after the device route", route)
				continue
			}
			at = next
		}
	}
	if index("POST /api/bd/activity", 0) > index("POST /api/activity/activity", 0) {
		t.Errorf("the group is registered at /api/activity before /api/bd")
	}

	refused := `{"error":"Bearer access token required"}`
	for _, req := range []replay.Request{
		{Role: "admin", Method: "POST", Path: "/api/activity/activity", Body: replay.Text(`{"device_id":"D1"}`)},
		{Role: "admin", Method: "POST", Path: "/api/bd/activity", Body: replay.Text(`{"device_id":"D1"}`)},
		{Method: "POST", Path: "/api/bd/activity", Body: replay.Text(`{"device_id":"D1"}`)},
	} {
		if got := w.Do(req); got.Status != 401 || got.Body != refused {
			t.Errorf("%s %s as %q: %d %s, want 401 %s", req.Method, req.Path, req.Role, got.Status, got.Body, refused)
		}
	}
	for _, path := range []string{"/api/bd", "/api/bd/D1", "/api/bd/D1/top", "/api/bd/activity"} {
		if got := w.Do(replay.Request{Role: "admin", Path: path}); got.Status != 404 || got.Body != `{"success":false,"error":"Not Found"}` {
			t.Errorf("GET %s as admin: %d %s, want the console's 404", path, got.Status, got.Body)
		}
	}
}

// Every route asks for a session and for the permission audit.view
// itself. Under /api/activity the panel access guard in front of the
// group asks first and answers. The guard reads the address as it was
// written, and the routes answer in any letter case, so in another
// spelling it is the route's own checks that answer: the sign-in page
// for nobody, and a refusal for a role that had the permission taken
// away.
func TestPermissions(t *testing.T) {
	w := replay.NewWorldWithRoles(t, storetest.Migrated(t), nil, replay.RoleOverrides{"operator": {"audit.view": false}})
	for _, path := range []string{"/api/activity", "/api/activity/D1", "/api/activity/D1/top"} {
		got := w.Do(replay.Request{Role: "operator", Path: path})
		if want := `{"success":false,"error":"Panel access denied: activity","code":"panel_access_denied"}`; got.Status != 403 || got.Body != want {
			t.Errorf("GET %s without audit.view: %d %s, want 403 %s", path, got.Status, got.Body, want)
		}
		got = w.Do(replay.Request{Path: path})
		if want := `{"success":false,"error":"Unauthorized. Please log in.","code":"session_required"}`; got.Status != 401 || got.Body != want {
			t.Errorf("GET %s signed out: %d %s, want 401 %s", path, got.Status, got.Body, want)
		}
		if got := w.Do(replay.Request{Role: "viewer", Path: path}); got.Status != 200 {
			t.Errorf("GET %s as a viewer: %d %s, want 200", path, got.Status, got.Body)
		}

		spelled := "/API/Activity" + strings.TrimPrefix(path, "/api/activity")
		if got := w.Do(replay.Request{Role: "operator", Path: spelled}); got.Status != 403 || strings.Contains(got.Body, "panel_access_denied") {
			t.Errorf("GET %s without audit.view: %d %s, want the 403 of the route's permission check", spelled, got.Status, got.Body)
		}
		if got := w.Do(replay.Request{Path: spelled}); got.Status != 302 || !strings.HasPrefix(got.Header.Get("Location"), "/login") {
			t.Errorf("GET %s signed out: %d to %q, want 302 to the sign-in page", spelled, got.Status, got.Header.Get("Location"))
		}
		if got := w.Do(replay.Request{Role: "viewer", Path: spelled}); got.Status != 200 {
			t.Errorf("GET %s as a viewer: %d %s, want 200", spelled, got.Status, got.Body)
		}
	}
}

// A device that only the signal server knows gets its name from the
// signal server's table as that server made it, on every way a database
// can have come about. The read writes nothing: no copy in the console's
// table, and a device deleted there stays deleted (BUGS AI7).
func TestTheSignalServersDevices(t *testing.T) {
	storetest.EachOrigin(t, func(t *testing.T, db *store.DB, origin storetest.Origin) {
		ctx := context.Background()
		storetest.GoServer(t, db)
		for _, statement := range []string{
			`INSERT INTO peers (id, hostname, os, version, status) VALUES ('S1', 'from-the-signal-server', 'Linux', '1.4.2', 'ONLINE')`,
			`INSERT INTO peers (id, hostname, soft_deleted) VALUES ('S2', 'deleted there', ` + db.Pick("1", "TRUE") + `)`,
			`INSERT INTO peers (id, hostname) VALUES ('S4', 'back-here')`,
			`INSERT INTO peer (id, note, info, is_deleted) VALUES ('S4', 'old note', '{"hostname":"old"}', ` + db.Pick("1", "TRUE") + `)`,
			`INSERT INTO activity_summaries (device_id, reported_at) VALUES ('S1', '2019-03-04T08:00:00.000Z')`,
		} {
			if _, err := db.Exec(ctx, statement); err != nil {
				t.Fatalf("%s: %v", statement, err)
			}
		}
		w := replay.NewWorld(t, db, nil)
		before := w.Rows(`SELECT id, note, `+db.Pick("info", "info::text AS info")+`, CASE WHEN is_deleted`+db.Pick(" = 1", "")+` THEN 1 ELSE 0 END AS deleted FROM peer ORDER BY id`, nil)
		for path, want := range map[string]string{
			"/api/activity/S1": `"hostname":"from-the-signal-server"`,
			"/api/activity/S2": `"hostname":"S2"`,
			"/api/activity/S3": `"hostname":"S3"`,
			"/api/activity/S4": `"hostname":"back-here"`,
			"/api/activity":    `"device_id":"S1","hostname":"from-the-signal-server"`,
		} {
			if got := w.Do(replay.Request{Role: "admin", Path: path}); got.Status != 200 || !strings.Contains(got.Body, want) {
				t.Errorf("GET %s: %d %s, want %s", path, got.Status, got.Body, want)
			}
		}
		after := w.Rows(`SELECT id, note, `+db.Pick("info", "info::text AS info")+`, CASE WHEN is_deleted`+db.Pick(" = 1", "")+` THEN 1 ELSE 0 END AS deleted FROM peer ORDER BY id`, nil)
		if after != before || !strings.HasPrefix(before, `[{"id":"S4","note":"old note",`) {
			t.Errorf("the console's table after the lookups: %s\nbefore them: %s", after, before)
		}
	})
}

// What goes wrong while a device is looked up costs the name, never the
// answer; a query of the route itself that fails is a 500.
func TestFailures(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		for _, statement := range []string{
			`INSERT INTO peer (id, note) VALUES ('D1', 'pc-one')`,
			`INSERT INTO activity_summaries (device_id, reported_at) VALUES ('D1', '2019-03-04T08:00:00.000Z')`,
			`INSERT INTO activity_sessions (device_id, started_at, ended_at) VALUES ('D1', '2019-03-04T08:00:00.000Z', '2019-03-04T09:00:00.000Z')`,
		} {
			if _, err := db.Exec(ctx, statement); err != nil {
				t.Fatalf("%s: %v", statement, err)
			}
		}
		w := replay.NewWorld(t, db, nil)
		named := func(path, want string) {
			t.Helper()
			if got := w.Do(replay.Request{Role: "admin", Path: path}); got.Status != 200 || !strings.Contains(got.Body, want) {
				t.Errorf("GET %s: %d %s, want %s", path, got.Status, got.Body, want)
			}
		}
		named("/api/activity", `"hostname":"pc-one"`)
		named("/api/activity/D1", `"hostname":"pc-one"`)
		if _, err := db.Exec(ctx, "DROP TABLE peer"); err != nil {
			t.Fatal(err)
		}
		named("/api/activity", `"hostname":"D1"`)
		named("/api/activity/D1", `"hostname":"D1"`)

		if _, err := db.Exec(ctx, "DROP TABLE activity_sessions"); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{"/api/activity", "/api/activity/D1", "/api/activity/D1/top"} {
			got := w.Do(replay.Request{Role: "admin", Path: path})
			if want := `{"error":"Internal server error"}`; got.Status != 500 || got.Body != want {
				t.Errorf("GET %s without the sessions table: %d %s, want 500 %s", path, got.Status, got.Body, want)
			}
		}
	})
}
