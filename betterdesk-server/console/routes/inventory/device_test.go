package inventory_test

import (
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/routes/internal/replay"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// TestNodeDeviceAnswers replays what the Node console's three device
// routes answered with its real database adapter and alert rules engine,
// on SQLite and on PostgreSQL (testdata/node_device_sqlite.json,
// testdata/node_device_postgres.json), and the rows they left: tokens in
// every state and Authorization headers in every form, the body checked
// before the device it names, reports with values of every JSON type, the
// info of a device's row in every form either database holds it, devices
// only the signal server knows, disks of every kind with the disk alerts
// they raise, the count of apps in the log line, and telemetry with
// values of every type and the CPU and memory alerts.
//
// The alert rules are evaluated in the background, as in the old
// console; each step waits for them before the next one reads the alerts.
//
// Two kinds of answers are not compared as the requests were sent:
//
//   - Node's HTTP parser removed white space around a header's value,
//     which the test's requests skip (the console's server removes it as
//     well). A request with such a header is sent again without it, and
//     that answer is compared.
//   - A body that is no JSON was answered by the recording's stand-in for
//     the rest of the console; there only the status is compared.
func TestNodeDeviceAnswers(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		engine := w.Server.Deps().Alerts
		trimmed, elsewhere := 0, 0
		w.Run(replay.Load(t, db, "node_device"), func(t *testing.T, step replay.Step, got replay.Answer) bool {
			engine.Wait()
			if headers, ok := withoutSpaceAround(step.Headers); ok {
				trimmed++
				got = w.Do(replay.Request{Role: step.Role, Method: step.Method, Path: step.Path, Body: step.Body, Headers: headers})
				engine.Wait()
				if want, have := replay.Normalize(step.Answer), replay.Normalize(got.Body); got.Status != step.Status || want != have {
					t.Errorf("%s with the header trimmed:\n   node: %d %s\n     go: %d %s", step.Name, step.Status, want, got.Status, have)
				}
				return true
			}
			if step.Answer == `{"success":false,"error":"Internal Server Error"}` && step.Status == 400 {
				elsewhere++
				if got.Status != step.Status {
					t.Errorf("%s: status %d, want %d", step.Name, got.Status, step.Status)
				}
				return true
			}
			return false
		})
		if trimmed != 6 || elsewhere != 1 {
			t.Errorf("%d requests were sent again with a header trimmed and %d answers compared by status only, want 6 and 1", trimmed, elsewhere)
		}
	})
}

// withoutSpaceAround returns the headers with the white space around each
// value removed, as an HTTP server reads them, and whether a value had
// any.
func withoutSpaceAround(headers map[string]string) (map[string]string, bool) {
	out := map[string]string{}
	changed := false
	for name, value := range headers {
		out[name] = strings.Trim(value, " \t")
		changed = changed || out[name] != value
	}
	return out, changed
}

// tokenDeviceA puts a live token bound to device-a into the database: the
// token tok-device-a with its SHA-256.
func tokenDeviceA(w *replay.World) {
	w.Exec("INSERT INTO access_tokens (token, token_hash, user_id, client_id, expires_at) VALUES (?, ?, 5, 'device-a', '2999-01-01 00:00:00')",
		[]any{"tok-device-a", "a70dc32ded6e642ec955672a88edb7cc9d6d9e2abb6bf78146a63491adb24155"})
}

var deviceA = map[string]string{"Authorization": "Bearer tok-device-a"}

// csrfRefused is the CSRF gate's answer.
const csrfRefused = `{"success":false,"error":"Invalid CSRF token. Please refresh the page and try again. If you recently disabled SSL, clear your browser cookies for this site and reload."}`

// A device that writes to /api/inventory needs a CSRF token like a
// browser; at /api/bd it does not while nobody is signed in (BUGS DV2). A
// signed-in browser needs the token at both mounts. No role is refused by
// the panel access guard in front of the group.
func TestTheCSRFGateAtEachMount(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		tokenDeviceA(w)
		report := replay.Text(`{"device_id":"device-a","hardware":{"hostname":"pc"}}`)
		telemetry := replay.Text(`{"device_id":"device-a","cpu_usage_percent":5}`)
		stored := `{"success":true}`
		for _, c := range []struct {
			name, role, path string
			body             *string
			status           int
			answer           string
		}{
			{"inventory at /api/bd without a session", "", "/api/bd/inventory", report, 200, stored},
			{"telemetry at /api/bd without a session", "", "/api/bd/telemetry", telemetry, 200, stored},
			{"inventory at /api/inventory without a session", "", "/api/inventory/inventory", report, 403, csrfRefused},
			{"telemetry at /api/inventory without a session", "", "/api/inventory/telemetry", telemetry, 403, csrfRefused},
			{"inventory at /api/bd with a session", "operator", "/api/bd/inventory", report, 403, csrfRefused},
			{"inventory at /api/inventory with a session", "admin", "/api/inventory/inventory", report, 403, csrfRefused},
		} {
			got := w.Do(replay.Request{Role: c.role, Method: "POST", Path: c.path, Body: c.body, Headers: deviceA, NoCSRF: true})
			if got.Status != c.status || got.Body != c.answer {
				t.Errorf("%s: %d %s, want %d %s", c.name, got.Status, got.Body, c.status, c.answer)
			}
		}
		for _, role := range []string{"", "pro", "guest_issuer", "viewer", "operator", "global_admin", "server_admin", "super_admin", "admin"} {
			for _, path := range []string{"/api/inventory/inventory", "/api/bd/inventory"} {
				if got := w.Do(replay.Request{Role: role, Method: "POST", Path: path, Body: report, Headers: deviceA}); got.Status != 200 || got.Body != stored {
					t.Errorf("POST %s as %q with the CSRF token: %d %s", path, role, got.Status, got.Body)
				}
			}
		}
	})
}

// The token check: a token the lookup cannot read is "invalid or
// expired", and a failure to note the use of a good token changes
// nothing.
func TestTheTokenCheckWhenTheDatabaseFails(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		tokenDeviceA(w)
		w.Exec("INSERT INTO device_inventory (device_id, hardware, software) VALUES ('device-a', '{}', '{}')", nil)
		refuseUpdates(w, "access_tokens")
		if got := w.Do(replay.Request{Path: "/api/bd/inventory/device-a", Headers: deviceA}); got.Status != 200 || !strings.Contains(got.Body, `"device_id":"device-a"`) {
			t.Errorf("a good token whose use cannot be noted: %d %s, want the report", got.Status, got.Body)
		}
		w.Exec("DROP TABLE access_tokens", nil)
		want := `{"error":"Invalid or expired access token"}`
		for _, req := range []replay.Request{
			{Path: "/api/bd/inventory/device-a", Headers: deviceA},
			{Method: "POST", Path: "/api/bd/inventory", Body: replay.Text(`{}`), Headers: deviceA},
			{Method: "POST", Path: "/api/inventory/telemetry", Body: replay.Text(`{}`), Headers: deviceA},
		} {
			if got := w.Do(req); got.Status != 401 || got.Body != want {
				t.Errorf("%s %s without the token table: %d %s, want 401 %s", req.Method, req.Path, got.Status, got.Body, want)
			}
		}
		if got := w.Do(replay.Request{Path: "/api/bd/inventory/device-a"}); got.Status != 401 || got.Body != `{"error":"Bearer access token required"}` {
			t.Errorf("without a token and without the token table: %d %s, want the 401 of a missing token", got.Status, got.Body)
		}
	})
}

// A query of a route that fails is the route's 500: with success false on
// the two uploads, without it on the read. A failure to write the
// device's info is logged and changes nothing.
func TestDeviceRouteFailures(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		tokenDeviceA(w)
		w.Exec("DROP TABLE peer", nil)
		report := replay.Text(`{"device_id":"device-a","hardware":{"hostname":"pc"}}`)
		if got := w.Do(replay.Request{Method: "POST", Path: "/api/bd/inventory", Body: report, Headers: deviceA}); got.Status != 200 || got.Body != `{"success":true}` {
			t.Errorf("an upload without the device table: %d %s, want it stored", got.Status, got.Body)
		}
		if got := w.Rows("SELECT device_id FROM device_inventory", nil); got != `[{"device_id":"device-a"}]` {
			t.Errorf("the reports after an upload without the device table: %s", got)
		}

		w.Exec("DROP TABLE device_telemetry", nil)
		w.Exec("DROP TABLE device_inventory", nil)
		upload := `{"success":false,"error":"Internal server error"}`
		for _, req := range []replay.Request{
			{Method: "POST", Path: "/api/bd/inventory", Body: report, Headers: deviceA},
			{Method: "POST", Path: "/api/bd/telemetry", Body: replay.Text(`{"device_id":"device-a"}`), Headers: deviceA},
		} {
			if got := w.Do(req); got.Status != 500 || got.Body != upload {
				t.Errorf("%s without its table: %d %s, want 500 %s", req.Path, got.Status, got.Body, upload)
			}
		}
		if got := w.Do(replay.Request{Path: "/api/bd/inventory/device-a", Headers: deviceA}); got.Status != 500 || got.Body != `{"error":"Internal server error"}` {
			t.Errorf("the report without its table: %d %s", got.Status, got.Body)
		}
		// The checks before the database answer as before
		if got := w.Do(replay.Request{Path: "/api/bd/inventory/device-b", Headers: deviceA}); got.Status != 403 {
			t.Errorf("another device's report without the table: %d %s, want 403", got.Status, got.Body)
		}
		if got := w.Do(replay.Request{Method: "POST", Path: "/api/bd/inventory", Body: replay.Text(`{"device_id":"device-a"}`), Headers: deviceA}); got.Status != 400 {
			t.Errorf("an upload without hardware without the table: %d %s, want 400", got.Status, got.Body)
		}
	})
}

// refuseUpdates makes every UPDATE of the table fail.
func refuseUpdates(w *replay.World, table string) {
	if w.DB.IsPostgres() {
		w.Exec(`CREATE FUNCTION refuse_update() RETURNS trigger AS $body$ BEGIN RAISE EXCEPTION 'no updates'; END $body$ LANGUAGE plpgsql`, nil)
		w.Exec(`CREATE TRIGGER refuse_update BEFORE UPDATE ON `+table+` FOR EACH ROW EXECUTE FUNCTION refuse_update()`, nil)
		return
	}
	w.Exec(`CREATE TRIGGER refuse_update BEFORE UPDATE ON `+table+` BEGIN SELECT RAISE(ABORT, 'no updates'); END`, nil)
}
