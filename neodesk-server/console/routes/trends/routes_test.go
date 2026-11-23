package trends_test

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
)

// answer is what the route sends.
type answer struct {
	Success bool `json:"success"`
	Data    struct {
		Days     int   `json:"days"`
		Since    int64 `json:"since"`
		Sessions []struct {
			Start int64  `json:"start"`
			End   *int64 `json:"end"`
		} `json:"sessions"`
		Help []struct {
			Created *int64 `json:"created"`
			Updated *int64 `json:"updated"`
			Status  string `json:"status"`
		} `json:"help"`
		DevicesChecked int `json:"devicesChecked"`
		DevicesTotal   int `json:"devicesTotal"`
	} `json:"data"`
	GeneratedAt int64 `json:"generatedAt"`
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// sessionsTable makes the signal server's table of remote sessions in the
// console's database as the server makes it on SQLite, with the rows
// given: host, start, end ("" while it runs).
func sessionsTable(t *testing.T, w *routetest.World, rows ...[3]string) {
	t.Helper()
	ctx := context.Background()
	if _, err := w.DB.Exec(ctx, `CREATE TABLE peer_connection_sessions (id INTEGER PRIMARY KEY AUTOINCREMENT, host_id TEXT NOT NULL,
		peer_id TEXT NOT NULL DEFAULT '', started_at TEXT NOT NULL DEFAULT (datetime('now')), ended_at TEXT)`); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		var ended any
		if r[2] != "" {
			ended = r[2]
		}
		if _, err := w.DB.Exec(ctx, `INSERT INTO peer_connection_sessions (host_id, started_at, ended_at) VALUES (?, ?, ?)`, r[0], r[1], ended); err != nil {
			t.Fatal(err)
		}
	}
}

// stored is a time as the signal server stores it on SQLite.
func stored(t time.Time) string { return t.UTC().Format(time.DateTime) }

// TestTrends: the sessions of every listed device come from one query of
// the signal server's table, not from the API server device by device
// (BUGS L103); the help requests from the API server.
func TestTrends(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	started, ended := now.Add(-2*time.Hour), now.Add(-time.Hour)
	old := now.Add(-20 * 24 * time.Hour)

	api := routetest.NewAPI()
	api.On("GET /api/peers", 200, `[{"id":"a"},{"id":"b"}]`)
	api.On("GET /api/help/requests?limit=5000", 200, `{"requests":[{"id":1,"status":"pending","created_at":"`+stamp(started)+`","updated_at":"`+stamp(ended)+`"}]}`)
	w := routetest.New(t, routetest.Options{API: api})
	sessionsTable(t, w, [3]string{"a", stored(started), stored(ended)}, [3]string{"a", stored(old), ""},
		[3]string{"b", stored(ended), ""}, [3]string{"gone", stored(ended), ""})

	if got := w.SignedOut().Do("GET", "/api/reports/trends", ""); got.Status != 401 || got.Body != `{"success":false,"error":"Unauthorized. Please log in.","code":"session_required"}` {
		t.Errorf("signed out: %d %s", got.Status, got.Body)
	}
	// The reports section is closed to guest_issuer before the route's own check
	if got := w.As("guest_issuer").Do("GET", "/api/reports/trends", ""); got.Status != 403 {
		t.Errorf("guest_issuer: %d %s", got.Status, got.Body)
	}
	if calls := api.Calls(); calls != nil {
		t.Fatalf("the API server was asked for accounts that may not read: %v", calls)
	}

	before := time.Now().UnixMilli()
	got := w.As("viewer").Do("GET", "/api/reports/trends", "")
	after := time.Now().UnixMilli()
	if got.Status != 200 {
		t.Fatalf("viewer: %d %s", got.Status, got.Body)
	}
	var first answer
	if err := json.Unmarshal([]byte(got.Body), &first); err != nil {
		t.Fatal(err)
	}
	data := first.Data
	if !first.Success || data.Days != 14 || data.DevicesChecked != 2 || data.DevicesTotal != 2 {
		t.Errorf("answer: %s", got.Body)
	}
	if first.GeneratedAt < before || first.GeneratedAt > after || data.Since != first.GeneratedAt-14*86400000 {
		t.Errorf("generatedAt %d (asked between %d and %d), since %d", first.GeneratedAt, before, after, data.Since)
	}
	// Session 2 is too old, and the device of the last one is not listed
	if len(data.Sessions) != 2 || data.Sessions[0].Start != started.UnixMilli() || data.Sessions[0].End == nil || *data.Sessions[0].End != ended.UnixMilli() ||
		data.Sessions[1].Start != ended.UnixMilli() || data.Sessions[1].End != nil {
		t.Errorf("sessions: %s", got.Body)
	}
	if len(data.Help) != 1 || data.Help[0].Status != "pending" || data.Help[0].Created == nil || *data.Help[0].Created != started.UnixMilli() {
		t.Errorf("help: %s", got.Body)
	}
	calls := api.Calls()
	sort.Strings(calls)
	want := []string{
		"GET /api/help/requests?limit=5000",
		"GET /api/peers",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("the API server was asked %q, want %q", calls, want)
	}

	// The same number of days again comes from what was kept, for every account
	again := w.As("admin").Do("GET", "/api/reports/trends?days=14", "")
	if again.Body != got.Body {
		t.Errorf("a second request within five minutes:\n%s\nwant the first answer\n%s", again.Body, got.Body)
	}
	if calls := api.Calls(); calls != nil {
		t.Errorf("a second request within five minutes asked the API server: %v", calls)
	}

	// Another number of days is collected anew
	month := w.As("admin").Do("GET", "/api/reports/trends?days=30", "")
	var wide answer
	if err := json.Unmarshal([]byte(month.Body), &wide); err != nil {
		t.Fatal(err)
	}
	if wide.Data.Days != 30 || len(wide.Data.Sessions) != 3 {
		t.Errorf("30 days: %s", month.Body)
	}
	if calls := api.Calls(); len(calls) != 2 {
		t.Errorf("30 days asked the API server %d times: %v", len(calls), calls)
	}
}

func TestTrendsAnswers502WhenTheListCannotBeRead(t *testing.T) {
	api := routetest.NewAPI()
	api.On("GET /api/peers", 200, `[{"id":"a"},null]`)
	w := routetest.New(t, routetest.Options{API: api})
	admin := w.As("admin")
	if got := admin.Do("GET", "/api/reports/trends", ""); got.Status != 502 || got.Body != `{"success":false,"error":"Server error"}` {
		t.Errorf("a device that is null: %d %s", got.Status, got.Body)
	}
	if got := admin.Do("GET", "/api/reports/trends", "", "Accept-Language", "de"); got.Status != 502 || got.Body != `{"success":false,"error":"Serverfehler"}` {
		t.Errorf("in German: %d %s", got.Status, got.Body)
	}
	// A failure is not kept: the next request collects again
	api.On("GET /api/peers", 200, `[]`)
	if got := admin.Do("GET", "/api/reports/trends", ""); got.Status != 200 {
		t.Errorf("after the failure: %d %s", got.Status, got.Body)
	}
}

func TestTrendsNeedsAuditView(t *testing.T) {
	api := routetest.NewAPI()
	api.On("GET /api/peers", 200, `[]`)
	w := routetest.New(t, routetest.Options{API: api, Roles: routetest.Overrides{"operator": {"audit.view": false}}})
	// The reports section of the panel access guard asks audit.view (PA3, F6).
	if got := w.As("operator").Do("GET", "/api/reports/trends", ""); got.Status != 403 || got.Body != `{"success":false,"error":"Panel access denied: reports","code":"panel_access_denied"}` {
		t.Errorf("without audit.view: %d %s", got.Status, got.Body)
	}
	if calls := api.Calls(); calls != nil {
		t.Errorf("the API server was asked for an account that may not read: %v", calls)
	}
	if got := w.As("viewer").Do("GET", "/api/reports/trends", ""); got.Status != 200 {
		t.Errorf("viewer: %d %s", got.Status, got.Body)
	}
}

// TestTrendsCountOnlyDevicesInScope closes devices a and c to the viewer
// with a device group whose access list names the operator: it gets the
// sessions b reported and counts b alone, also from the result kept for
// every account, while the admin keeps seeing every device.
func TestTrendsCountOnlyDevicesInScope(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	started := now.Add(-2 * time.Hour)
	session := func(minute int, host string) [3]string {
		return [3]string{host, stored(started.Add(time.Duration(minute) * time.Minute)), ""}
	}
	api := routetest.NewAPI()
	api.On("GET /api/peers", 200, `[{"id":"a"},{"id":"b"},{"id":"c"}]`)
	w := routetest.New(t, routetest.Options{API: api})
	sessionsTable(t, w, session(1, "a"), session(2, "b"), session(3, "c"))
	ctx := context.Background()
	for _, statement := range []string{
		"INSERT INTO device_groups (id, guid, name) VALUES (90, 'closed', 'Closed')",
		"INSERT INTO device_group_members (device_group_id, peer_id) VALUES (90, 'a'), (90, 'c')",
		"INSERT INTO device_group_user_access (device_group_id, user_id) VALUES (90, 5)",
	} {
		if _, err := w.DB.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	read := func(role string) answer {
		t.Helper()
		got := w.As(role).Do("GET", "/api/reports/trends", "")
		var a answer
		if got.Status != 200 || json.Unmarshal([]byte(got.Body), &a) != nil {
			t.Fatalf("%s: %d %s", role, got.Status, got.Body)
		}
		return a
	}
	starts := func(a answer) []int64 {
		var out []int64
		for _, s := range a.Data.Sessions {
			out = append(out, (s.Start-started.UnixMilli())/60000)
		}
		return out
	}
	for i, role := range []string{"viewer", "admin", "viewer"} {
		got := read(role)
		wantSessions, wantDevices := []int64{2}, 1
		if role == "admin" {
			wantSessions, wantDevices = []int64{1, 2, 3}, 3
		}
		if !reflect.DeepEqual(starts(got), wantSessions) || got.Data.DevicesChecked != wantDevices || got.Data.DevicesTotal != wantDevices {
			t.Errorf("request %d as %s: sessions %v, checked %d, total %d; want sessions %v and %d devices",
				i+1, role, starts(got), got.Data.DevicesChecked, got.Data.DevicesTotal, wantSessions, wantDevices)
		}
	}
}
