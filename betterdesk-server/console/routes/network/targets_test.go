package network

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// The members of a target are checked before anything is stored: text for
// name, host and url, whole numbers in range for port and the times, a
// flag for enabled. A refusal is 400 with the console's own words, the
// same on both databases (BUGS.md NM1).
func TestTargetValuesOfTheWrongType(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newWorld(t, db, names{})
		count := func() int {
			var n int
			if err := db.QueryRow(context.Background(), "SELECT COUNT(*) FROM network_targets").Scan(&n); err != nil {
				t.Fatal(err)
			}
			return n
		}
		send := func(method, path, body string) (int, string) {
			t.Helper()
			rec := w.do("adm", method, path, &body, "")
			return rec.Code, rec.Body.String()
		}
		const (
			name     = `{"error":"name must be text"}`
			host     = `{"error":"host must be text"}`
			url      = `{"error":"url must be text"}`
			port     = `{"error":"port must be a whole number from 1 to 65535"}`
			timeout  = `{"error":"timeout_ms must be a whole number from 1 to 60000"}`
			interval = `{"error":"interval_ms must be a whole number from 1 to 2147483647"}`
			enabled  = `{"error":"enabled must be true or false"}`
			kind     = `{"error":"check_type must be one of: ping, tcp, http, https"}`
		)
		for body, want := range map[string]string{
			`{"name":42,"check_type":"ping","host":"10.1.2.3"}`:                                  name,
			`{"name":{"a":1},"check_type":"ping","host":"10.1.2.3"}`:                             name,
			`{"name":true,"check_type":"ping","host":"10.1.2.3"}`:                                name,
			`{"name":["x"],"check_type":"ping","host":"10.1.2.3"}`:                               name,
			`{"name":"x","check_type":"ping","host":["10.1.2.3"]}`:                               host,
			`{"name":"x","check_type":"ping","host":7}`:                                          host,
			`{"name":"x","check_type":"http","url":5}`:                                           url,
			`{"name":"x","check_type":"http","url":["http://10.1.2.3/"]}`:                        url,
			`{"name":"x","check_type":"tcp","host":"10.1.2.3","port":"1x"}`:                      port,
			`{"name":"x","check_type":"tcp","host":"10.1.2.3","port":70000}`:                     port,
			`{"name":"x","check_type":"tcp","host":"10.1.2.3","port":80.5}`:                      port,
			`{"name":"x","check_type":"tcp","host":"10.1.2.3","port":-1}`:                        port,
			`{"name":"x","check_type":"tcp","host":"10.1.2.3","port":true}`:                      port,
			`{"name":"x","check_type":"tcp","host":"10.1.2.3","port":"5000/tcp"}`:                port,
			`{"name":"x","check_type":"ping","host":"x","timeout_ms":"abc"}`:                     timeout,
			`{"name":"x","check_type":"ping","host":"x","timeout_ms":-5}`:                        timeout,
			`{"name":"x","check_type":"ping","host":"x","timeout_ms":2147483648}`:                timeout,
			`{"name":"x","check_type":"ping","host":"x","timeout_ms":"1e23"}`:                    timeout,
			`{"name":"x","check_type":"ping","host":"x","interval_ms":"1500.9"}`:                 interval,
			`{"name":"x","check_type":"ping","host":"x","interval_ms":[1]}`:                      interval,
			`{"name":"x","check_type":"ping","host":"x","enabled":"no"}`:                         enabled,
			`{"name":"x","check_type":"ping","host":"x","enabled":{}}`:                           enabled,
			`{"name":"x","check_type":"ping","host":"x","enabled":null}`:                         enabled,
			`{"name":"x","check_type":"ping","host":"x","enabled":2}`:                            enabled,
			`{"name":"x","check_type":"ping","host":"x","timeout_ms":"99999999999999999999999"}`: timeout,
		} {
			if status, answer := send("POST", "/api/network/targets", body); status != 400 || answer != want {
				t.Errorf("create %s: %d %s, want 400 %s", body, status, answer, want)
			}
		}
		if n := count(); n != 0 {
			t.Fatalf("%d targets stored by refused requests", n)
		}

		// What the web app sends, and the forms of a number the old routes took
		status, answer := send("POST", "/api/network/targets", `{"name":"Printer","host":"10.1.2.3","check_type":"tcp","port":"9100","interval_ms":30000,"timeout_ms":0,"enabled":0}`)
		var created struct {
			ID         int64           `json:"id"`
			Port       *float64        `json:"port"`
			TimeoutMs  float64         `json:"timeout_ms"`
			IntervalMs float64         `json:"interval_ms"`
			Enabled    json.RawMessage `json:"enabled"`
		}
		if err := json.Unmarshal([]byte(answer), &created); status != 201 || err != nil || created.Port == nil || *created.Port != 9100 || created.TimeoutMs != 5000 || created.IntervalMs != 30000 || !isFlag(created.Enabled, false) {
			t.Fatalf("create: %d %s", status, answer)
		}
		target := "/api/network/targets/" + json.Number(jsonInt(created.ID)).String()
		for body, want := range map[string]string{
			`{"name":null}`:                            name,
			`{"name":["a","b"]}`:                       name,
			`{"name":5000000000}`:                      name,
			`{"host":7.5}`:                             host,
			`{"url":{}}`:                               url,
			`{"port":5000000000}`:                      port,
			`{"port":"none"}`:                          port,
			`{"timeout_ms":"99999999999999999999999"}`: timeout,
			`{"timeout_ms":"none"}`:                    timeout,
			`{"interval_ms":-1}`:                       interval,
			`{"enabled":{}}`:                           enabled,
			`{"enabled":"yes"}`:                        enabled,
			`{"check_type":""}`:                        kind,
			`{"check_type":null}`:                      kind,
			`{"name":"renamed","port":"5000000000"}`:   port,
		} {
			if status, answer := send("PATCH", target, body); status != 400 || answer != want {
				t.Errorf("update %s: %d %s, want 400 %s", body, status, answer, want)
			}
		}
		status, answer = send("PATCH", target, `{"name":"Printer 2","host":null,"port":null,"timeout_ms":2500,"interval_ms":0,"enabled":true}`)
		var updated struct {
			Name       string          `json:"name"`
			Host       *string         `json:"host"`
			Port       *float64        `json:"port"`
			TimeoutMs  float64         `json:"timeout_ms"`
			IntervalMs float64         `json:"interval_ms"`
			Enabled    json.RawMessage `json:"enabled"`
		}
		if err := json.Unmarshal([]byte(answer), &updated); status != 200 || err != nil || updated.Name != "Printer 2" || updated.Host != nil || updated.Port != nil || updated.TimeoutMs != 2500 || updated.IntervalMs != 0 || !isFlag(updated.Enabled, true) {
			t.Errorf("update: %d %s", status, answer)
		}
	})
}

// isFlag reports whether a flag of an answer is on, or off: 1 and 0 on
// SQLite, true and false on PostgreSQL.
func isFlag(raw json.RawMessage, on bool) bool {
	if on {
		return string(raw) == "1" || string(raw) == "true"
	}
	return string(raw) == "0" || string(raw) == "false"
}

// jsonInt writes a whole number as JSON.
func jsonInt(n int64) string {
	out, _ := json.Marshal(n)
	return string(out)
}

// An update that names no column answers the target as it is; 404 is for
// an id no target has (BUGS.md NM2).
func TestUpdateWithNothingAnswersTheTarget(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newWorld(t, db, names{})
		send := func(method, path, body string) (int, string) {
			t.Helper()
			rec := w.do("adm", method, path, &body, "")
			return rec.Code, rec.Body.String()
		}
		status, created := send("POST", "/api/network/targets", `{"name":"Printer","host":"10.1.2.3","check_type":"ping"}`)
		if status != 201 {
			t.Fatalf("create: %d %s", status, created)
		}
		for _, body := range []string{`{}`, `{"note":"x","last_status":"up","id":7}`, `[{"name":"x"}]`} {
			if status, answer := send("PATCH", "/api/network/targets/1", body); status != 200 || answer != created {
				t.Errorf("update %s: %d %s, want 200 %s", body, status, answer, created)
			}
		}
		if status, answer := send("PATCH", "/api/network/targets/2", `{}`); status != 404 || answer != `{"error":"Target not found"}` {
			t.Errorf("update of a missing target: %d %s", status, answer)
		}
	})
}

// The id of the address is read as SQLite compares a text with an id
// column, on both databases: a decimal number with spaces around it, a
// sign, a fraction or an exponent names the target of its value when that
// is a whole number. Any other text, and a number no id column holds,
// names no target: 404 for a read, change, delete and check, no checks
// for the history. PostgreSQL failed with 500 and its own message there
// (BUGS C7).
func TestTargetIDsReadAsSQLiteReadsThem(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newWorld(t, db, names{})
		send := func(as, method, path, body string) (int, string) {
			t.Helper()
			var b *string
			if body != "" {
				b = &body
			}
			rec := w.do(as, method, path, b, "")
			return rec.Code, rec.Body.String()
		}
		status, created := send("adm", "POST", "/api/network/targets", `{"name":"Printer","host":"10.1.2.3","check_type":"ping"}`)
		if status != 201 {
			t.Fatalf("create: %d %s", status, created)
		}
		notFound := `{"error":"Target not found"}`
		for _, id := range []string{"first", "1abc", "1;drop", "1.5", "0x1", "0x1p0", "0X1P0", "1_0", "-1", "0", "1e", "%2B", "-", "2147483648",
			"99999999999999999999", "1e400", "Infinity", "NaN", "1%20x", "%C3%BC"} {
			for _, c := range []struct{ as, method, path, body string }{
				{"vera", "GET", "/api/network/targets/" + id, ""},
				{"adm", "PATCH", "/api/network/targets/" + id, `{"timeout_ms":3000}`},
				{"olga", "POST", "/api/network/targets/" + id + "/check", `{}`},
				{"adm", "DELETE", "/api/network/targets/" + id, ""},
			} {
				if status, answer := send(c.as, c.method, c.path, c.body); status != 404 || answer != notFound {
					t.Errorf("%s %s: %d %s, want 404", c.method, c.path, status, answer)
				}
			}
			if status, answer := send("vera", "GET", "/api/network/targets/"+id+"/history", ""); status != 200 || answer != `[]` {
				t.Errorf("history of %s: %d %s, want 200 []", id, status, answer)
			}
		}
		for _, id := range []string{"1", "01", "1.0", "1.", "1e0", "+1", "%201%20", "%091", "0.1e1", "100e-2"} {
			if status, answer := send("vera", "GET", "/api/network/targets/"+id, ""); status != 200 || answer != created {
				t.Errorf("GET %s: %d %s, want the target", id, status, answer)
			}
			if status, answer := send("adm", "PATCH", "/api/network/targets/"+id, `{}`); status != 200 || answer != created {
				t.Errorf("PATCH %s: %d %s, want the target", id, status, answer)
			}
		}
		if status, answer := send("olga", "POST", "/api/network/targets/1.0/check", `{}`); status != 200 || !strings.HasPrefix(answer, `{"target_id":1,`) {
			t.Errorf("check of 1.0: %d %s", status, answer)
		}
		if status, answer := send("vera", "GET", "/api/network/targets/%201/history", ""); status != 200 || !strings.HasPrefix(answer, `[{"id":1,"target_id":1,`) {
			t.Errorf("history of ' 1': %d %s", status, answer)
		}
		if status, answer := send("adm", "DELETE", "/api/network/targets/1e0", ""); status != 200 || answer != `{"status":"deleted"}` {
			t.Errorf("delete of 1e0: %d %s", status, answer)
		}
	})
}

// The history's limit is a whole number from 1 written in digits, held at
// 500, and the list's check_type one text: anything else is 400 on both
// databases, where a negative limit listed every check on SQLite and
// failed on PostgreSQL, and a type given twice or in brackets failed on
// SQLite and found nothing on PostgreSQL (BUGS NM13).
func TestTargetFiltersAnswerAlikeOnBothDatabases(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newWorld(t, db, names{})
		send := func(as, method, path, body string) (int, string) {
			t.Helper()
			var b *string
			if body != "" {
				b = &body
			}
			rec := w.do(as, method, path, b, "")
			return rec.Code, rec.Body.String()
		}
		for _, body := range []string{
			`{"name":"Printer","host":"10.1.2.3","check_type":"tcp","port":9}`,
			`{"name":"Router","host":"10.1.2.4","check_type":"ping"}`,
		} {
			if status, answer := send("adm", "POST", "/api/network/targets", body); status != 201 {
				t.Fatalf("create: %d %s", status, answer)
			}
		}
		for i := 0; i < 3; i++ {
			if _, err := db.Exec(context.Background(), "INSERT INTO network_checks (target_id, status) VALUES (1, 'up')"); err != nil {
				t.Fatal(err)
			}
		}
		for _, query := range []string{"limit=-1", "limit=0", "limit=00", "limit=all", "limit=2.9", "limit=%202", "limit=1e1", "limit=+2",
			"limit=2&limit=5", "limit[]=4", "limit[a]=4"} {
			if status, answer := send("vera", "GET", "/api/network/targets/1/history?"+query, ""); status != 400 || answer != `{"error":"Invalid limit"}` {
				t.Errorf("history?%s: %d %s, want 400 Invalid limit", query, status, answer)
			}
		}
		for query, want := range map[string]int{"": 3, "limit=": 3, "limit=2": 2, "limit=002": 2, "limit=99999999999999999999": 3} {
			status, answer := send("vera", "GET", "/api/network/targets/1/history?"+query, "")
			if got := strings.Count(answer, `"target_id":1`); status != 200 || got != want {
				t.Errorf("history?%s: %d with %d checks, want 200 with %d: %s", query, status, got, want, answer)
			}
		}
		for _, query := range []string{"check_type=tcp&check_type=ping", "check_type[]=tcp", "check_type[]=tcp&check_type[]=ping",
			"check_type[0]=tcp", "check_type[is]=tcp", "check_type[a][b]=tcp", "enabled=true&check_type[]=tcp&check_type[]=x"} {
			if status, answer := send("vera", "GET", "/api/network/targets?"+query, ""); status != 400 || answer != `{"error":"Invalid check_type"}` {
				t.Errorf("targets?%s: %d %s, want 400 Invalid check_type", query, status, answer)
			}
		}
		for query, want := range map[string]string{"check_type=tcp": "Printer", "check_type=": "Printer Router", "check_type": "Printer Router",
			"check_type=icmp": "", "check_type=%74cp&enabled=true": "Printer"} {
			status, answer := send("vera", "GET", "/api/network/targets?"+query, "")
			var targets []struct {
				Name string `json:"name"`
			}
			_ = json.Unmarshal([]byte(answer), &targets)
			got := make([]string, len(targets))
			for i, target := range targets {
				got[i] = target.Name
			}
			if status != 200 || strings.Join(got, " ") != want {
				t.Errorf("targets?%s: %d %q, want 200 %q", query, status, strings.Join(got, " "), want)
			}
		}
	})
}

// The history's from and to are times, compared as times on both
// databases: a date alone as to takes in that whole day, and a time
// between two microseconds is rounded into the window. Anything else is
// 400 on both, where SQLite compared the text and PostgreSQL failed on
// what it could not read as a time (BUGS NM14).
func TestHistoryWindowAnswersAlikeOnBothDatabases(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newWorld(t, db, names{})
		body := `{"name":"Printer","host":"10.1.2.3","check_type":"tcp","port":9}`
		if rec := w.do("adm", "POST", "/api/network/targets", &body, ""); rec.Code != 201 {
			t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
		}
		for _, at := range []string{"2019-03-03 23:59:59", "2019-03-04 10:00:00", "2019-03-04 23:59:59", "2019-03-05 00:00:00"} {
			if _, err := db.Exec(context.Background(), "INSERT INTO network_checks (target_id, status, checked_at) VALUES (1, 'up', ?)", db.Pick(at, at+"+00")); err != nil {
				t.Fatal(err)
			}
		}
		history := func(query string) (int, string) {
			t.Helper()
			rec := w.do("vera", "GET", "/api/network/targets/1/history?"+query, nil, "")
			return rec.Code, rec.Body.String()
		}
		for query, want := range map[string]string{
			"from=never": "from", "to=never": "to", "from=1": "from", "from=2019-13-01": "from", "from=2019-02-30": "from",
			"from=2019-03-04%2025:00:00": "from", "to=2019-03-04T10": "to", "from=%202019-03-04": "from",
			"from=0000-01-01": "from", "from=0001-01-01T00:00:00%2B01:00": "from", "to=9999-12-31T23:00:00-05:00": "to",
			"from=a&from=b": "from", "from=a&from=b&limit=1": "from", "to=2019-03-04&to=2019-03-05": "to",
			"from[x]=1": "from", "to[x]=1": "to", "from[]=2019-03-04": "from", "from[]=a&to[]=b": "from", "to[x]=1&from[y]=2": "from",
		} {
			if status, answer := history(query); status != 400 || answer != `{"error":"Invalid `+want+`"}` {
				t.Errorf("history?%s: %d %s, want 400 Invalid %s", query, status, answer, want)
			}
		}
		for query, want := range map[string]string{
			"":                                  "4 3 2 1",
			"from=&to=":                         "4 3 2 1",
			"from":                              "4 3 2 1",
			"from=2019-03-04":                   "4 3 2",
			"to=2019-03-04":                     "3 2 1",
			"from=2019-03-04&to=2019-03-04":     "3 2",
			"from=2019-03-04T10:00:00Z":         "4 3 2",
			"from=2019-03-04T10:00:00.5Z":       "4 3",
			"from=2019-03-04T10:00:00.0000001Z": "4 3",
			"to=2019-03-04T10:00:00.000Z":       "2 1",
			"to=2019-03-04T09:59:59.9999999Z":   "1",
			"from=2019-03-04%2010:00:00":        "4 3 2",
			"from=2019-03-04T12:00:00%2B02:00":  "4 3 2",
			"to=2019-03-04T05:00:00-05:00":      "2 1",
			"from=0001-01-01&to=9999-12-31":     "4 3 2 1",
			"from=2019-03-04%2010:00:00&to=2019-03-04%2023:59:59&limit=1": "3",
		} {
			status, answer := history(query)
			var checks []struct {
				ID int `json:"id"`
			}
			_ = json.Unmarshal([]byte(answer), &checks)
			ids := make([]string, len(checks))
			for i, c := range checks {
				ids[i] = fmt.Sprint(c.ID)
			}
			if status != 200 || strings.Join(ids, " ") != want {
				t.Errorf("history?%s: %d %q, want 200 %q: %s", query, status, strings.Join(ids, " "), want, answer)
			}
		}
	})
}

// The stats average the last round trip of the targets that have one
// (BUGS.md NM3).
func TestStatsAverageTheLastRoundTrip(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newWorld(t, db, names{})
		stats := func() string {
			t.Helper()
			rec := w.do("vera", "GET", "/api/network/stats", nil, "")
			if rec.Code != 200 {
				t.Fatalf("stats: %d %s", rec.Code, rec.Body.String())
			}
			return rec.Body.String()
		}
		if got := stats(); got != `{"total":0,"up":0,"down":0,"unknown":0,"avg_latency":null}` {
			t.Errorf("no targets: %s", got)
		}
		ctx := context.Background()
		for _, row := range []struct {
			status any
			rtt    any
		}{{"up", 10}, {"up", 20.5}, {"down", nil}, {nil, nil}} {
			if _, err := db.Exec(ctx, "INSERT INTO network_targets (name, host, check_type, last_status, last_rtt_ms) VALUES ('t', 'h', 'ping', ?, ?)", row.status, row.rtt); err != nil {
				t.Fatal(err)
			}
		}
		if got := stats(); got != `{"total":4,"up":2,"down":1,"unknown":1,"avg_latency":15.25}` {
			t.Errorf("two round trips: %s", got)
		}
		if _, err := db.Exec(ctx, "UPDATE network_targets SET last_rtt_ms = NULL"); err != nil {
			t.Fatal(err)
		}
		if got := stats(); got != `{"total":4,"up":2,"down":1,"unknown":1,"avg_latency":null}` {
			t.Errorf("no round trip: %s", got)
		}
	})
}

// The monitor takes an interval from one second to the longest a timer
// waits; below or above that it is not started (BUGS.md NM6). An interval
// that is no number still means the default.
func TestMonitorRefusesAnIntervalBelowASecond(t *testing.T) {
	w := newWorld(t, storetest.Migrated(t), names{})
	start := func(body string) (int, string) {
		t.Helper()
		rec := w.do("adm", "POST", "/api/network/monitor/start", &body, "")
		return rec.Code, rec.Body.String()
	}
	stop := func() {
		t.Helper()
		body := "{}"
		w.do("adm", "POST", "/api/network/monitor/stop", &body, "")
	}
	const refused = `{"error":"interval_ms must be from 1000 to 2147483647"}`
	for _, body := range []string{`{"interval_ms":-5}`, `{"interval_ms":1}`, `{"interval_ms":999}`, `{"interval_ms":"1e5"}`, `{"interval_ms":2147483648}`, `{"interval_ms":1e21}`} {
		if status, answer := start(body); status != 400 || answer != refused {
			t.Errorf("%s: %d %s, want 400 %s", body, status, answer, refused)
		}
		if w.monitor.Running() {
			t.Errorf("%s: the monitor runs", body)
			stop()
		}
	}
	for body, want := range map[string]string{
		`{"interval_ms":1000}`:       `{"status":"started","interval_ms":1000}`,
		`{"interval_ms":2147483647}`: `{"status":"started","interval_ms":2147483647}`,
		`{"interval_ms":"soon"}`:     `{"status":"started","interval_ms":60000}`,
		`{"interval_ms":0}`:          `{"status":"started","interval_ms":60000}`,
		`{}`:                         `{"status":"started","interval_ms":60000}`,
	} {
		if status, answer := start(body); status != 200 || answer != want {
			t.Errorf("%s: %d %s, want 200 %s", body, status, answer, want)
		}
		stop()
	}
}

// The ping, TCP and HTTP tools refuse a timeout below 1 before anything
// leaves the server (BUGS.md NM7). One that is no number or 0 still means
// the default.
func TestToolsRefuseATimeoutBelowOne(t *testing.T) {
	w := newWorld(t, storetest.Migrated(t), names{})
	const refused = `{"error":"timeout must be at least 1"}`
	for _, c := range []struct{ path, body string }{
		{"/api/network/ping", `{"host":"10.1.2.3","timeout":-1}`},
		{"/api/network/ping", `{"host":"10.1.2.3","timeout":"-5000"}`},
		{"/api/network/ping", `{"host":"10.1.2.3","timeout":-5000000000}`},
		{"/api/network/tcp", `{"host":"10.1.2.3","port":80,"timeout":-1}`},
		{"/api/network/tcp", `{"host":"10.1.2.3","port":80,"timeout":"-2000ms"}`},
		{"/api/network/http", `{"url":"http://10.1.2.3/","timeout":-1}`},
		{"/api/network/http", `{"url":"http://10.1.2.3/","timeout":-1e23}`},
	} {
		w.pinged = nil
		rec := w.do("olga", "POST", c.path, &c.body, "")
		if rec.Code != 400 || rec.Body.String() != refused {
			t.Errorf("%s %s: %d %s, want 400 %s", c.path, c.body, rec.Code, rec.Body.String(), refused)
		}
		if len(w.pinged) != 0 {
			t.Errorf("%s %s: ping ran with %q", c.path, c.body, w.pinged)
		}
	}
	for _, c := range []struct{ path, body string }{
		{"/api/network/ping", `{"host":"10.1.2.3","timeout":0}`},
		{"/api/network/ping", `{"host":"10.1.2.3","timeout":"soon"}`},
		{"/api/network/tcp", `{"host":"10.1.2.3","port":80,"timeout":1}`},
		{"/api/network/http", `{"url":"http://10.1.2.3/","timeout":-0.5}`},
	} {
		if rec := w.do("olga", "POST", c.path, &c.body, ""); rec.Code != 200 {
			t.Errorf("%s %s: %d %s, want the check", c.path, c.body, rec.Code, rec.Body.String())
		}
	}
}
