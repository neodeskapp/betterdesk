package automation_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/replay"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// TestNodeAnswers replays what the Node console's ten panel routes
// answered with its real database adapter, on SQLite and on PostgreSQL
// (testdata/node_sqlite.json, testdata/node_postgres.json): every route
// for every role, every field of a rule and of a command with values of
// every JSON type, ids in every spelling, the filters and the limit of
// both lists in every form a query string can give them, rows with values
// only SQLite can hold, and at the end the rules, alerts and commands that
// are left and the audit log.
//
// Five kinds of answers are not compared byte for byte:
//
//   - A command list has has_result in place of each command's result
//     (BUGS RV39); sameCommandList compares it with Node's that way.
//   - The recording was made without the panel access guard, which sits
//     in front of the routes in the console. It asks server.config, as
//     the routes do (PA3 F8), and refuses every request of a role without
//     it before the route's own permission check answers; the recording's
//     requests of those roles changed nothing either. The guard's answer
//     is taken there.
//   - Where the recording's stand-in for the rest of the console answered
//     (no route for the address, a body that is no JSON), only the status
//     is compared: the console has its own answers for those.
//   - An address with two slashes in a row is no address of the Go
//     console's route table. Express took one of them for the end of the
//     mount, so /api/automation//rules was the rule list.
//   - An id that does not decode to text (%C3%28) is no id of digits, so
//     the routes pass it on (BUGS AU11) and the recording's stand-in for
//     the rest of the console said 404. The console's own last answer for
//     an address that does not decode is 400, as the old console's catch-all
//     page route answered.
//
// The steps the bug fixes change are listed in panelFixes
// (fixed_test.go). One fix changes a whole kind of answer: on SQLite a
// new rule or command is answered with the stored row, where Node
// answered with the values of the request (BUGS AU3). There the id must
// be Node's and the answer what a read of the row gives.
//
// On PostgreSQL the replay keeps the limits of the rules in four bytes,
// as Node's column did (nodeLimitColumn); the console's column has eight
// (BUGS AE4).
func TestNodeAnswers(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newWorld(t, db)
		guarded, elsewhere, slashes, undecoded, created, lists := 0, 0, 0, 0, 0, 0
		defer panelFixes.Unused(t, db)
		rec := replay.Load(t, db, "node")
		if db.IsPostgres() {
			rec.Seed = append(rec.Seed, nodeLimitColumn...)
		}
		w.Run(rec, panelFixes.Check(w, func(t *testing.T, step replay.Step, got replay.Answer) bool {
			switch {
			case step.Method == "GET" && step.Status == 200 && isCommandList(step.Path):
				lists++
				if got.Status != 200 || !sameCommandList(t, step.Answer, got.Body) {
					t.Errorf("%s as %s: %d %s\nNode, with has_result for result: %s", step.Name, step.Role, got.Status, got.Body, step.Answer)
				}
				return true
			case !db.IsPostgres() && step.Method == "POST" && step.Status == 201 && got.Status == 201:
				created++
				answersTheRow(t, w, step, got)
				return true
			case got.Status == 403 && strings.Contains(got.Body, `"code":"panel_access_denied"`) &&
				(step.Role == "guest_issuer" || step.Role == "viewer" || step.Role == "operator" || step.Role == "global_admin"):
				guarded++
				// The recording must not have changed anything there either
				if step.Status < 300 {
					t.Errorf("%s as %s: the guard refuses a request that went through in the recording (%d %s)", step.Name, step.Role, step.Status, step.Answer)
				}
				return true
			case strings.Contains(step.Path, "//") && step.Status == 200:
				slashes++
				if got.Status != 404 {
					t.Errorf("%s: status %d, want 404", step.Name, got.Status)
				}
				return true
			case strings.Contains(step.Path, "%C3%28"):
				undecoded++
				if got.Status != 400 || step.Status != 404 {
					t.Errorf("%s: status %d, want 400 (the recording has %d)", step.Name, got.Status, step.Status)
				}
				return true
			case step.Answer == `{"oracle":"no route"}` || step.Answer == `{"success":false,"error":"Internal Server Error"}`:
				elsewhere++
				if got.Status != step.Status {
					t.Errorf("%s: status %d, want %d", step.Name, got.Status, step.Status)
				}
				return true
			}
			return false
		}))
		if guarded != 54 {
			t.Errorf("%d answers were the panel access guard's, want 54", guarded)
		}
		if elsewhere != 165 || slashes != 1 || undecoded != 1 {
			t.Errorf("%d answers were compared by status only, %d had two slashes and %d an id that is no text, want 165, 1 and 1", elsewhere, slashes, undecoded)
		}
		want := createdOnSQLite
		if db.IsPostgres() {
			want = 0
		}
		if created != want {
			t.Errorf("%d new rules and commands were answered with the stored row, want %d", created, want)
		}
		if lists < 20 {
			t.Errorf("%d command lists were compared without their results, want at least 20", lists)
		}
	})
}

// isCommandList reports whether a path is the command list of the panel
// routes, in any letter case and with or without a slash at the end.
func isCommandList(path string) bool {
	path, _, _ = strings.Cut(path, "?")
	return strings.EqualFold(strings.TrimSuffix(path, "/"), "/api/automation/commands")
}

// sameCommandList compares a command list of the Go console with Node's
// (BUGS RV39): Node's commands carry their result, the Go console's say
// with has_result whether there is one that is not empty. Otherwise the
// two lists are the same.
func sameCommandList(t *testing.T, node, goBody string) bool {
	t.Helper()
	read := func(body string) map[string]any {
		var list map[string]any
		decoder := json.NewDecoder(strings.NewReader(replay.Normalize(body)))
		decoder.UseNumber()
		if err := decoder.Decode(&list); err != nil {
			t.Fatalf("a command list that is no JSON: %v: %s", err, body)
		}
		return list
	}
	want, got := read(node), read(goBody)
	commands, _ := want["commands"].([]any)
	for _, c := range commands {
		command, _ := c.(map[string]any)
		result, has := command["result"]
		if !has {
			return false
		}
		delete(command, "result")
		text, isText := result.(string)
		command["has_result"] = result != nil && (!isText || text != "")
	}
	return reflect.DeepEqual(want, got)
}

// createdOnSQLite is the number of rules and commands the recording on
// SQLite creates.
const createdOnSQLite = 297

// answersTheRow checks the answer to a new rule or command on SQLite: it
// has Node's id and is the row as a read of it gives it.
func answersTheRow(t *testing.T, w *replay.World, step replay.Step, got replay.Answer) {
	t.Helper()
	kind, list := "rule", "rules"
	if strings.HasSuffix(strings.ToLower(step.Path), "/commands") {
		kind, list = "command", "commands"
	}
	idOf := func(body string) float64 {
		var members map[string]json.RawMessage
		var row struct {
			ID float64 `json:"id"`
		}
		if json.Unmarshal([]byte(body), &members) != nil || json.Unmarshal(members[kind], &row) != nil {
			return 0
		}
		return row.ID
	}
	id := idOf(got.Body)
	if id == 0 || id != idOf(step.Answer) {
		t.Errorf("%s: %s, Node answered %s", step.Name, got.Body, step.Answer)
		return
	}
	read := w.Do(replay.Request{Role: "admin", Path: "/api/automation/" + list + "/" + strconv.Itoa(int(id))})
	if want := `{"success":true,"` + kind + `":` + read.Body + `}`; read.Status != 200 || got.Body != want {
		t.Errorf("%s: %s, want the row %s", step.Name, got.Body, read.Body)
	}
}

// The console has the group's routes at /api/automation and at /api/nd,
// in this order and in the order of the old file: the thirteen panel
// routes, with the device routes after GET commands/{id}: both at /api/nd,
// the result route alone at /api/automation (BUGS I5). At /api/nd the
// panel routes step aside, and nothing else of this console answers their
// addresses there. The device routes ask for a device token, a session
// does not stand for one.
func TestTheRoutesAtBothMounts(t *testing.T) {
	w := replay.NewWorld(t, storetest.Migrated(t), nil)
	var registered []string
	for _, route := range w.Server.Router().Routes() {
		for _, mount := range []string{"/api/automation", "/api/nd"} {
			for _, part := range []string{"/rules", "/alerts", "/commands", "/smtp"} {
				if strings.HasPrefix(route.Path, mount+part) {
					registered = append(registered, route.Method+" "+route.Path)
				}
			}
		}
	}
	var want []string
	for _, mount := range []string{"/api/automation", "/api/nd"} {
		for _, route := range []string{
			"GET %s/rules", "POST %s/rules", "GET %s/rules/{id}", "PATCH %s/rules/{id}", "DELETE %s/rules/{id}",
			"GET %s/alerts", "POST %s/alerts/{id}/ack",
			"POST %s/commands", "GET %s/commands", "GET %s/commands/{id}",
			"GET %s/commands (device)", "POST %s/commands/{id}/result",
			"GET %s/smtp", "PUT %s/smtp", "POST %s/smtp/test",
		} {
			if strings.HasSuffix(route, " (device)") {
				if mount != "/api/nd" {
					continue
				}
				route = strings.TrimSuffix(route, " (device)")
			}
			want = append(want, strings.Replace(route, "%s", mount, 1))
		}
	}
	if strings.Join(registered, "\n") != strings.Join(want, "\n") {
		t.Errorf("the routes in the order of the console:\n%s\nwant\n%s", strings.Join(registered, "\n"), strings.Join(want, "\n"))
	}

	body := replay.Text(`{"status":"completed","result":"x","host":"127.0.0.1"}`)
	refused := `{"error":"Bearer access token required"}`
	for _, req := range []replay.Request{
		{Role: "admin", Path: "/api/nd/commands"},
		{Role: "admin", Method: "POST", Path: "/api/nd/commands/1/result", Body: body},
		{Role: "admin", Method: "POST", Path: "/api/automation/commands/1/result", Body: body},
		{Path: "/api/nd/commands"},
	} {
		if got := w.Do(req); got.Status != 401 || got.Body != refused {
			t.Errorf("%s %s as %q: %d %s, want 401 %s", req.Method, req.Path, req.Role, got.Status, got.Body, refused)
		}
	}
	for _, req := range []replay.Request{
		{Role: "admin", Path: "/api/nd/smtp"},
		{Role: "admin", Method: "PUT", Path: "/api/nd/smtp", Body: body},
		{Role: "admin", Method: "POST", Path: "/api/nd/smtp/test", Body: body},
		{Role: "admin", Path: "/api/nd/rules"},
		{Role: "admin", Path: "/api/nd/alerts"},
		{Role: "admin", Path: "/api/nd/commands/1"},
		{Role: "admin", Method: "POST", Path: "/api/nd/rules", Body: replay.Text(`{"name":"x","condition_type":"custom"}`)},
		// An id that is not written in digits is no id of the routes
		{Role: "admin", Path: "/api/automation/rules/abc"},
		{Role: "admin", Path: "/api/automation/rules/%31"},
		{Role: "admin", Method: "PATCH", Path: "/api/automation/rules/1.0", Body: body},
		{Role: "admin", Method: "DELETE", Path: "/api/automation/rules/-1"},
		{Role: "admin", Method: "POST", Path: "/api/automation/alerts/abc/ack", Body: body},
		{Role: "admin", Path: "/api/automation/commands/abc"},
	} {
		if got := w.Do(req); got.Status != 404 || got.Body != `{"success":false,"error":"Not Found"}` {
			t.Errorf("%s %s: %d %s, want the console's 404", req.Method, req.Path, got.Status, got.Body)
		}
	}
	if rows := w.Rows("SELECT id FROM alert_rules", nil); rows != "[]" {
		t.Errorf("rules after the requests under /api/nd: %s", rows)
	}
}

// one sends a request per route of the group.
var one = []struct{ method, path, body string }{
	{"GET", "/rules", ""},
	{"POST", "/rules", `{"name":"x","condition_type":"custom"}`},
	{"GET", "/rules/1", ""},
	{"PATCH", "/rules/1", `{"description":"changed"}`},
	{"DELETE", "/rules/2", ""},
	{"GET", "/alerts", ""},
	{"POST", "/alerts/1/ack", `{}`},
	{"POST", "/commands", `{"device_id":"D1","payload":"whoami"}`},
	{"GET", "/commands", ""},
	{"GET", "/commands/1", ""},
}

func request(role, mount string, i int) replay.Request {
	req := replay.Request{Role: role, Method: one[i].method, Path: mount + one[i].path}
	if one[i].body != "" {
		req.Body = replay.Text(one[i].body)
	}
	return req
}

// seedOne puts two rules, an alert and a command into the database.
func seedOne(t *testing.T, db *store.DB) {
	t.Helper()
	for _, statement := range []string{
		`INSERT INTO alert_rules (name, condition_type) VALUES ('first', 'custom')`,
		`INSERT INTO alert_rules (name, condition_type) VALUES ('second', 'custom')`,
		`INSERT INTO alert_history (rule_id, device_id, message, triggered_at) VALUES (1, 'D1', 'fired', '2019-03-04T08:00:00.000Z')`,
		`INSERT INTO remote_commands (device_id, payload, created_by) VALUES ('D1', 'whoami', 'someone')`,
	} {
		if _, err := db.Exec(context.Background(), statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
}

// state is what the routes can change, as one text.
func state(w *replay.World) string {
	return w.Rows("SELECT id, description FROM alert_rules ORDER BY id", nil) +
		w.Rows("SELECT id, acknowledged_by FROM alert_history ORDER BY id", nil) +
		w.Rows("SELECT id, device_id FROM remote_commands ORDER BY id", nil) +
		w.Rows("SELECT action FROM audit_log ORDER BY id", nil)
}

// Every route asks for a session and for the permission server.config
// itself, and reads and writes nothing for an account without it.
//
// The panel access guard in front of the group asks server.config too,
// for reading and writing (PA3 F8), in any letter case of the address, as
// the routes answer (BUGS PA1): a role without the permission gets the
// guard's refusal, a viewer and a global admin, who may see devices,
// included. Signed out, an address in another spelling does not start
// with /api/ for the sign-in check, which sends the sign-in page.
func TestPermissions(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		seedOne(t, db)
		w := replay.NewWorldWithAPI(t, db, nil, replay.RoleOverrides{"operator": {"server.config": true}}, knownDevices)
		before := state(w)
		const guarded = `{"success":false,"error":"Panel access denied: automation","code":"panel_access_denied"}`
		for i, route := range one {
			name := route.method + " " + route.path

			got := w.Do(request("", "/api/automation", i))
			if want := `{"success":false,"error":"Unauthorized. Please log in.","code":"session_required"}`; got.Status != 401 || got.Body != want {
				t.Errorf("%s signed out: %d %s, want 401 %s", name, got.Status, got.Body, want)
			}
			if got := w.Do(request("", "/API/Automation", i)); got.Status != 302 || !strings.HasPrefix(got.Header.Get("Location"), "/login") {
				t.Errorf("%s signed out, in another spelling: %d to %q, want 302 to the sign-in page", name, got.Status, got.Header.Get("Location"))
			}
			// A global admin may see and edit devices, a viewer may see them;
			// both lack the permission
			for _, role := range []string{"global_admin", "viewer"} {
				for _, mount := range []string{"/api/automation", "/API/Automation"} {
					if got := w.Do(request(role, mount, i)); got.Status != 403 || got.Body != guarded {
						t.Errorf("%s as %s at %s: %d %s, want 403 %s", name, role, mount, got.Status, got.Body, guarded)
					}
				}
			}
		}
		if after := state(w); after != before {
			t.Errorf("the refused requests changed something:\nbefore %s\n after %s", before, after)
		}

		// With the permission every route answers, in either spelling: an
		// operator who was given it, and a server admin; the commands also
		// need device.connect, which the server admin has not (BUGS RV32)
		for i, route := range one {
			commands := strings.HasPrefix(route.path, "/commands")
			if commands {
				const denied = `{"success":false,"error":"Permission denied: device.connect"}`
				if got := w.Do(request("server_admin", "/api/automation", i)); got.Status != 403 || got.Body != denied {
					t.Errorf("%s %s as server_admin: %d %s, want 403 %s", route.method, route.path, got.Status, got.Body, denied)
				}
			}
			role, mount := "operator", "/api/automation"
			if i%2 == 1 {
				role, mount = "server_admin", "/API/Automation"
				if commands {
					role = "operator"
				}
			}
			want := 200
			if route.method == "POST" && !strings.HasSuffix(route.path, "/ack") {
				want = 201
			}
			if got := w.Do(request(role, mount, i)); got.Status != want {
				t.Errorf("%s %s as %s at %s: %d %s, want %d", route.method, route.path, role, mount, got.Status, got.Body, want)
			}
		}
		want := `[{"id":1,"description":"changed"},{"id":3,"description":""}]` +
			`[{"id":1,"acknowledged_by":"e2e_operator"}]` +
			`[{"id":1,"device_id":"D1"},{"id":2,"device_id":"D1"}]` +
			`[{"action":"alert_rule_created"},{"action":"alert_rule_deleted"},{"action":"remote_command_sent"}]`
		if after := state(w); after != want {
			t.Errorf("after the requests that went through:\n     %s\nwant %s", after, want)
		}
	})
}

// A new rule, a deleted rule and a sent command are written to the audit
// log with the account, the text the old console wrote and the client's
// address. Nothing else the routes do is.
func TestTheAuditLog(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		seedOne(t, db)
		w := newWorld(t, db)
		for _, req := range []replay.Request{
			{Role: "admin", Method: "POST", Path: "/api/automation/rules", Body: replay.Text(`{"name":"  Disk full ","condition_type":"disk_usage"}`)},
			{Role: "super_admin", Method: "PATCH", Path: "/api/automation/rules/3", Body: replay.Text(`{"name":"Disk nearly full"}`)},
			{Role: "super_admin", Method: "DELETE", Path: "/api/automation/rules/3"},
			{Role: "admin", Method: "DELETE", Path: "/api/automation/rules/3"},
			{Role: "admin", Method: "POST", Path: "/api/automation/alerts/1/ack", Body: replay.Text(`{}`)},
			{Role: "admin", Method: "POST", Path: "/api/automation/commands", Body: replay.Text(`{"device_id":"D7","payload":"uptime"}`)},
			{Role: "super_admin", Method: "POST", Path: "/api/automation/commands", Body: replay.Text(`{"device_id":"D8","payload":"x","command_type":"reboot"}`)},
			{Role: "admin", Method: "POST", Path: "/api/automation/rules", Body: replay.Text(`{"name":"","condition_type":"disk_usage"}`)},
			{Role: "admin", Method: "POST", Path: "/api/automation/commands", Body: replay.Text(`{"device_id":"D7","payload":"x","command_type":"nonsense"}`)},
			{Role: "admin", Path: "/api/automation/rules"},
		} {
			if got := w.Do(req); got.Status >= 500 {
				t.Fatalf("%s %s: %d %s", req.Method, req.Path, got.Status, got.Body)
			}
		}
		want := `[{"user_id":1,"action":"alert_rule_created","details":"Rule:   Disk full ","ip_address":"203.0.113.7"},` +
			`{"user_id":2,"action":"alert_rule_deleted","details":"Rule #3: Disk nearly full","ip_address":"203.0.113.7"},` +
			`{"user_id":1,"action":"remote_command_sent","details":"shell to D7","ip_address":"203.0.113.7"},` +
			`{"user_id":2,"action":"remote_command_sent","details":"reboot to D8","ip_address":"203.0.113.7"}]`
		if got := w.Rows("SELECT user_id, action, details, ip_address FROM audit_log ORDER BY id", nil); got != want {
			t.Errorf("the audit log:\n     %s\nwant %s", got, want)
		}
	})
}

// A rule keeps the state it was created with: off when the request says
// false, 0, "0" or "false", on for everything else and without the field.
func TestARuleKeepsTheStateItWasCreatedWith(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		for _, tt := range []struct {
			enabled string
			on      bool
		}{
			{`false`, false}, {`0`, false}, {`"0"`, false}, {`"false"`, false}, {`-0`, false}, {`0.0`, false},
			{`true`, true}, {`1`, true}, {`"1"`, true}, {`"true"`, true}, {`null`, true}, {`""`, true}, {`"no"`, true}, {`"off"`, true}, {`"False"`, true}, {`" 0"`, true}, {`"0.0"`, true},
			{``, true},
		} {
			body := `{"name":"CPU high","condition_type":"cpu_usage","condition_op":"gt","condition_value":90,"severity":"warning"`
			if tt.enabled != "" {
				body += `,"enabled":` + tt.enabled
			}
			got := w.Do(replay.Request{Role: "admin", Method: "POST", Path: "/api/automation/rules", Body: replay.Text(body + "}")})
			if got.Status != 201 {
				t.Fatalf("enabled %s: %d %s", tt.enabled, got.Status, got.Body)
			}
			stored := w.Rows("SELECT CASE WHEN enabled"+db.Pick(" = 1", "")+" THEN 1 ELSE 0 END AS enabled FROM alert_rules ORDER BY id DESC LIMIT 1", nil)
			if want := map[bool]string{true: `[{"enabled":1}]`, false: `[{"enabled":0}]`}[tt.on]; stored != want {
				t.Errorf("enabled %s: stored as %s, want %s", tt.enabled, stored, want)
			}
			// The answer is the stored row
			if want := map[bool]string{true: db.Pick(`"enabled":1`, `"enabled":true`), false: db.Pick(`"enabled":0`, `"enabled":false`)}[tt.on]; !strings.Contains(got.Body, want) {
				t.Errorf("enabled %s: answered %s, want %s", tt.enabled, got.Body, want)
			}
		}
	})
}

// A new rule and a new command are answered with the row as it is stored,
// on both databases: what a later read gives. SQLite answered with the
// values of the request (BUGS AU3).
func TestWhatANewRuleAndANewCommandAreAnsweredWith(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newWorld(t, db)
		created := w.Do(replay.Request{Role: "admin", Method: "POST", Path: "/api/automation/rules",
			Body: replay.Text(`{"name":" Types ","condition_type":"custom","description":"text","condition_value":12.5,"severity":"fatal","scope_device_id":"D7","cooldown_secs":900,"notify_emails":"","enabled":"0"}`)})
		read := w.Do(replay.Request{Role: "admin", Path: "/api/automation/rules/1"})
		want := db.Pick(
			`{"id":1,"name":"Types","description":"text","enabled":0,"condition_type":"custom","condition_op":"gt","condition_value":12.5,"severity":"warning","scope_device_id":"D7","cooldown_secs":900,"notify_emails":"","created_by":"e2e_admin","created_at":"<now, SQLite form>","updated_at":"<now, SQLite form>"}`,
			`{"id":1,"name":"Types","description":"text","enabled":false,"condition_type":"custom","condition_op":"gt","condition_value":12.5,"severity":"warning","scope_device_id":"D7","cooldown_secs":900,"notify_emails":"","created_by":"e2e_admin","created_at":"<now, ISO form>","updated_at":"<now, ISO form>"}`)
		if have := replay.Normalize(read.Body); read.Status != 200 || have != want {
			t.Errorf("the rule read back: %d %s\nwant 200 %s", read.Status, have, want)
		}
		if have := replay.Normalize(created.Body); created.Status != 201 || have != `{"success":true,"rule":`+want+`}` {
			t.Errorf("new rule: %d %s\nwant 201 with %s", created.Status, have, want)
		}

		created = w.Do(replay.Request{Role: "admin", Method: "POST", Path: "/api/automation/commands", Body: replay.Text(`{"device_id":"D7","payload":"  uptime "}`)})
		read = w.Do(replay.Request{Role: "admin", Path: "/api/automation/commands/1"})
		want = db.Pick(
			`{"id":1,"device_id":"D7","command_type":"shell","payload":"uptime","status":"pending","result":null,"created_by":"e2e_admin","created_at":"<now, SQLite form>","executed_at":null,"completed_at":null}`,
			`{"id":1,"device_id":"D7","command_type":"shell","payload":"uptime","status":"pending","result":null,"created_by":"e2e_admin","created_at":"<now, ISO form>","executed_at":null,"completed_at":null}`)
		if have := replay.Normalize(read.Body); read.Status != 200 || have != want {
			t.Errorf("the command read back: %d %s\nwant 200 %s", read.Status, have, want)
		}
		if have := replay.Normalize(created.Body); created.Status != 201 || have != `{"success":true,"command":`+want+`}` {
			t.Errorf("new command: %d %s\nwant 201 with %s", created.Status, have, want)
		}
	})
}

// A query that fails is a 500 with the text of every route's catch block.
func TestFailures(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		seedOne(t, db)
		w := newWorld(t, db)
		for _, statement := range []string{"DROP TABLE alert_history", "DROP TABLE alert_rules", "DROP TABLE remote_commands"} {
			if _, err := db.Exec(context.Background(), statement); err != nil {
				t.Fatal(err)
			}
		}
		for i, route := range one {
			got := w.Do(request("admin", "/api/automation", i))
			if want := `{"error":"Internal server error"}`; got.Status != 500 || got.Body != want {
				t.Errorf("%s %s without the tables: %d %s, want 500 %s", route.method, route.path, got.Status, got.Body, want)
			}
		}
	})
}
