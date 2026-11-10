package trends_test

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/routes/internal/routetest"
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

func TestTrends(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	started, ended := now.Add(-2*time.Hour), now.Add(-time.Hour)
	old := now.Add(-20 * 24 * time.Hour)

	api := routetest.NewAPI()
	api.On("GET /api/peers", 200, `[{"id":"a"},{"id":"b"}]`)
	api.On("GET /api/peers/a/connection-sessions?limit=500", 200, `{"peer_id":"a","count":2,"sessions":[`+
		`{"id":1,"host_id":"a","peer_id":"b","started_at":"`+stamp(started)+`","ended_at":"`+stamp(ended)+`"},`+
		`{"id":2,"host_id":"a","peer_id":"b","started_at":"`+stamp(old)+`"}]}`)
	api.On("GET /api/peers/b/connection-sessions?limit=500", 200, `{"peer_id":"b","count":2,"sessions":[`+
		`{"id":1,"host_id":"a","peer_id":"b","started_at":"`+stamp(started)+`"},`+
		`{"id":3,"host_id":"b","peer_id":"a","started_at":"`+stamp(ended)+`"}]}`)
	api.On("GET /api/help/requests?limit=5000", 200, `{"requests":[{"id":1,"status":"pending","created_at":"`+stamp(started)+`","updated_at":"`+stamp(ended)+`"}]}`)
	w := routetest.New(t, routetest.Options{API: api})

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
	// Session 1 is reported by both devices and counts once; session 2 is too old
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
		"GET /api/peers/a/connection-sessions?limit=500",
		"GET /api/peers/b/connection-sessions?limit=500",
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
	if calls := api.Calls(); len(calls) != 4 {
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
	if got := w.As("operator").Do("GET", "/api/reports/trends", ""); got.Status != 403 || got.Body != `{"success":false,"error":"Permission denied: audit.view"}` {
		t.Errorf("without audit.view: %d %s", got.Status, got.Body)
	}
	if calls := api.Calls(); calls != nil {
		t.Errorf("the API server was asked for an account that may not read: %v", calls)
	}
	if got := w.As("viewer").Do("GET", "/api/reports/trends", ""); got.Status != 200 {
		t.Errorf("viewer: %d %s", got.Status, got.Body)
	}
}
