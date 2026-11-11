package activity_test

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/routes/internal/replay"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// What the query parser of package jsbind does not take apart the way
// the old console's did, in a from or a to: an index of 20 and more
// (from[25]=x was a list of one value), and an object beside a list. An
// object bound no value and a list one per element, so a statement could
// come out with the right number of values and run. The Go console
// answers 500 where the old one found rows.
var beyondTheQueryParser = regexp.MustCompile(`\?(from\[25\]=|from\[day\]=x&to\[\]=[^&]+&to\[\]=[^&]+$|to\[day\]=x&from\[\]=[^&]+&from\[\]=[^&]+$)`)

// TestNodeAnswers replays what the Node console's three read routes
// answered with its real database adapter, on SQLite and on PostgreSQL
// (testdata/node_sqlite.json, testdata/node_postgres.json): every route
// for every role, devices whose note and info hold values of every JSON
// type, the window and the limit in every form a query string can give
// them, rows with values only SQLite can hold, and devices that only the
// signal server's table has, with the rows the lookups copy.
//
// Four kinds of answers are not compared byte for byte:
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
//   - Windows the query parser does not take apart, see
//     beyondTheQueryParser.
func TestNodeAnswers(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		guarded, elsewhere, slashes, unparsed := 0, 0, 0, 0
		w.Run(replay.Load(t, db, "node"), func(t *testing.T, step replay.Step, got replay.Answer) bool {
			switch {
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
			case step.Status == 200 && got.Status == 500 && beyondTheQueryParser.MatchString(step.Path):
				unparsed++
				return true
			}
			return false
		})
		if guarded != 5 {
			t.Errorf("%d answers were the panel access guard's, want the guest issuer's 5", guarded)
		}
		if elsewhere != 3 || slashes != 2 {
			t.Errorf("%d answers were compared by status only and %d had two slashes, want 3 and 2", elsewhere, slashes)
		}
		// PostgreSQL reads neither an object nor a list as a time, so
		// there only the index is left
		if want := map[bool]int{false: 9, true: 3}[db.IsPostgres()]; unparsed != want {
			t.Errorf("%d windows were beyond the query parser, want %d", unparsed, want)
		}
	})
}

// The route a device uploads its activity with is not registered: it
// takes a device token. And the group answers nothing under /api/bd,
// where the old console mounted it for that route.
func TestTheGroupHasNoDeviceRoutes(t *testing.T) {
	w := replay.NewWorld(t, storetest.Migrated(t), nil)
	registered := map[string]bool{}
	for _, route := range w.Server.Router().Routes() {
		if strings.HasPrefix(route.Path, "/api/activity") || strings.HasPrefix(route.Path, "/api/bd") && strings.Contains(route.Path, "activity") {
			registered[route.Method+" "+route.Path] = true
		}
	}
	want := []string{"GET /api/activity", "GET /api/activity/{id}", "GET /api/activity/{id}/top"}
	for _, route := range want {
		if !registered[route] {
			t.Errorf("%s is not registered", route)
		}
		delete(registered, route)
	}
	for route := range registered {
		t.Errorf("%s is registered: the group has the three read routes and nothing else", route)
	}
	for _, req := range []replay.Request{
		{Role: "admin", Method: "POST", Path: "/api/activity/activity", Body: replay.Text(`{"device_id":"D1"}`)},
		{Role: "admin", Method: "POST", Path: "/api/bd/activity", Body: replay.Text(`{"device_id":"D1"}`)},
		{Role: "admin", Path: "/api/bd"},
		{Role: "admin", Path: "/api/bd/D1"},
		{Role: "admin", Path: "/api/bd/D1/top"},
	} {
		if got := w.Do(req); got.Status != 404 {
			t.Errorf("%s %s: %d %s, want the console's 404", req.Method, req.Path, got.Status, got.Body)
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
// can have come about, and the lookup leaves a copy in the console's
// table.
func TestTheSignalServersDevices(t *testing.T) {
	storetest.EachOrigin(t, func(t *testing.T, db *store.DB, origin storetest.Origin) {
		ctx := context.Background()
		storetest.GoServer(t, db)
		for _, statement := range []string{
			`INSERT INTO peers (id, hostname, os, version, status) VALUES ('S1', 'from-the-signal-server', 'Linux', '1.4.2', 'ONLINE')`,
			`INSERT INTO peers (id, hostname, soft_deleted) VALUES ('S2', 'deleted there', ` + db.Pick("1", "TRUE") + `)`,
			`INSERT INTO activity_summaries (device_id, reported_at) VALUES ('S1', '2019-03-04T08:00:00.000Z')`,
		} {
			if _, err := db.Exec(ctx, statement); err != nil {
				t.Fatalf("%s: %v", statement, err)
			}
		}
		w := replay.NewWorld(t, db, nil)
		for path, want := range map[string]string{
			"/api/activity/S1": `"hostname":"from-the-signal-server"`,
			"/api/activity/S2": `"hostname":"S2"`,
			"/api/activity/S3": `"hostname":"S3"`,
			"/api/activity":    `"device_id":"S1","hostname":"from-the-signal-server"`,
		} {
			if got := w.Do(replay.Request{Role: "admin", Path: path}); got.Status != 200 || !strings.Contains(got.Body, want) {
				t.Errorf("GET %s: %d %s, want %s", path, got.Status, got.Body, want)
			}
		}
		want := `[{"id":"S1","info":"{\"hostname\":\"from-the-signal-server\",\"os\":\"Linux\",\"platform\":\"Linux\",\"version\":\"1.4.2\"}","online":1}]`
		rows := w.Rows(`SELECT id, `+db.Pick("info", "info::text AS info")+`, CASE WHEN status_online`+db.Pick(" = 1", "")+` THEN 1 ELSE 0 END AS online FROM peer ORDER BY id`, nil)
		if db.IsPostgres() {
			// A JSON column prints its members by length, then name
			want = `[{"id":"S1","info":"{\"os\": \"Linux\", \"version\": \"1.4.2\", \"hostname\": \"from-the-signal-server\", \"platform\": \"Linux\"}","online":1}]`
		}
		if rows != want {
			t.Errorf("the console's table after the lookups: %s\nwant %s", rows, want)
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
