package inventory_test

import (
	"encoding/json"
	"regexp"
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
//
// The steps a bug fix changed are on the list deviceFixes.
// The steps the device guards answer now (BUGS AI12) are checked by
// guardedByKit, and withoutShortTokens changes the rows read after them.
// The statements of the info after the uploads are checked by keptInfo
// (BUGS AI13), the uploads refused for the types of their values and the
// statements after them by typedUploads and typedRows (BUGS AI15, both in
// stored_test.go), and the other uploads whose answer a fix changes by
// changedUpload (fixes_test.go).
func TestNodeDeviceAnswers(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		engine := w.Server.Deps().Alerts
		trimmed, elsewhere, guarded := 0, 0, 0
		rec := replay.Load(t, db, "node_device")
		withoutShortTokens(&rec, db)
		byGuards := guardedByKit(tokensOf(rec), &guarded)
		// The copies find only the deleted row of the seed, as it was
		kept, differ := keptInfo(&rec, w, `[{"id":"del-1","uuid":"","status_online":0}]`)
		changed, changedDone := changedUpload(db)
		rows, rowsDiffer := typedRows(&rec, w)
		typedCount := 0
		typed := typedUploads(&typedCount)
		w.Run(rec, deviceFixes.Check(w, func(t *testing.T, step replay.Step, got replay.Answer) bool {
			engine.Wait()
			if kept(t, step, got) || rows(t, step, got) || changed(t, step, got) {
				return true
			}
			if byGuards(t, step, got) || typed(t, step, got) {
				return true
			}
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
		}))
		deviceFixes.Unused(t, db)
		if trimmed != 6 || elsewhere != 1 {
			t.Errorf("%d requests were sent again with a header trimmed and %d answers compared by status only, want 6 and 1", trimmed, elsewhere)
		}
		if guarded == 0 {
			t.Errorf("no answer was the device guards'")
		}
		changedDone(t)
		if want := map[bool]int{false: 129, true: 70}[db.IsPostgres()]; typedCount != 109 || *rowsDiffer != want {
			t.Errorf("%d uploads were refused for their types and %d statements read rows that are no longer Node's, want 109 and %d", typedCount, *rowsDiffer, want)
		}
		if want := map[bool]int{false: 98, true: 38}[db.IsPostgres()]; *differ != want {
			t.Errorf("%d statements of the info differ from Node's, want %d", *differ, want)
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

// A device that writes with its token and no session needs no CSRF token
// at either mount (BUGS DV2). A signed-in browser needs the token at both
// mounts. No role is refused by
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
			{"inventory at /api/inventory without a session", "", "/api/inventory/inventory", report, 200, stored},
			{"telemetry at /api/inventory without a session", "", "/api/inventory/telemetry", telemetry, 200, stored},
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

// The three routes of the desktop agent are behind the device guards of
// the other device routes (BUGS AI12): a token bound to a client id the
// guards take for no device is refused, and the device a request names
// in the body, the query or X-Device-Id is compared with the token's as
// text, so a number or a list names another device.
func TestInventoryUsesTheDeviceGuards(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		tokenDeviceA(w)
		w.Exec("INSERT INTO access_tokens (token, token_hash, user_id, client_id, expires_at) VALUES (?, ?, 5, 'x', '2999-01-01 00:00:00')",
			[]any{"tok-short", "7c7b85d90f624dbe2f9196551fdac64191dc4a52460bd9b5d4b938db6b445b1f"})
		short := map[string]string{"Authorization": "Bearer tok-short"}
		unbound := `{"error":"Invalid or unbound access token"}`
		mismatch := `{"error":"Device ID mismatch"}`
		for _, c := range []struct {
			name   string
			req    replay.Request
			status int
			answer string
		}{
			{"a report with a token of the client id x", replay.Request{Method: "POST", Path: "/api/bd/inventory", Body: replay.Text(`{"device_id":"x","hardware":{"hostname":"pc"}}`), Headers: short}, 401, unbound},
			{"telemetry with a token of the client id x", replay.Request{Method: "POST", Path: "/api/inventory/telemetry", Body: replay.Text(`{"device_id":"x"}`), Headers: short}, 401, unbound},
			{"the report of x with its token", replay.Request{Path: "/api/bd/inventory/x", Headers: short}, 401, unbound},
			{"a device id that is a number", replay.Request{Method: "POST", Path: "/api/bd/inventory", Body: replay.Text(`{"device_id":900100001,"hardware":{"hostname":"pc"}}`), Headers: deviceA}, 403, mismatch},
			{"a device id in a list", replay.Request{Method: "POST", Path: "/api/inventory/inventory", Body: replay.Text(`{"device_id":["device-a"],"hardware":{"hostname":"pc"}}`), Headers: deviceA}, 403, mismatch},
			{"telemetry of a device id that is a number", replay.Request{Method: "POST", Path: "/api/bd/telemetry", Body: replay.Text(`{"device_id":5}`), Headers: deviceA}, 403, mismatch},
			{"another device in the query", replay.Request{Method: "POST", Path: "/api/bd/inventory?device_id=device-b", Body: replay.Text(`{"device_id":"","hardware":{"hostname":"pc"}}`), Headers: deviceA}, 403, mismatch},
			{"another device in the header", replay.Request{Path: "/api/bd/inventory/device-a", Headers: map[string]string{"Authorization": "Bearer tok-device-a", "X-Device-Id": "device-b"}}, 403, mismatch},
			{"its own report", replay.Request{Method: "POST", Path: "/api/bd/inventory", Body: replay.Text(`{"device_id":"device-a","hardware":{"hostname":"pc"}}`), Headers: deviceA}, 200, `{"success":true}`},
		} {
			if got := w.Do(c.req); got.Status != c.status || got.Body != c.answer {
				t.Errorf("%s: %d %s, want %d %s", c.name, got.Status, got.Body, c.status, c.answer)
			}
		}
		if rows := w.Rows("SELECT device_id FROM device_inventory", nil); rows != `[{"device_id":"device-a"}]` {
			t.Errorf("the reports stored: %s", rows)
		}
	})
}

// A report and telemetry are stored for the device of their token. The
// guards have compared every device the request names with the token's,
// so an upload that names its device only in the query or in X-Device-Id,
// or nowhere, is stored, where Node asked for the body's device_id (400
// "Missing device_id or hardware", "Missing device_id"). A report still
// needs its hardware, and telemetry whose body is no JSON object is no
// telemetry (BUGS AI18).
func TestUploadsTakeTheTokensDevice(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		tokenDeviceA(w)
		hardware := `"hardware":{"hostname":"pc-a"}`
		for _, c := range []struct {
			name, path, body string
			headers          map[string]string
			status           int
			answer           string
		}{
			{"a report naming its device nowhere", "/api/bd/inventory", `{` + hardware + `}`, deviceA, 200, `{"success":true}`},
			{"a report naming it only in the query", "/api/inventory/inventory?device_id=device-a", `{` + hardware + `}`, deviceA, 200, `{"success":true}`},
			{"a report naming it only in X-Device-Id", "/api/bd/inventory", `{` + hardware + `}`, map[string]string{"Authorization": "Bearer tok-device-a", "X-Device-Id": "device-a"}, 200, `{"success":true}`},
			{"a report with device_id null", "/api/bd/inventory", `{"device_id":null,` + hardware + `}`, deviceA, 200, `{"success":true}`},
			{"a report with device_id empty", "/api/bd/inventory", `{"device_id":"",` + hardware + `}`, deviceA, 200, `{"success":true}`},
			{"a report without hardware", "/api/bd/inventory", `{"device_id":"device-a"}`, deviceA, 400, `{"success":false,"error":"Missing device_id or hardware"}`},
			{"a report in a list", "/api/bd/inventory", `[{` + hardware + `}]`, deviceA, 400, `{"success":false,"error":"Missing device_id or hardware"}`},
			{"telemetry naming its device nowhere", "/api/bd/telemetry", `{"cpu_usage_percent":5}`, deviceA, 200, `{"success":true}`},
			{"telemetry of nothing", "/api/inventory/telemetry", `{}`, deviceA, 200, `{"success":true}`},
			{"telemetry with device_id null", "/api/bd/telemetry", `{"device_id":null,"cpu_usage_percent":7}`, deviceA, 200, `{"success":true}`},
			{"telemetry in a list", "/api/bd/telemetry", `[{"cpu_usage_percent":5}]`, deviceA, 400, `{"success":false,"error":"Invalid telemetry"}`},
		} {
			got := w.Do(replay.Request{Method: "POST", Path: c.path, Body: replay.Text(c.body), Headers: c.headers})
			if got.Status != c.status || got.Body != c.answer {
				t.Errorf("%s: %d %s, want %d %s", c.name, got.Status, got.Body, c.status, c.answer)
			}
		}
		if rows := w.Rows("SELECT device_id FROM device_inventory", nil); rows != `[{"device_id":"device-a"}]` {
			t.Errorf("the reports stored: %s", rows)
		}
		if rows := w.Rows("SELECT device_id, cpu_usage_percent FROM device_telemetry", nil); rows != `[{"device_id":"device-a","cpu_usage_percent":7}]` {
			t.Errorf("the telemetry stored: %s", rows)
		}
	})
}

// tokensOf reads the devices the tokens of a recording's seed are bound
// to, by token.
func tokensOf(rec replay.Recording) map[string]string {
	tokens := map[string]string{}
	for _, seed := range rec.Seed {
		if strings.HasPrefix(seed.SQL, "INSERT INTO access_tokens (token,") {
			token, _ := seed.Params[0].(string)
			device, _ := seed.Params[3].(string)
			tokens[token] = device
		}
	}
	return tokens
}

// guardedByKit checks the steps the device guards of package kit answer
// since the routes are behind them (BUGS AI12), and counts them:
//
//   - A token bound to a client id the guards take for no device (x, "dev
//     a") is refused like an unbound one.
//   - A request that names another device than its token's, or names it
//     with a value that is no text (0, false, a number, a list), gets the
//     guard's refusal, without success, also where Node answered first
//     that the body lacked something.
func guardedByKit(tokens map[string]string, count *int) replay.Check {
	return func(t *testing.T, step replay.Step, got replay.Answer) bool {
		token := strings.TrimPrefix(step.Headers["Authorization"], "Bearer ")
		want, status := "", 0
		switch {
		case token == "tok-short" || token == "tok-spaced":
			want, status = `{"error":"Invalid or unbound access token"}`, 401
		case step.Method == "POST" && tokens[token] != "" && (step.Answer == `{"success":false,"error":"Device ID mismatch"}` || step.Status == 400 && namesAnother(step, tokens[token])):
			want, status = `{"error":"Device ID mismatch"}`, 403
		default:
			return false
		}
		*count++
		if got.Status != status || got.Body != want {
			t.Errorf("%s (AI12): %d %s, want %d %s", step.Name, got.Status, got.Body, status, want)
		}
		return true
	}
}

// namesAnother reports whether the body of a step names a device that is
// not the token's: with text of another device, or a value that is no
// text but names one for the guards (anything but null and "").
func namesAnother(step replay.Step, device string) bool {
	if step.Body == nil {
		return false
	}
	var body map[string]any
	if json.Unmarshal([]byte(*step.Body), &body) != nil {
		return false
	}
	claim, given := body["device_id"]
	switch v := claim.(type) {
	case nil:
		return false
	case string:
		return v != "" && v != device
	}
	return given
}

// shortTokenAlerts are the alerts Node raised for the reports of the
// tokens bound to x and "dev a", which the guards refuse.
var shortTokenAlerts = regexp.MustCompile(`,?\{"rule_id":[0-9]+,"device_id":"(x|dev a)","severity":"[^"]*","message":"[^"]*"\}`)

// withoutShortTokens changes the rows the recording reads after the
// requests of the tokens bound to x and "dev a" (BUGS AI12) into the rows
// the guards leave: the tokens were not used, x keeps its info, and no
// alert was raised for those reports.
func withoutShortTokens(rec *replay.Recording, db *store.DB) {
	for i, step := range rec.Steps {
		switch step.Name {
		case "the tokens were noted":
			for _, token := range []string{"tok-short", "tok-spaced"} {
				rows := rec.Steps[i].Rows
				rows = strings.Replace(rows, `{"token":"`+token+`","used":1}`, `{"token":"`+token+`","used":0}`, 1)
				rec.Steps[i].Rows = strings.Replace(rows, `{"token":"`+token+`","used":true}`, `{"token":"`+token+`","used":false}`, 1)
			}
		case "the info of x":
			rec.Steps[i].Rows = `[{"id":"x","note":"","info":"{\"hostname\":` + db.Pick(``, ` `) + `\"short\"}","is_deleted":0}]`
		case "the alerts raised":
			rec.Steps[i].Rows = strings.Replace(shortTokenAlerts.ReplaceAllString(step.Rows, ""), "[,", "[", 1)
		}
	}
}
