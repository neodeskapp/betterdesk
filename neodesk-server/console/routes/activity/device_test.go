package activity_test

import (
	"fmt"
	"maps"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/replay"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// refusedSteps are the steps of the device recordings that a bug fix of
// BUGS.md changed into the device guard's refusal, 403 {"error":"Device
// ID mismatch"}, with the bug. The names are the same in both recordings.
var refusedSteps = map[string]string{
	// A device_id that cannot be text names no device.
	"46 POST /api/nd/activity":                           "DV3",
	"47 POST /api/nd/activity":                           "DV3",
	"54 POST /api/nd/activity?device_id%5BtoString%5D=x": "DV3",
	// Only text names a device: a number, true or false, a list in the body
	// or in the query is refused, where Node compared the text String()
	// made of it.
	"35 POST /api/nd/activity":                          "DV4",
	"36 POST /api/nd/activity":                          "DV4",
	"40 POST /api/nd/activity":                          "DV4",
	"41 POST /api/nd/activity":                          "DV4",
	"48 POST /api/nd/activity":                          "DV4",
	"53 POST /api/nd/activity?device_id%5B%5D=device-a": "DV4",
}

// storedSteps are the reports of the device recordings that Node refused
// or failed and the Go console stores since a bug fix of BUGS.md, by name
// and bug on SQLite and on PostgreSQL. "" in the database's place means
// the step is no such report there.
var storedSteps = map[string][2]string{
	// The report is the token's device's: one that names its device only
	// in the query or in X-Device-Id, or nowhere, is stored.
	"26 POST /api/activity/activity":              {"AI17", "AI17"},
	"30 POST /api/activity/activity":              {"AI17", "AI17"},
	"32 POST /api/nd/activity":                    {"AI17", "AI17"},
	"33 POST /api/nd/activity":                    {"AI17", "AI17"},
	"34 POST /api/nd/activity":                    {"AI17", "AI17"},
	"51 POST /api/nd/activity?device_id=device-a": {"AI17", "AI17"},
	"57 POST /api/nd/activity":                    {"AI17", "AI17"},
	"59 POST /api/nd/activity":                    {"AI17", "AI17"},
	// A session that is null is skipped; a duration that is no number
	// counts 0, also in a session that is not stored; the log line counts
	// the stored sessions.
	"75 POST /api/nd/activity": {"AI16", "AI16"},
	"76 POST /api/nd/activity": {"AI16", "AI16"},
	"79 POST /api/nd/activity": {"AI16", "AI16"},
	"80 POST /api/nd/activity": {"AI16", "AI16"},
	"82 POST /api/nd/activity": {"AI16", "AI16"},
	"83 POST /api/nd/activity": {"AI16", "AI16"},
	// PostgreSQL took the total "0 7 30" for no number
	"207 POST /api/nd/activity": {"", "AI16"},
}

// invalidReports are the steps of the device recordings whose body is no
// JSON object, which the route refuses as no report since it takes the
// device from the token (BUGS AI17), where Node asked for the body's
// device_id.
var invalidReports = map[string]string{"257 POST /api/nd/activity": "AI17"}

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
// rest of the console; there only the status is compared. The steps in
// refusedSteps are compared with the refusal, those in storedSteps with
// the answer of a stored report, and the rows read after the reports by
// storedRows (stored_test.go).
func TestNodeDeviceAnswers(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		engine := w.Server.Deps().Alerts
		elsewhere := 0
		refused, answered := map[string]bool{}, map[string]bool{}
		rec := replay.Load(t, db, "node_device")
		stored, differ := storedRows(&rec, w)
		w.Run(rec, func(t *testing.T, step replay.Step, got replay.Answer) bool {
			engine.Wait()
			if stored(t, step, got) {
				return true
			}
			if bug, ok := invalidReports[step.Name]; ok {
				answered[step.Name] = true
				if got.Status != 400 || got.Body != `{"success":false,"error":"Invalid report"}` {
					t.Errorf("%s (%s): %d %s, want 400 Invalid report", step.Name, bug, got.Status, got.Body)
				}
				return true
			}
			if bugs, ok := storedSteps[step.Name]; ok && bugs[map[bool]int{false: 0, true: 1}[db.IsPostgres()]] != "" {
				answered[step.Name] = true
				if got.Status != 200 || got.Body != `{"success":true}` {
					t.Errorf("%s (%s): %d %s, want it stored", step.Name, bugs, got.Status, got.Body)
				}
				return true
			}
			if bug, ok := refusedSteps[step.Name]; ok {
				refused[step.Name] = true
				if got.Status != 403 || got.Body != `{"error":"Device ID mismatch"}` {
					t.Errorf("%s (%s): %d %s, want the refusal of another device", step.Name, bug, got.Status, got.Body)
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
		if elsewhere != 1 {
			t.Errorf("%d answers were compared by status only, want the 1 of the body that is no JSON", elsewhere)
		}
		if want := map[bool]map[string]int{
			false: {"alerts": 4, "sessions": 2, "summaries": 34},
			true:  {"alerts": 5, "sessions": 2, "summaries": 28},
		}[db.IsPostgres()]; !maps.Equal(differ, want) {
			t.Errorf("statements whose rows differ from Node's, by table: %v, want %v", differ, want)
		}
		for name := range invalidReports {
			if !answered[name] {
				t.Errorf("the listed step %q is not in the recording", name)
			}
		}
		for name, bugs := range storedSteps {
			if !answered[name] && bugs[map[bool]int{false: 0, true: 1}[db.IsPostgres()]] != "" {
				t.Errorf("the listed step %q is not in the recording", name)
			}
		}
		for name := range refusedSteps {
			if !refused[name] {
				t.Errorf("the listed step %q is not in the recording", name)
			}
		}
	})
}

// tokenDeviceA puts a live token bound to device-a into the database: the
// token tok-device-a with its SHA-256.
func tokenDeviceA(w *replay.World) {
	w.Exec("INSERT INTO access_tokens (token, token_hash, user_id, client_id, expires_at, token_kind) VALUES (?, ?, 5, 'device-a', '2999-01-01 00:00:00', 'device')",
		[]any{"tok-device-a", "a70dc32ded6e642ec955672a88edb7cc9d6d9e2abb6bf78146a63491adb24155"})
}

var deviceA = map[string]string{"Authorization": "Bearer tok-device-a"}

// A device that reports with its token and no session needs no CSRF token
// at either mount (BUGS DV2). A signed-in browser needs the token at both
// mounts, and at
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
			{"at /api/nd without a session", "", "/api/nd/activity", 200, stored},
			{"at /api/activity without a session", "", "/api/activity/activity", 200, stored},
			{"at /api/nd with a session", "operator", "/api/nd/activity", 403, refused},
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
			if got := w.Do(replay.Request{Role: role, Method: "POST", Path: "/api/nd/activity", Body: report, Headers: deviceA}); got.Status != 200 || got.Body != stored {
				t.Errorf("POST /api/nd/activity as %q with the CSRF token: %d %s", role, got.Status, got.Body)
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
		if got := w.Do(replay.Request{Method: "POST", Path: "/api/nd/activity", Body: replay.Text(body), Headers: deviceA}); got.Status != 500 || got.Body != failed {
			t.Errorf("a report with a session the database refuses: %d %s, want 500 %s", got.Status, got.Body, failed)
		}
		if got := w.Rows("SELECT COUNT(*) AS n FROM activity_sessions", nil) + w.Rows("SELECT COUNT(*) AS n FROM activity_summaries", nil); got != `[{"n":0}][{"n":0}]` {
			t.Errorf("rows after the failed report: %s, want none", got)
		}
		w.Exec("DROP TABLE activity_summaries", nil)
		body = `{"device_id":"device-a","sessions":[` + session("03", "30") + `],"timestamp":"2011-03-04T10:00:00.000Z"}`
		if got := w.Do(replay.Request{Method: "POST", Path: "/api/nd/activity", Body: replay.Text(body), Headers: deviceA}); got.Status != 500 || got.Body != failed {
			t.Errorf("a report without the summary table: %d %s, want 500 %s", got.Status, got.Body, failed)
		}
		// The sessions were stored before the summary failed
		if got := w.Rows("SELECT COUNT(*) AS n FROM activity_sessions", nil); got != `[{"n":1}]` {
			t.Errorf("sessions after the report without the summary table: %s, want 1", got)
		}
	})
}

// The summary of a report counts the sessions that are stored and adds
// their durations as numbers. A session that is no object is skipped, as
// is one without a start or an end, and the 501st. Node counted every
// session sent, added the durations with JavaScript's +, answered 500 for
// a session that is null and for a length that cannot be made text in
// the log line, the latter after the summary was stored (BUGS AI16).
func TestTheSummaryCountsWhatIsStored(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		tokenDeviceA(w)
		session := func(minute int, duration string) string {
			return fmt.Sprintf(`{"started_at":"2011-03-04T09:%02d:00.000Z","ended_at":"2011-03-04T09:%02d:30.000Z","duration_secs":%s}`, minute, minute, duration)
		}
		many := make([]string, 501)
		for i := range many {
			many[i] = session(i%60, "1")
		}
		for _, c := range []struct {
			name, sessions string
			count, total   int
		}{
			{"a null session", `[null,` + session(1, "30") + `]`, 1, 30},
			{"sessions that are no objects", `[5,"x",true,[],` + session(2, "20") + `]`, 1, 20},
			{"a session without an end", `[{"started_at":"2011-03-04T09:03:00.000Z","duration_secs":99},` + session(4, "10") + `]`, 1, 10},
			{"durations as text", `[` + session(5, `"60"`) + `,` + session(6, `" 7 "`) + `]`, 2, 67},
			{"sessions not stored whose durations are no numbers", `[{"duration_secs":{"toString":1}},{"duration_secs":"06a"},` + session(10, "5") + `]`, 1, 5},
			{"501 sessions", `[` + strings.Join(many, ",") + `]`, 500, 500},
			{"a length that is no number", `{"length":{"toString":1}}`, 0, 0},
		} {
			w.Exec("DELETE FROM activity_summaries", nil)
			body := `{"device_id":"device-a","sessions":` + c.sessions + `,"idle_seconds":5,"timestamp":"2011-03-04T10:00:00.000Z"}`
			if got := w.Do(replay.Request{Method: "POST", Path: "/api/nd/activity", Body: replay.Text(body), Headers: deviceA}); got.Status != 200 || got.Body != `{"success":true}` {
				t.Errorf("%s: %d %s, want it stored", c.name, got.Status, got.Body)
			}
			want := fmt.Sprintf(`[{"session_count":%d,"total_active_secs":%d}]`, c.count, c.total)
			if got := w.Rows("SELECT session_count, total_active_secs FROM activity_summaries", nil); got != want {
				t.Errorf("%s: the summary %s, want %s", c.name, got, want)
			}
		}
	})
}

// A report is stored for the device of its token. The guard has compared
// every device the request names with the token's, so a report that names
// its device only in the query or in X-Device-Id, or nowhere, is stored,
// where Node asked for the body's device_id (400 "Missing device_id"). A
// device_id that is no text or another device's is the guard's refusal,
// without success, before the route (BUGS AI17).
func TestTheReportTakesTheTokensDevice(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		tokenDeviceA(w)
		report := func(deviceID, timestamp string) *string {
			return replay.Text(`{` + deviceID + `"sessions":[],"idle_seconds":5,"timestamp":"` + timestamp + `"}`)
		}
		for _, c := range []struct {
			name, path, timestamp, deviceID string
			headers                         map[string]string
		}{
			{"only in the query", "/api/nd/activity?device_id=device-a", "2011-03-04T10:01:00.000Z", "", deviceA},
			{"only in X-Device-Id", "/api/nd/activity", "2011-03-04T10:02:00.000Z", "", map[string]string{"Authorization": "Bearer tok-device-a", "X-Device-Id": "device-a"}},
			{"nowhere", "/api/activity/activity", "2011-03-04T10:03:00.000Z", "", deviceA},
			{"as empty text", "/api/nd/activity", "2011-03-04T10:04:00.000Z", `"device_id":"",`, deviceA},
			{"as null", "/api/nd/activity", "2011-03-04T10:05:00.000Z", `"device_id":null,`, deviceA},
		} {
			got := w.Do(replay.Request{Method: "POST", Path: c.path, Body: report(c.deviceID, c.timestamp), Headers: c.headers})
			if got.Status != 200 || got.Body != `{"success":true}` {
				t.Errorf("a report naming its device %s: %d %s, want it stored", c.name, got.Status, got.Body)
			}
		}
		for _, deviceID := range []string{`"device_id":["device-a"],`, `"device_id":5,`, `"device_id":"device-b",`, `"device_id":{"toString":1},`} {
			got := w.Do(replay.Request{Method: "POST", Path: "/api/nd/activity", Body: report(deviceID, "2011-03-04T11:00:00.000Z"), Headers: deviceA})
			if got.Status != 403 || got.Body != `{"error":"Device ID mismatch"}` {
				t.Errorf("a report with %s: %d %s, want the guard's 403", deviceID, got.Status, got.Body)
			}
		}
		want := `[{"device_id":"device-a","n":5}]`
		if got := w.Rows("SELECT device_id, COUNT(*) AS n FROM activity_summaries GROUP BY device_id", nil); got != want {
			t.Errorf("the summaries: %s, want %s", got, want)
		}
	})
}
