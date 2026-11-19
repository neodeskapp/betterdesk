package network

import (
	"context"
	"encoding/json"
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
			timeout  = `{"error":"timeout_ms must be a whole number from 1 to 2147483647"}`
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
