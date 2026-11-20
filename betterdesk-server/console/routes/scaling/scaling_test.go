package scaling

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/middleware"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

const (
	signedOut = `{"success":false,"error":"Unauthorized. Please log in.","code":"session_required"}`
	relays    = "/api/panel/scaling/relays"
	rules     = "/api/panel/scaling/rules"
)

// clock is a clock that moves a millisecond with every look.
type clock struct{ at time.Time }

func (c *clock) now() time.Time {
	c.at = c.at.Add(time.Millisecond)
	return c.at
}

// each runs a test on SQLite and PostgreSQL with a site whose ids and
// times are fixed: ids count up from r-0000000000000001.
func each(t *testing.T, fn func(t *testing.T, s *site, db *store.DB)) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		var ids bytes.Buffer
		for i := 1; i <= 400; i++ {
			ids.Write([]byte{0, 0, 0, 0, 0, 0, byte(i >> 8), byte(i)})
		}
		c := &clock{at: time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)}
		fn(t, newSite(t, db, Options{Now: c.now, Random: &ids}), db)
	})
}

func stored(t *testing.T, db *store.DB, key string) string {
	t.Helper()
	value, _, err := getSetting(context.Background(), db, key)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestRoutesInTheOrderOfTheOldFileAtBothAddresses(t *testing.T) {
	s := newSite(t, storetest.Migrated(t), Options{})
	var got []string
	for _, route := range s.d.Router.Routes() {
		if strings.HasPrefix(route.Path, "/api/") {
			got = append(got, route.Method+" "+route.Path)
		}
	}
	first := []string{
		"GET /api/panel/scaling/relays", "GET /api/panel/scaling/relays/{nodeId}", "POST /api/panel/scaling/relays",
		"PUT /api/panel/scaling/relays/{nodeId}", "DELETE /api/panel/scaling/relays/{nodeId}",
		"GET /api/panel/scaling/relays/{nodeId}/health", "GET /api/panel/scaling/relays/{nodeId}/metrics",
		"GET /api/panel/scaling/capacity", "GET /api/panel/scaling/rules", "POST /api/panel/scaling/rules",
		"PUT /api/panel/scaling/rules/{ruleId}", "DELETE /api/panel/scaling/rules/{ruleId}", "POST /api/bd/scaling/relay-heartbeat",
	}
	want := append([]string{}, first...)
	for _, route := range first {
		method, path, _ := strings.Cut(route, " ")
		want = append(want, method+" /api/bd"+path)
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("routes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestGuards(t *testing.T) {
	each(t, func(t *testing.T, s *site, db *store.DB) {
		writes := []struct{ method, path string }{
			{"GET", relays}, {"GET", relays + "/r-1"}, {"POST", relays}, {"PUT", relays + "/r-1"}, {"DELETE", relays + "/r-1"},
			{"GET", relays + "/r-1/health"}, {"GET", relays + "/r-1/metrics"}, {"GET", "/api/panel/scaling/capacity"},
			{"GET", rules}, {"POST", rules}, {"PUT", rules + "/r-1"}, {"DELETE", rules + "/r-1"},
		}
		viewer, global := s.signIn("viewer"), s.signIn("global_admin")
		for _, prefix := range []string{"", "/api/bd"} {
			for _, w := range writes {
				body := ""
				if w.method == "POST" || w.method == "PUT" {
					body = `{"name":"x","address":"relay.example:1","match_type":"tag","match_value":"v"}`
				}
				if status, answer := s.send("", w.method, prefix+w.path, body); status != 401 || answer != signedOut {
					t.Errorf("%s %s signed out: %d %s", w.method, prefix+w.path, status, answer)
				}
				for role, cookie := range map[string]string{"viewer": viewer, "global_admin": global} {
					if status, answer := s.send(cookie, w.method, prefix+w.path, body); status != 403 || answer != `{"success":false,"error":"Permission denied: server.config"}` {
						t.Errorf("%s %s as %s: %d %s", w.method, prefix+w.path, role, status, answer)
					}
				}
			}
		}
		if stored(t, db, keyRelays) != "" || stored(t, db, keyRules) != "" {
			t.Error("a refused request stored something")
		}
		// server_admin holds server.config
		if status, answer := s.send(s.signIn("server_admin"), "GET", relays, ""); status != 200 || answer != `{"data":[]}` {
			t.Errorf("list as server_admin: %d %s", status, answer)
		}
	})
}

func TestHeartbeatAnswersAnyoneAndStoresNothing(t *testing.T) {
	each(t, func(t *testing.T, s *site, db *store.DB) {
		const want = `{"ok":true,"note":"relay heartbeat acknowledged but not stored"}`
		for _, path := range []string{"/api/bd/scaling/relay-heartbeat", "/api/bd/api/bd/scaling/relay-heartbeat", "/API/BD/Scaling/Relay-Heartbeat/"} {
			for _, cookie := range []string{"", s.signIn("viewer")} {
				if status, answer := s.send(cookie, "POST", path, `{"relay":"r-1","sessions":3}`); status != 202 || answer != want {
					t.Errorf("%s: %d %s", path, status, answer)
				}
			}
		}
		if status, _ := s.send("", "GET", "/api/bd/scaling/relay-heartbeat", ""); status != 404 {
			t.Errorf("GET: %d, want no route", status)
		}
		var rows int
		if err := db.QueryRow(context.Background(), "SELECT COUNT(*) FROM settings WHERE key LIKE 'scaling%'").Scan(&rows); err != nil || rows != 0 {
			t.Errorf("%d settings rows after heartbeats (%v)", rows, err)
		}
	})
}

func TestRelays(t *testing.T) {
	each(t, func(t *testing.T, s *site, db *store.DB) {
		admin := s.signIn("admin")
		check := func(name, method, path, body string, status int, want string) {
			t.Helper()
			if got, answer := s.send(admin, method, path, body); got != status || answer != want {
				t.Errorf("%s\n got  %d %s\n want %d %s", name, got, answer, status, want)
			}
		}
		const first = `{"id":"r-0000000000000001","created_at":"2026-11-10T12:00:00.001Z","name":"Edge 1","address":"relay.example.com:21117","location":"Lab","max_sessions":10,"max_bandwidth_mbps":20,"updated_at":"2026-11-10T12:00:00.002Z","connected_devices":0,"active_sessions":0,"bandwidth_mbps":0,"cpu":0,"memory":0,"status":"unknown"}`
		check("empty list", "GET", relays, "", 200, `{"data":[]}`)
		check("empty capacity", "GET", "/api/panel/scaling/capacity", "", 200, `{"data":{"relay_count":0,"max_devices":500,"max_sessions":50,"max_bandwidth_mbps":100}}`)
		check("create", "POST", relays, `{"name":" Edge 1 ","address":" relay.example.com:21117 ","location":" Lab ","max_sessions":"10 seats","max_bandwidth_mbps":20.9,"status":"online","cpu":99}`, 201, `{"data":`+first+`}`)
		if got := stored(t, db, keyRelays); got != "["+first+"]" {
			t.Errorf("stored: %s", got)
		}
		check("read", "GET", relays+"/r-0000000000000001", "", 200, `{"data":`+first+`}`)
		check("read at the second address", "GET", "/api/bd"+relays+"/r-0000000000000001", "", 200, `{"data":`+first+`}`)
		check("read unknown", "GET", relays+"/r-0000000000000009", "", 404, `{"error":"not found"}`)
		check("list", "GET", relays, "", 200, `{"data":[`+first+`]}`)
		check("same address", "POST", relays, `{"name":"Twin","address":"relay.example.com:21117"}`, 409, `{"error":"a relay with this address already exists"}`)
		check("health", "GET", relays+"/r-0000000000000001/health", "", 200, `{"data":null,"note":"live relay telemetry not yet available"}`)
		check("health of an unknown relay", "GET", relays+"/nope/health", "", 200, `{"data":null,"note":"live relay telemetry not yet available"}`)
		check("metrics", "GET", relays+"/r-0000000000000001/metrics", "", 200, `{"data":[],"note":"live relay telemetry not yet available"}`)

		// Defaults: 50 sessions and 100 Mbit/s when the body gives no number
		const second = `{"id":"r-0000000000000002","created_at":"2026-11-10T12:00:00.003Z","name":"Edge 2","address":"[2001:db8::7]:443","location":"","max_sessions":50,"max_bandwidth_mbps":100,"updated_at":"2026-11-10T12:00:00.004Z","connected_devices":0,"active_sessions":0,"bandwidth_mbps":0,"cpu":0,"memory":0,"status":"unknown"}`
		check("create with defaults", "POST", "/api/bd"+relays, `{"name":"Edge 2","address":"[2001:db8::7]:443","max_sessions":"many","max_bandwidth_mbps":null}`, 201, `{"data":`+second+`}`)
		check("capacity", "GET", "/api/panel/scaling/capacity", "", 200, `{"data":{"relay_count":2,"max_devices":900,"max_sessions":60,"max_bandwidth_mbps":120}}`)

		// An update keeps id and time of creation and replaces the rest
		const updated = `{"id":"r-0000000000000001","created_at":"2026-11-10T12:00:00.001Z","name":"Edge (EU) #1","address":"10.0.0.5:21118","location":"","max_sessions":250,"max_bandwidth_mbps":100,"updated_at":"2026-11-10T12:00:00.005Z","connected_devices":0,"active_sessions":0,"bandwidth_mbps":0,"cpu":0,"memory":0,"status":"unknown"}`
		check("update", "PUT", relays+"/r-0000000000000001", `{"name":"Edge (EU) #1","address":"10.0.0.5:21118","max_sessions":250}`, 200, `{"data":`+updated+`}`)
		check("update to a taken address", "PUT", relays+"/r-0000000000000002", `{"name":"Edge 2","address":"10.0.0.5:21118"}`, 409, `{"error":"another relay already uses this address"}`)
		check("update keeping its own address", "PUT", relays+"/r-0000000000000002", `{"name":"Edge 2b","address":"[2001:db8::7]:443"}`, 200, `{"data":`+strings.NewReplacer(`"Edge 2"`, `"Edge 2b"`, "00.004Z", "00.006Z").Replace(second)+`}`)
		check("update unknown", "PUT", relays+"/r-0000000000000009", `{"name":"x","address":"a:1"}`, 404, `{"error":"not found"}`)
		if got := stored(t, db, keyRelays); !strings.HasPrefix(got, "["+updated+`,{"id":"r-0000000000000002"`) {
			t.Errorf("stored after updates: %s", got)
		}

		check("delete", "DELETE", relays+"/r-0000000000000001", "", 200, `{"ok":true}`)
		check("delete again", "DELETE", relays+"/r-0000000000000001", "", 404, `{"error":"not found"}`)
		check("delete at the second address", "DELETE", "/api/bd"+relays+"/r-0000000000000002", "", 200, `{"ok":true}`)
		if got := stored(t, db, keyRelays); got != "[]" {
			t.Errorf("stored after deletes: %s", got)
		}
	})
}

// TestAWriteKeepsTheListWhenItCannotBeRead: when a stored list cannot be
// read, the reads answer 500 and the writes refuse with 500 and leave the
// stored value as it is, where they stored a list of only the new entry
// (BUGS.md SC14).
func TestAWriteKeepsTheListWhenItCannotBeRead(t *testing.T) {
	each(t, func(t *testing.T, s *site, db *store.DB) {
		ctx := context.Background()
		admin := s.signIn("admin")
		const broken = `[{"id":"r-a","address":"a:1"},`
		for _, key := range []string{keyRelays, keyRules} {
			if err := setSetting(ctx, db, key, broken); err != nil {
				t.Fatal(err)
			}
		}
		for _, c := range []struct{ method, path, body, want string }{
			{"GET", relays, "", `500 {"error":"failed to load relay nodes"}`},
			{"GET", relays + "/r-a", "", `500 {"error":"failed to load relay node"}`},
			{"POST", relays, `{"name":"x","address":"c:1"}`, `500 {"error":"failed to save relay node"}`},
			{"PUT", relays + "/r-a", `{"name":"x","address":"c:1"}`, `500 {"error":"failed to update relay node"}`},
			{"DELETE", relays + "/r-a", "", `500 {"error":"failed to delete relay node"}`},
			{"GET", "/api/panel/scaling/capacity", "", `500 {"error":"failed to compute capacity"}`},
			{"GET", rules, "", `500 {"error":"failed to load rules"}`},
			{"POST", rules, `{"name":"x","match_type":"tag","match_value":"y"}`, `500 {"error":"failed to save rule"}`},
			{"PUT", rules + "/r-a", `{"name":"x","match_type":"tag","match_value":"y"}`, `500 {"error":"failed to update rule"}`},
			{"DELETE", rules + "/r-a", "", `500 {"error":"failed to delete rule"}`},
		} {
			if status, answer := s.send(admin, c.method, c.path, c.body); fmt.Sprintf("%d %s", status, answer) != c.want {
				t.Errorf("%s %s: %d %s, want %s", c.method, c.path, status, answer, c.want)
			}
		}
		for _, key := range []string{keyRelays, keyRules} {
			if got := stored(t, db, key); got != broken {
				t.Errorf("%s after the writes: %s", key, got)
			}
		}
	})
}

func TestRelayValidation(t *testing.T) {
	each(t, func(t *testing.T, s *site, db *store.DB) {
		admin := s.signIn("admin")
		long := strings.Repeat("n", 81)
		cases := []struct{ body, want string }{
			{`{}`, "name must be 1-80 characters"},
			{`[{"name":"x","address":"a:1"}]`, "name must be 1-80 characters"},
			{`{"name":"","address":"a:1"}`, "name must be 1-80 characters"},
			{`{"name":"   ","address":"a:1"}`, "name must be 1-80 characters"},
			{`{"name":0,"address":"a:1"}`, "name must be 1-80 characters"},
			{`{"name":"` + long + `","address":"a:1"}`, "name must be 1-80 characters"},
			{`{"name":"<b>x</b>","address":"a:1"}`, "name contains invalid characters"},
			{`{"name":"a;b","address":"a:1"}`, "name contains invalid characters"},
			{`{"name":"a\"b","address":"a:1"}`, "name contains invalid characters"},
			{`{"name":{"a":1},"address":"a:1"}`, "name contains invalid characters"},
			{`{"name":"x"}`, "address must be host:port (e.g. relay.example.com:21117)"},
			{`{"name":"x","address":"relay.example.com"}`, "address must be host:port (e.g. relay.example.com:21117)"},
			{`{"name":"x","address":"relay.example.com:21117/x"}`, "address must be host:port (e.g. relay.example.com:21117)"},
			{`{"name":"x","address":"relay example:1"}`, "address must be host:port (e.g. relay.example.com:21117)"},
			{`{"name":"x","address":"a:123456"}`, "address must be host:port (e.g. relay.example.com:21117)"},
			{`{"name":"x","address":":1"}`, "address must be host:port (e.g. relay.example.com:21117)"},
			{`{"name":"x","address":"a:0"}`, "invalid port"},
			{`{"name":"x","address":"a:65536"}`, "invalid port"},
			{`{"name":"x","address":"a:1","location":"` + long + `"}`, "location too long"},
			{`{"name":"x","address":"a:1","max_sessions":0}`, "max_sessions out of range"},
			{`{"name":"x","address":"a:1","max_sessions":"-4"}`, "max_sessions out of range"},
			{`{"name":"x","address":"a:1","max_sessions":100001}`, "max_sessions out of range"},
			{`{"name":"x","address":"a:1","max_bandwidth_mbps":0.5}`, "max_bandwidth_mbps out of range"},
			{`{"name":"x","address":"a:1","max_bandwidth_mbps":"1000001"}`, "max_bandwidth_mbps out of range"},
		}
		for _, c := range cases {
			for _, target := range []struct{ method, path string }{{"POST", relays}, {"PUT", relays + "/r-1"}} {
				if status, answer := s.send(admin, target.method, target.path, c.body); status != 400 || answer != `{"error":"`+c.want+`"}` {
					t.Errorf("%s %s: %d %s, want 400 %s", target.method, clip(c.body), status, answer, c.want)
				}
			}
		}
		if got := stored(t, db, keyRelays); got != "" {
			t.Errorf("a refused relay was stored: %s", got)
		}
		// What passes: the limits themselves, names in any script, and
		// values of other types as JavaScript writes them
		accepted := []string{
			`{"name":"` + strings.Repeat("n", 80) + `","address":"a:1","location":"` + strings.Repeat("l", 80) + `","max_sessions":1,"max_bandwidth_mbps":1}`,
			`{"name":"中继 Rélais ١ (a/b) #2_x-y.z","address":"A-b_c.d:65535","max_sessions":100000,"max_bandwidth_mbps":1000000}`,
			`{"name":7,"address":"[::1]:1"}`,
			`{"name":true,"address":"x:2","location":["a","b"]}`,
		}
		for _, body := range accepted {
			if status, answer := s.send(admin, "POST", relays, body); status != 201 {
				t.Errorf("%s: %d %s", clip(body), status, answer)
			}
		}
		got := stored(t, db, keyRelays)
		for _, want := range []string{`"name":"7","address":"[::1]:1"`, `"name":"true","address":"x:2","location":"a,b"`, `"name":"中继 Rélais ١ (a/b) #2_x-y.z"`} {
			if !strings.Contains(got, want) {
				t.Errorf("stored %s lacks %s", got, want)
			}
		}
	})
}

func clip(s string) string {
	if len(s) > 160 {
		return s[:160] + "..."
	}
	return s
}

func TestRelayLimitAndStoredOddities(t *testing.T) {
	each(t, func(t *testing.T, s *site, db *store.DB) {
		ctx := context.Background()
		admin := s.signIn("admin")
		var full []string
		for i := 0; i < maxRelays; i++ {
			full = append(full, fmt.Sprintf(`{"id":"old-%d","address":"h:%d","max_sessions":1,"max_bandwidth_mbps":2}`, i, i+1))
		}
		if err := setSetting(ctx, db, keyRelays, "["+strings.Join(full, ",")+"]"); err != nil {
			t.Fatal(err)
		}
		if status, answer := s.send(admin, "POST", relays, `{"name":"x","address":"new:1"}`); status != 400 || answer != `{"error":"too many relay entries (max 100)"}` {
			t.Errorf("101st relay: %d %s", status, answer)
		}
		if status, answer := s.send(admin, "GET", "/api/panel/scaling/capacity", ""); status != 200 || answer != `{"data":{"relay_count":100,"max_devices":20500,"max_sessions":100,"max_bandwidth_mbps":200}}` {
			t.Errorf("capacity of 100: %d %s", status, answer)
		}

		// Members an older version left are kept, in their places
		if err := setSetting(ctx, db, keyRelays, `[{"region":"eu","id":"r-a","name":"Old","address":"old:1","notes":{"b":1,"a":[1.50,null]},"max_sessions":5}]`); err != nil {
			t.Fatal(err)
		}
		status, answer := s.send(admin, "PUT", relays+"/r-a", `{"name":"New","address":"old:1"}`)
		want := `{"data":{"region":"eu","id":"r-a","name":"New","address":"old:1","notes":{"b":1,"a":[1.5,null]},"max_sessions":50,"location":"","max_bandwidth_mbps":100,"updated_at":"2026-11-10T12:00:00.001Z","connected_devices":0,"active_sessions":0,"bandwidth_mbps":0,"cpu":0,"memory":0,"status":"unknown"}}`
		if status != 200 || answer != want {
			t.Errorf("update of an old entry:\n got  %d %s\n want 200 %s", status, answer, want)
		}

		// An empty value is an empty list, one that is no list of relays
		// cannot be read (BUGS.md SC14)
		if err := setSetting(ctx, db, keyRelays, ""); err != nil {
			t.Fatal(err)
		}
		if status, answer := s.send(admin, "GET", relays, ""); status != 200 || answer != `{"data":[]}` {
			t.Errorf("stored \"\": %d %s", status, answer)
		}
		for _, raw := range []string{"not json", `{"id":"r-a"}`, `"text"`, "null", "[1,2", "[] x"} {
			if err := setSetting(ctx, db, keyRelays, raw); err != nil {
				t.Fatal(err)
			}
			if status, answer := s.send(admin, "GET", relays, ""); status != 500 || answer != `{"error":"failed to load relay nodes"}` {
				t.Errorf("stored %q: %d %s", raw, status, answer)
			}
		}
		// Entries that are no objects are listed and never match an id; a
		// null is skipped like them (BUGS.md SC16)
		if err := setSetting(ctx, db, keyRelays, `[1,"r-a",[2],{"id":"r-b","address":"b:1"}]`); err != nil {
			t.Fatal(err)
		}
		if status, answer := s.send(admin, "GET", relays+"/r-a", ""); status != 404 {
			t.Errorf("a text entry matched an id: %d %s", status, answer)
		}
		if status, answer := s.send(admin, "GET", relays+"/r-b", ""); status != 200 || answer != `{"data":{"id":"r-b","address":"b:1"}}` {
			t.Errorf("the object among odd entries: %d %s", status, answer)
		}
		const withNull = `[null,{"id":"r-b","address":"b:1","max_sessions":7}]`
		for _, c := range []struct{ name, method, path, body, want, stored string }{
			{"list", "GET", relays, "", `200 {"data":` + withNull + `}`, withNull},
			{"read", "GET", relays + "/r-b", "", `200 {"data":{"id":"r-b","address":"b:1","max_sessions":7}}`, withNull},
			{"create", "POST", relays, `{"name":"x","address":"c:1"}`, `201 {"data":{"id":"r-0000000000000001",`, `[null,{"id":"r-b",`},
			{"create at a taken address", "POST", relays, `{"name":"x","address":"b:1"}`, `409 {"error":"a relay with this address already exists"}`, withNull},
			{"update", "PUT", relays + "/r-b", `{"name":"x","address":"c:1"}`, `200 {"data":{"id":"r-b","address":"c:1","max_sessions":50,`, `[null,{"id":"r-b","address":"c:1",`},
			{"delete", "DELETE", relays + "/r-b", "", `200 {"ok":true}`, `[null]`},
			{"capacity", "GET", "/api/panel/scaling/capacity", "", `200 {"data":{"relay_count":1,"max_devices":700,"max_sessions":7,"max_bandwidth_mbps":100}}`, withNull},
		} {
			if err := setSetting(ctx, db, keyRelays, withNull); err != nil {
				t.Fatal(err)
			}
			if status, answer := s.send(admin, c.method, c.path, c.body); !strings.HasPrefix(fmt.Sprintf("%d %s", status, answer), c.want) {
				t.Errorf("%s with a null in the list: %d %s, want %s", c.name, status, answer, c.want)
			}
			if got := stored(t, db, keyRelays); !strings.HasPrefix(got, c.stored) {
				t.Errorf("%s with a null in the list stored %s, want %s", c.name, got, c.stored)
			}
		}
	})
}

// The cases of web-nodejs/tests/scaling.rules.enabled.test.js, and the
// rest of what the rule routes do.
func TestRules(t *testing.T) {
	each(t, func(t *testing.T, s *site, db *store.DB) {
		admin := s.signIn("admin")
		const body = `"name":"Office","match_type":"subnet","match_value":"10.0.0.0/8","target_relay":"r-1","priority":5`
		enabled := func(index int) any {
			t.Helper()
			var saved []map[string]any
			if err := json.Unmarshal([]byte(stored(t, db, keyRules)), &saved); err != nil || len(saved) <= index {
				t.Fatalf("stored rules: %s (%v)", stored(t, db, keyRules), err)
			}
			return saved[index]["enabled"]
		}

		// stores a rule created as disabled as disabled
		status, answer := s.send(admin, "POST", rules, `{`+body+`,"enabled":false}`)
		want := `{"data":{"id":"r-0000000000000001","created_at":"2026-11-10T12:00:00.001Z","name":"Office","match_type":"subnet","match_value":"10.0.0.0/8","target_relay":"r-1","fallback":"master","priority":5,"enabled":false,"updated_at":"2026-11-10T12:00:00.002Z"}}`
		if status != 201 || answer != want || enabled(0) != false {
			t.Errorf("create disabled:\n got  %d %s\n want 201 %s", status, answer, want)
		}
		// keeps a disabled rule disabled when an edit leaves the switch out
		status, answer = s.send(admin, "PUT", rules+"/r-0000000000000001", `{"name":"Office 2","match_type":"subnet","match_value":"10.0.0.0/8","target_relay":"r-1","priority":5}`)
		if status != 200 || !strings.Contains(answer, `"name":"Office 2"`) || enabled(0) != false {
			t.Errorf("edit without the switch: %d %s", status, answer)
		}
		// turns a rule on and off when the edit sends the switch
		if s.send(admin, "PUT", rules+"/r-0000000000000001", `{`+body+`,"enabled":true}`); enabled(0) != true {
			t.Error("the edit did not switch the rule on")
		}
		if s.send(admin, "PUT", "/api/bd"+rules+"/r-0000000000000001", `{`+body+`}`); enabled(0) != true {
			t.Error("an edit without the switch switched an enabled rule off")
		}
		if s.send(admin, "PUT", rules+"/r-0000000000000001", `{`+body+`,"enabled":false}`); enabled(0) != false {
			t.Error("the edit did not switch the rule off")
		}
		// Only the value false switches off
		for _, value := range []string{`"false"`, `0`, `null`, `[]`} {
			if s.send(admin, "PUT", rules+"/r-0000000000000001", `{`+body+`,"enabled":`+value+`}`); enabled(0) != true {
				t.Errorf(`"enabled":%s switched the rule off`, value)
			}
		}
		// creates a rule enabled when the request leaves the switch out
		status, answer = s.send(admin, "POST", "/api/bd"+rules, `{"name":" Guests ","match_type":" tag ","match_value":" guest ","fallback":" direct ","priority":"7th"}`)
		want = `{"data":{"id":"r-0000000000000002","created_at":"2026-11-10T12:00:00.011Z","name":"Guests","match_type":"tag","match_value":"guest","target_relay":"","fallback":"direct","priority":7,"enabled":true,"updated_at":"2026-11-10T12:00:00.012Z"}}`
		if status != 201 || answer != want || enabled(1) != true {
			t.Errorf("create without the switch:\n got  %d %s\n want 201 %s", status, answer, want)
		}
		if status, answer := s.send(admin, "POST", rules, `{"name":"D","match_type":"country","match_value":"DE","fallback":"","priority":"x"}`); status != 201 || !strings.Contains(answer, `"fallback":"master","priority":10,"enabled":true`) {
			t.Errorf("defaults: %d %s", status, answer)
		}

		if status, answer := s.send(admin, "GET", rules, ""); status != 200 || answer != `{"data":`+stored(t, db, keyRules)+`}` || strings.Count(answer, `"id"`) != 3 {
			t.Errorf("list: %d %s", status, answer)
		}
		if status, answer := s.send(admin, "PUT", rules+"/r-nope", `{`+body+`}`); status != 404 || answer != `{"error":"not found"}` {
			t.Errorf("update unknown: %d %s", status, answer)
		}
		if status, answer := s.send(admin, "DELETE", rules+"/r-0000000000000001", ""); status != 200 || answer != `{"ok":true}` {
			t.Errorf("delete: %d %s", status, answer)
		}
		if status, answer := s.send(admin, "DELETE", rules+"/r-0000000000000001", ""); status != 404 || answer != `{"error":"not found"}` {
			t.Errorf("delete again: %d %s", status, answer)
		}
		if got := stored(t, db, keyRules); strings.Count(got, `"id"`) != 2 || strings.Contains(got, "r-0000000000000001") {
			t.Errorf("stored after the delete: %s", got)
		}

		for bodyText, problem := range map[string]string{
			`{}`: "name must be 1-80 characters",
			`{"name":"` + strings.Repeat("n", 81) + `","match_type":"tag","match_value":"v"}`: "name must be 1-80 characters",
			`{"name":"x","match_value":"v"}`:                                                              "invalid match_type",
			`{"name":"x","match_type":"hostname","match_value":"v"}`:                                      "invalid match_type",
			`{"name":"x","match_type":"Subnet","match_value":"v"}`:                                        "invalid match_type",
			`{"name":"x","match_type":"tag"}`:                                                             "match_value required",
			`{"name":"x","match_type":"tag","match_value":" "}`:                                           "match_value required",
			`{"name":"x","match_type":"tag","match_value":"` + strings.Repeat("v", 201) + `"}`:            "match_value required",
			`{"name":"x","match_type":"tag","match_value":"v","priority":-1}`:                             "priority out of range",
			`{"name":"x","match_type":"tag","match_value":"v","priority":"1001"}`:                         "priority out of range",
			`{"name":"<b>","match_type":"group","match_value":"` + strings.Repeat("v", 200) + `"}`:        "",
			`{"name":"x","match_type":"device_id","match_value":900100001,"priority":1000,"enabled":1}`:   "",
			`{"name":"y","match_type":"subnet","match_value":"10.0.0.0/8","priority":0,"target_relay":7}`: "",
		} {
			for _, target := range []struct{ method, path string }{{"POST", rules}, {"PUT", rules + "/r-0000000000000002"}} {
				status, answer := s.send(admin, target.method, target.path, bodyText)
				if problem == "" {
					if status != 200 && status != 201 {
						t.Errorf("%s %s: %d %s, want it stored", target.method, clip(bodyText), status, answer)
					}
				} else if status != 400 || answer != `{"error":"`+problem+`"}` {
					t.Errorf("%s %s: %d %s, want 400 %s", target.method, clip(bodyText), status, answer, problem)
				}
			}
		}
	})
}

func TestRuleLimit(t *testing.T) {
	each(t, func(t *testing.T, s *site, db *store.DB) {
		full := strings.TrimSuffix(strings.Repeat(`{"id":"old"},`, maxRules), ",")
		if err := setSetting(context.Background(), db, keyRules, "["+full+"]"); err != nil {
			t.Fatal(err)
		}
		status, answer := s.send(s.signIn("admin"), "POST", rules, `{"name":"x","match_type":"tag","match_value":"v"}`)
		if status != 400 || answer != `{"error":"too many rules (max 200)"}` {
			t.Errorf("201st rule: %d %s", status, answer)
		}
		// Deleting by an id that several entries have removes them all
		if status, _ := s.send(s.signIn("admin"), "DELETE", rules+"/old", ""); status != 200 || stored(t, db, keyRules) != "[]" {
			t.Errorf("delete of a shared id: %d, stored %s", status, stored(t, db, keyRules))
		}
	})
}

func TestSettingsRows(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		if _, ok, err := getSetting(ctx, db, "scaling_test"); ok || err != nil {
			t.Fatalf("a missing row: found=%v, %v", ok, err)
		}
		for _, value := range []string{"first", "", `[{"a":"ü \" ' ; --"}]`} {
			if err := setSetting(ctx, db, "scaling_test", value); err != nil {
				t.Fatal(err)
			}
			if got, ok, err := getSetting(ctx, db, "scaling_test"); got != value || !ok || err != nil {
				t.Errorf("stored %q, read %q (found=%v, %v)", value, got, ok, err)
			}
		}
		var rows int
		var stamped bool
		if err := db.QueryRow(ctx, "SELECT COUNT(*), MAX(CASE WHEN updated_at IS NULL THEN 0 ELSE 1 END) = 1 FROM settings WHERE key = 'scaling_test'").Scan(&rows, &stamped); err != nil || rows != 1 || !stamped {
			t.Errorf("%d rows, stamped=%v (%v)", rows, stamped, err)
		}
	})
}

// bodyOf is req.body for a JSON request with the given text.
func bodyOf(raw string) middleware.JSONObject {
	var body middleware.JSONObject
	steps := middleware.Bodies(middleware.BodyOptions{})(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		body = middleware.Body(r)
	}))
	req := httptest.NewRequest("POST", "/", strings.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	steps.ServeHTTP(httptest.NewRecorder(), req)
	return body
}

// FuzzStoredListAndBody feeds text to the reader of a stored list and to
// the validation of a body. Neither may panic; a list that parses writes
// back as JSON that parses to the same.
func FuzzStoredListAndBody(f *testing.F) {
	for _, seed := range []string{`[]`, `[{"id":"r-1","address":"a:1","max_sessions":5}]`, `[null,1,"x",[2],{"a":{"b":[1.50,true]}}]`, `{"name":"x","address":"a:1"}`, `[1,2`, "", `{"name":["a",{"b":1}],"address":" a:65535 ","max_sessions":"9e9","enabled":null,"priority":-0.5}`, "\xff"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		items, err := parseArray(raw)
		if err == nil && items != nil {
			text, _ := list(items).MarshalJSON()
			if !json.Valid(text) {
				t.Fatalf("parseArray(%q) writes %q, which is no JSON", raw, text)
			}
			again, err := parseArray(string(text))
			if err != nil {
				t.Fatalf("parseArray(%q) writes %q, which does not parse: %v", raw, text, err)
			}
			second, _ := list(again).MarshalJSON()
			if !bytes.Equal(text, second) {
				t.Fatalf("parseArray(%q) writes %q, then %q", raw, text, second)
			}
			for i, item := range items {
				if member(item, "id") == "r-1" && find(items, "r-1") > i {
					t.Fatalf("find skips the entry %d of %q", i, raw)
				}
			}
			if i := find(items, "r-1"); i >= 0 && member(items[i], "id") != "r-1" {
				t.Fatalf("find gives the entry %d of %q", i, raw)
			}
			addressTaken(items, "a:1", -1)
		}
		if !json.Valid([]byte(raw)) || !strings.HasPrefix(strings.TrimSpace(raw), "{") {
			return
		}
		body := bodyOf(raw)
		if problem := validateRelay(body); problem == "" {
			address := text(body.Get("address"), "")
			if !addressPattern.MatchString(address) || utf16Len(text(body.Get("name"), "")) > 80 {
				t.Fatalf("validateRelay accepts %q", raw)
			}
		}
		validateRule(body)
	})
}
