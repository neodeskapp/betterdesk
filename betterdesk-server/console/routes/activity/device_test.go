package activity_test

import (
	"testing"

	"github.com/unitronix/betterdesk-server/console/routes/internal/replay"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// TestNodeDeviceAnswers replays what the Node console's device route
// answered with its real database adapter, device guards and alert rules
// engine, on SQLite and on PostgreSQL (testdata/node_device_sqlite.json,
// testdata/node_device_postgres.json), and the rows it left: tokens in
// every state, the device named in the body, the query and the header
// (the guard, then the route's own comparison), sessions in every form and
// each of their members with values of every type, the total of the
// durations as JavaScript's + makes it, the idle time and the time of the
// report with values of every type, and the idle alerts.
//
// The alert rules are evaluated in the background, as in the old
// console; each step waits for them before the next one reads the alerts.
// A body that is no JSON was answered by the recording's stand-in for the
// rest of the console; there only the status is compared.
func TestNodeDeviceAnswers(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		engine := w.Server.Deps().Alerts
		elsewhere := 0
		w.Run(replay.Load(t, db, "node_device"), func(t *testing.T, step replay.Step, got replay.Answer) bool {
			engine.Wait()
			if step.Answer == `{"success":false,"error":"Internal Server Error"}` && step.Status == 400 {
				elsewhere++
				if got.Status != step.Status {
					t.Errorf("%s: status %d, want %d", step.Name, got.Status, step.Status)
				}
				return true
			}
			return false
		})
		if elsewhere != 1 {
			t.Errorf("%d answers were compared by status only, want the 1 of the body that is no JSON", elsewhere)
		}
	})
}

// tokenDeviceA puts a live token bound to device-a into the database: the
// token tok-device-a with its SHA-256.
func tokenDeviceA(w *replay.World) {
	w.Exec("INSERT INTO access_tokens (token, token_hash, user_id, client_id, expires_at) VALUES (?, ?, 5, 'device-a', '2999-01-01 00:00:00')",
		[]any{"tok-device-a", "a70dc32ded6e642ec955672a88edb7cc9d6d9e2abb6bf78146a63491adb24155"})
}

var deviceA = map[string]string{"Authorization": "Bearer tok-device-a"}

// A device that reports to /api/activity needs a CSRF token like a
// browser; at /api/bd it does not while nobody is signed in (BUGS DV2). A
// signed-in browser needs the token at both mounts, and at
// /api/activity the panel access guard of the activity section comes
// first: it refuses a report from every role that may not see the audit
// log, and from a guest issuer.
func TestTheCSRFGateAtEachMount(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		tokenDeviceA(w)
		report := replay.Text(`{"device_id":"device-a","timestamp":"2011-03-04T10:00:00.000Z"}`)
		refused := `{"success":false,"error":"Invalid CSRF token. Please refresh the page and try again. If you recently disabled SSL, clear your browser cookies for this site and reload."}`
		stored := `{"success":true}`
		for _, c := range []struct {
			name, role, path string
			status           int
			answer           string
		}{
			{"at /api/bd without a session", "", "/api/bd/activity", 200, stored},
			{"at /api/activity without a session", "", "/api/activity/activity", 403, refused},
			{"at /api/bd with a session", "operator", "/api/bd/activity", 403, refused},
			{"at /api/activity with a session", "admin", "/api/activity/activity", 403, refused},
		} {
			got := w.Do(replay.Request{Role: c.role, Method: "POST", Path: c.path, Body: report, Headers: deviceA, NoCSRF: true})
			if got.Status != c.status || got.Body != c.answer {
				t.Errorf("%s: %d %s, want %d %s", c.name, got.Status, got.Body, c.status, c.answer)
			}
		}
		guarded := `{"success":false,"error":"Panel access denied: activity","code":"panel_access_denied"}`
		for role, passes := range map[string]bool{
			"": true, "guest_issuer": false, "viewer": false, "operator": false, "global_admin": false, "server_admin": false, "super_admin": true, "admin": true,
		} {
			got := w.Do(replay.Request{Role: role, Method: "POST", Path: "/api/activity/activity", Body: report, Headers: deviceA})
			if passes && (got.Status != 200 || got.Body != stored) || !passes && (got.Status != 403 || got.Body != guarded) {
				t.Errorf("POST /api/activity/activity as %q with the CSRF token: %d %s, the guard lets it pass: %v", role, got.Status, got.Body, passes)
			}
			if got := w.Do(replay.Request{Role: role, Method: "POST", Path: "/api/bd/activity", Body: report, Headers: deviceA}); got.Status != 200 || got.Body != stored {
				t.Errorf("POST /api/bd/activity as %q with the CSRF token: %d %s", role, got.Status, got.Body)
			}
		}
	})
}

// A query that fails is the route's 500. Sessions are stored all or none,
// and a report whose sessions fail leaves no summary either.
func TestDeviceRouteFailures(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		tokenDeviceA(w)
		failed := `{"success":false,"error":"Internal server error"}`
		session := func(minute, duration string) string {
			return `{"started_at":"2011-03-04T09:` + minute + `:00.000Z","ended_at":"2011-03-04T09:` + minute + `:30.000Z","duration_secs":` + duration + `}`
		}
		// The second session's duration is no number PostgreSQL takes and
		// the second session's app no value SQLite takes
		bad := `{"started_at":"2011-03-04T09:02:00.000Z","ended_at":"2011-03-04T09:02:30.000Z","duration_secs":"half","app_name":true}`
		body := `{"device_id":"device-a","sessions":[` + session("01", "30") + `,` + bad + `],"timestamp":"2011-03-04T10:00:00.000Z"}`
		if got := w.Do(replay.Request{Method: "POST", Path: "/api/bd/activity", Body: replay.Text(body), Headers: deviceA}); got.Status != 500 || got.Body != failed {
			t.Errorf("a report with a session the database refuses: %d %s, want 500 %s", got.Status, got.Body, failed)
		}
		if got := w.Rows("SELECT COUNT(*) AS n FROM activity_sessions", nil) + w.Rows("SELECT COUNT(*) AS n FROM activity_summaries", nil); got != `[{"n":0}][{"n":0}]` {
			t.Errorf("rows after the failed report: %s, want none", got)
		}
		w.Exec("DROP TABLE activity_summaries", nil)
		body = `{"device_id":"device-a","sessions":[` + session("03", "30") + `],"timestamp":"2011-03-04T10:00:00.000Z"}`
		if got := w.Do(replay.Request{Method: "POST", Path: "/api/bd/activity", Body: replay.Text(body), Headers: deviceA}); got.Status != 500 || got.Body != failed {
			t.Errorf("a report without the summary table: %d %s, want 500 %s", got.Status, got.Body, failed)
		}
		// The sessions were stored before the summary failed
		if got := w.Rows("SELECT COUNT(*) AS n FROM activity_sessions", nil); got != `[{"n":1}]` {
			t.Errorf("sessions after the report without the summary table: %s, want 1", got)
		}
	})
}
