package registrations

import (
	"context"
	"math"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := httpx.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

var (
	sqliteNow = regexp.MustCompile(`"20[2-9][0-9]-[0-9]{2}-[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2}"`)
	isoNow    = regexp.MustCompile(`"20[2-9][0-9]-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}\.[0-9]{3}Z"`)
)

// shape replaces the times of the run by the form each database's driver
// gave Node: text of the second on SQLite, ISO with milliseconds on
// PostgreSQL.
func shape(s string) string {
	s = sqliteNow.ReplaceAllString(s, `"<sqlite time>"`)
	return isoNow.ReplaceAllString(s, `"<iso time>"`)
}

func nowForm(db *store.DB) string {
	if db.IsPostgres() {
		return `"<iso time>"`
	}
	return `"<sqlite time>"`
}

// TestCreateApproveRejectDelete walks a registration through the
// adapter methods: the row's keys in table order with the value types of
// each database, asking again while pending, approved and rejected, and
// what Approve and Reject return for a row that is no longer pending.
func TestCreateApproveRejectDelete(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		s := New(db, nil)
		now := nowForm(db)
		row, err := s.Create(ctx, Request{DeviceID: "dev-1", Hostname: "pc", Platform: "Linux", Version: "1", IPAddress: "10.0.0.1", PublicKey: "k", UUID: "u"})
		if err != nil {
			t.Fatal(err)
		}
		want := `{"id":1,"device_id":"dev-1","hostname":"pc","platform":"Linux","version":"1","ip_address":"10.0.0.1","public_key":"k","uuid":"u","status":"pending","approved_by":null,"approved_at":null,"rejected_reason":"","access_token":null,"console_url":null,"server_address":null,"server_key":null,"created_at":` + now + `,"updated_at":` + now + `}`
		if got := shape(mustJSON(t, row)); got != want {
			t.Fatalf("created:\n got %s\nwant %s", got, want)
		}
		// Asking again while pending changes the fields, keeps the id
		row, err = s.Create(ctx, Request{DeviceID: "dev-1", Hostname: "pc-2"})
		if err != nil {
			t.Fatal(err)
		}
		if row.Get("id") != float64(1) || row.Get("hostname") != "pc-2" || row.Get("public_key") != "" {
			t.Fatalf("asked again: %s", mustJSON(t, row))
		}
		id := jsbind.Number(1)
		approved, err := s.Approve(ctx, id, "", ServerConfig{ConsoleURL: "http://h:5000", ServerKey: "key"})
		if err != nil {
			t.Fatal(err)
		}
		if approved.Get("status") != "approved" || approved.Get("approved_by") != "admin" || approved.Get("access_token") != nil ||
			approved.Get("server_address") != nil || approved.Get("console_url") != "http://h:5000" || approved.Get("server_key") != "key" {
			t.Fatalf("approved: %s", mustJSON(t, approved))
		}
		// An approved row is not approved again; SQLite reads it back as it
		// is, PostgreSQL's UPDATE ... RETURNING finds nothing
		again, err := s.Approve(ctx, id, "x", ServerConfig{})
		if err != nil {
			t.Fatal(err)
		}
		if db.IsPostgres() != (again == nil) {
			t.Fatalf("approving an approved row on %s: %s", db.Dialect(), mustJSON(t, again))
		}
		if again != nil && again.Get("approved_by") != "admin" {
			t.Fatalf("approving again changed the row: %s", mustJSON(t, again))
		}
		rejectedApproved, err := s.Reject(ctx, id, "late")
		if err != nil {
			t.Fatal(err)
		}
		if db.IsPostgres() != (rejectedApproved == nil) || (rejectedApproved != nil && rejectedApproved.Get("status") != "approved") {
			t.Fatalf("rejecting an approved row: %s", mustJSON(t, rejectedApproved))
		}
		// An approved device asking again gets its row unchanged
		same, err := s.Create(ctx, Request{DeviceID: "dev-1", Hostname: "intruder"})
		if err != nil {
			t.Fatal(err)
		}
		if same.Get("hostname") != "pc-2" || same.Get("status") != "approved" {
			t.Fatalf("approved device asked again: %s", mustJSON(t, same))
		}

		second, err := s.Create(ctx, Request{DeviceID: "dev-2"})
		if err != nil {
			t.Fatal(err)
		}
		id2 := jsbind.Number(second.Get("id").(float64))
		rejected, err := s.Reject(ctx, id2, "no")
		if err != nil {
			t.Fatal(err)
		}
		if rejected.Get("status") != "rejected" || rejected.Get("rejected_reason") != "no" {
			t.Fatalf("rejected: %s", mustJSON(t, rejected))
		}
		// A rejected device asking again is pending again, without the reason
		reopened, err := s.Create(ctx, Request{DeviceID: "dev-2", Hostname: "again"})
		if err != nil {
			t.Fatal(err)
		}
		if reopened.Get("status") != "pending" || reopened.Get("rejected_reason") != "" || reopened.Get("id") != second.Get("id") {
			t.Fatalf("reopened: %s", mustJSON(t, reopened))
		}
		if n, err := s.PendingCount(ctx); err != nil || n != 1 {
			t.Fatalf("pending count %v %v", n, err)
		}
		gone, err := s.Delete(ctx, id2)
		if err != nil || !gone {
			t.Fatalf("delete: %v %v", gone, err)
		}
		gone, err = s.Delete(ctx, id2)
		if err != nil || gone {
			t.Fatalf("delete again: %v %v", gone, err)
		}
		if n, err := s.PendingCount(ctx); err != nil || n != 0 {
			t.Fatalf("pending count after delete %v %v", n, err)
		}
	})
}

// TestByDeviceIDAndBinding checks the lookups and how each database's
// driver bound values the requests carry: a list spread over the
// placeholders on SQLite, every value as text on PostgreSQL, an id that
// is no integer an error on PostgreSQL and nothing on SQLite.
func TestByDeviceIDAndBinding(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		s := New(db, nil)
		if _, err := s.Create(ctx, Request{DeviceID: "dev-1", PublicKey: "pk"}); err != nil {
			t.Fatal(err)
		}
		row, err := s.ByDeviceID(ctx, "dev-1")
		if err != nil || row == nil || row.Get("public_key") != "pk" || row.Get("status") != "pending" {
			t.Fatalf("by device id: %v %v", row, err)
		}
		if row, err := s.ByDeviceID(ctx, "DEV-1"); err != nil || row != nil {
			t.Fatalf("another letter case: %v %v", row, err)
		}
		list := jsbind.Query("device_id=dev-1&device_id=x", "device_id")
		row, err = s.ByDeviceIDValue(ctx, list)
		if db.IsPostgres() {
			if err != nil || row != nil {
				t.Fatalf("a list on PostgreSQL: %v %v", row, err)
			}
		} else if err == nil {
			t.Fatalf("a list of two on SQLite: %v", row)
		}
		one := jsbind.Query("device_id[]=dev-1", "device_id")
		row, err = s.ByDeviceIDValue(ctx, one)
		if db.IsPostgres() {
			if err != nil || row != nil {
				t.Fatalf("a list of one on PostgreSQL: %v %v", row, err)
			}
		} else if err != nil || row == nil {
			t.Fatalf("a list of one on SQLite: %v %v", row, err)
		}
		for _, n := range []float64{math.NaN(), math.Inf(1), 1e21, 99999999999} {
			row, err := s.ByID(ctx, jsbind.Number(n))
			if db.IsPostgres() != (err != nil) || row != nil {
				t.Errorf("id %v on %s: %v %v", n, db.Dialect(), row, err)
			}
		}
		if row, err := s.ByID(ctx, jsbind.Number(1)); err != nil || row == nil {
			t.Fatalf("id 1: %v %v", row, err)
		}
	})
}

// TestListFilters checks the status filter, the search in device id,
// host name and address with % and _ as wildcards, and the letter case
// rule of each database: LIKE folds ASCII only, ILIKE folds É too.
func TestListFilters(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		s := New(db, nil)
		for _, r := range []Request{
			{DeviceID: "aaa", Hostname: "École", IPAddress: "10.0.0.1"},
			{DeviceID: "bbb", Hostname: "lab_pc", IPAddress: "192.168.1.2"},
			{DeviceID: "ccc", Hostname: "100% off", IPAddress: "10.0.0.3"},
		} {
			if _, err := s.Create(ctx, r); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.Reject(ctx, jsbind.Number(3), ""); err != nil {
			t.Fatal(err)
		}
		// Distinct times, newest last created
		if _, err := db.Exec(ctx, db.Pick(
			"UPDATE pending_registrations SET created_at = datetime('2001-01-01', '+' || id || ' minutes')",
			"UPDATE pending_registrations SET created_at = TIMESTAMPTZ '2001-01-01 00:00:00+00' + id * INTERVAL '1 minute'")); err != nil {
			t.Fatal(err)
		}
		ids := func(f Filters) string {
			rows, err := s.List(ctx, f)
			if err != nil {
				return "error"
			}
			var out []string
			for _, r := range rows {
				out = append(out, r.Get("device_id").(string))
			}
			return strings.Join(out, ",")
		}
		empty := jsbind.String("")
		cases := []struct {
			name           string
			filters        Filters
			sqlite, postgr string
		}{
			{"all, newest first", Filters{Status: empty, Search: empty}, "ccc,bbb,aaa", "ccc,bbb,aaa"},
			{"pending", Filters{Status: jsbind.String("pending")}, "bbb,aaa", "bbb,aaa"},
			{"rejected", Filters{Status: jsbind.String("rejected")}, "ccc", "ccc"},
			{"search in the address", Filters{Search: jsbind.String("10.0.0")}, "ccc,aaa", "ccc,aaa"},
			{"underscore is a wildcard", Filters{Search: jsbind.String("b_pc")}, "bbb", "bbb"},
			{"percent is a wildcard", Filters{Search: jsbind.String("1%f")}, "ccc", "ccc"},
			{"ASCII letter case", Filters{Search: jsbind.String("LAB")}, "bbb", "bbb"},
			{"other letter case", Filters{Search: jsbind.String("école")}, "", "aaa"},
			{"status and search", Filters{Status: jsbind.String("pending"), Search: jsbind.String("1")}, "bbb,aaa", "bbb,aaa"},
			{"status list", Filters{Status: jsbind.Query("status=pending&status=x", "status")}, "error", ""},
		}
		for _, c := range cases {
			want := c.sqlite
			if db.IsPostgres() {
				want = c.postgr
			}
			if got := ids(c.filters); got != want {
				t.Errorf("%s: got %q, want %q", c.name, got, want)
			}
		}
	})
}

// TestNotificationsFromBothSources reads the pending LAN rows of the
// database and the API server's queue: newest first, a failing API
// server leaves the LAN entries.
func TestNotificationsFromBothSources(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		failing := false
		api := goapi.New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/enrollment/pending" {
				http.NotFound(w, r)
				return
			}
			if failing {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"devices":[{"device_id":"enr-1","hostname":"E","created_at":"2001-01-01T00:01:30Z"}],"count":1}`))
		}))
		s := New(db, api).WithClock(func() time.Time { return time.UnixMilli(0) }, time.UTC)
		for _, id := range []string{"lan-1", "lan-2", "lan-3"} {
			if _, err := s.Create(ctx, Request{DeviceID: id, IPAddress: "10.0.0.9"}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.Reject(ctx, jsbind.Number(3), ""); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, db.Pick(
			"UPDATE pending_registrations SET created_at = datetime('2001-01-01', '+' || id || ' minutes')",
			"UPDATE pending_registrations SET created_at = TIMESTAMPTZ '2001-01-01 00:00:00+00' + id * INTERVAL '1 minute'")); err != nil {
			t.Fatal(err)
		}
		ids := func() string {
			var out []string
			for _, item := range s.Notifications(ctx) {
				out = append(out, item.Get("id").(string)+"@"+item.Get("created_at").(string))
			}
			return strings.Join(out, " ")
		}
		want := "registration:lan:2@2001-01-01T00:02:00.000Z registration:enrollment:enr-1@2001-01-01T00:01:30.000Z registration:lan:1@2001-01-01T00:01:00.000Z"
		if got := ids(); got != want {
			t.Fatalf("both sources:\n got %s\nwant %s", got, want)
		}
		failing = true
		if got := ids(); got != "registration:lan:2@2001-01-01T00:02:00.000Z registration:lan:1@2001-01-01T00:01:00.000Z" {
			t.Fatalf("API server failing: %s", got)
		}
		first := s.Notifications(ctx)[0]
		if got := mustJSON(t, first); got != `{"id":"registration:lan:2","title":"lan-2","message":"lan-2 · 10.0.0.9","icon":"how_to_reg","link":"/registrations","read":false,"created_at":"2001-01-01T00:02:00.000Z","kind":"registration","source":"lan","device_id":"lan-2","hostname":"","platform":"","ip":"10.0.0.9","status":"pending"}` {
			t.Fatalf("entry: %s", got)
		}
	})
}
