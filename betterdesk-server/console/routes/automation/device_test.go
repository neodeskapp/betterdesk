package automation_test

import (
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/routes/internal/replay"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// TestNodeDeviceAnswers replays what the Node console's two device routes
// answered with its real database adapter and device guard, on SQLite and
// on PostgreSQL (testdata/node_device_sqlite.json,
// testdata/node_device_postgres.json), and the rows they left: tokens in
// every state at both mounts, the pending commands of a device, the
// status checked before the command and the command before its device,
// results of every JSON type, ids in every spelling, and every status
// after every other.
//
// Two kinds of answers are not compared byte for byte:
//
//   - Where the recording's stand-in for the rest of the console answered
//     (no route for the address, a body that is no JSON), only the status
//     is compared: the console has its own answers for those.
//   - An address with two slashes in a row is no address of the Go
//     console's route table, which answers 404.
//
// The steps the bug fixes change are listed in deviceFixes
// (fixed_test.go). One fix changes a whole kind of answer: a report Node
// took on a command that had completed or failed is 409, and a result
// that is no text is stored as its JSON (BUGS AU13). There the command
// must have finished, and the row is then made what Node's report made
// it, so the reads after it compare.
func TestNodeDeviceAnswers(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		elsewhere, slashes, refused := 0, 0, 0
		defer deviceFixes.Unused(t, db)
		w.Run(replay.Load(t, db, "node_device"), deviceFixes.Check(w, func(t *testing.T, step replay.Step, got replay.Answer) bool {
			switch {
			case step.Method == "POST" && step.Status == 200 && strings.HasSuffix(strings.ToLower(strings.TrimSuffix(step.Path, "/")), "/result"):
				if got.Status == 409 && got.Body == `{"error":"Command already finished"}` {
					refused++
					parts := strings.Split(strings.TrimSuffix(step.Path, "/"), "/")
					if status := w.Rows("SELECT status FROM remote_commands WHERE id = ?", []any{int64(jsbind.ParseNumber(parts[len(parts)-2]))}); status != `[{"status":"completed"}]` && status != `[{"status":"failed"}]` {
						t.Errorf("%s: refused for a command that is %s", step.Name, status)
					}
				} else if got.Status != 200 || replay.Normalize(got.Body) != replay.Normalize(step.Answer) {
					t.Errorf("%s: %d %s, Node answered %d %s", step.Name, got.Status, got.Body, step.Status, step.Answer)
				}
				nodeResult(t, w, step)
				return true
			case strings.Contains(step.Path, "//"):
				slashes++
				if got.Status != 404 {
					t.Errorf("%s: status %d, want 404", step.Name, got.Status)
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
		if elsewhere != 6 || slashes != 1 {
			t.Errorf("%d answers were compared by status only and %d had two slashes, want 6 and 1", elsewhere, slashes)
		}
		if refused != finishedReports {
			t.Errorf("%d reports on finished commands were refused, want %d", refused, finishedReports)
		}
	})
}

// finishedReports is the number of reports the device recording sends for
// a command that completed or failed, on each database.
const finishedReports = 6

// deviceWorld is a console with a token bound to device-a (tok-device-a)
// and one pending command of device-a, with the id 1.
func deviceWorld(t *testing.T, db *store.DB) *replay.World {
	t.Helper()
	w := replay.NewWorld(t, db, nil)
	w.Exec("INSERT INTO access_tokens (token, token_hash, user_id, client_id, expires_at) VALUES (?, ?, 5, 'device-a', '2999-01-01 00:00:00')",
		[]any{"tok-device-a", "a70dc32ded6e642ec955672a88edb7cc9d6d9e2abb6bf78146a63491adb24155"})
	w.Exec("INSERT INTO remote_commands (device_id, payload, created_by) VALUES ('device-a', 'whoami', 'e2e_admin')", nil)
	return w
}

var deviceA = map[string]string{"Authorization": "Bearer tok-device-a"}

// A device that reports a result with its token and no session needs no
// CSRF token at either mount (BUGS DV2). A signed-in browser needs the
// token at both mounts, and at
// /api/automation the panel access guard of the automation section comes
// first: it refuses a write from every role that may not edit devices and
// from a server admin.
func TestTheCSRFGateAtEachMount(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := deviceWorld(t, db)
		result := replay.Text(`{"status":"running"}`)
		refused := `{"success":false,"error":"Invalid CSRF token. Please refresh the page and try again. If you recently disabled SSL, clear your browser cookies for this site and reload."}`
		stored := `{"success":true}`
		for _, c := range []struct {
			name, role, path string
			status           int
			answer           string
		}{
			{"at /api/bd without a session", "", "/api/bd/commands/1/result", 200, stored},
			{"at /api/automation without a session", "", "/api/automation/commands/1/result", 200, stored},
			{"at /api/bd with a session", "operator", "/api/bd/commands/1/result", 403, refused},
			{"at /api/automation with a session", "admin", "/api/automation/commands/1/result", 403, refused},
		} {
			got := w.Do(replay.Request{Role: c.role, Method: "POST", Path: c.path, Body: result, Headers: deviceA, NoCSRF: true})
			if got.Status != c.status || got.Body != c.answer {
				t.Errorf("%s: %d %s, want %d %s", c.name, got.Status, got.Body, c.status, c.answer)
			}
		}
		guarded := `{"success":false,"error":"Panel access denied: automation","code":"panel_access_denied"}`
		for role, passes := range map[string]bool{
			"": true, "guest_issuer": false, "viewer": false, "operator": true, "global_admin": true, "server_admin": false, "super_admin": true, "admin": true,
		} {
			got := w.Do(replay.Request{Role: role, Method: "POST", Path: "/api/automation/commands/1/result", Body: result, Headers: deviceA})
			if passes && (got.Status != 200 || got.Body != stored) || !passes && (got.Status != 403 || got.Body != guarded) {
				t.Errorf("POST /api/automation/commands/1/result as %q with the CSRF token: %d %s, the guard lets it pass: %v", role, got.Status, got.Body, passes)
			}
			if got := w.Do(replay.Request{Role: role, Method: "POST", Path: "/api/bd/commands/1/result", Body: result, Headers: deviceA}); got.Status != 200 || got.Body != stored {
				t.Errorf("POST /api/bd/commands/1/result as %q with the CSRF token: %d %s", role, got.Status, got.Body)
			}
			// Reading needs no CSRF token
			if got := w.Do(replay.Request{Role: role, Path: "/api/bd/commands", Headers: deviceA}); got.Status != 200 || got.Body != `{"commands":[]}` {
				t.Errorf("GET /api/bd/commands as %q: %d %s", role, got.Status, got.Body)
			}
		}
	})
}

// A query that fails is the route's 500, {"error":"Internal server
// error"}. The checks in front of the command answer as before.
func TestDeviceRouteFailures(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := deviceWorld(t, db)
		if got := w.Do(replay.Request{Path: "/api/bd/commands", Headers: deviceA}); got.Status != 200 || !strings.Contains(got.Body, `"payload":"whoami"`) {
			t.Fatalf("the pending command: %d %s", got.Status, got.Body)
		}
		refuseCommandUpdates(w)
		failed := `{"error":"Internal server error"}`
		if got := w.Do(replay.Request{Method: "POST", Path: "/api/bd/commands/1/result", Body: replay.Text(`{"status":"failed"}`), Headers: deviceA}); got.Status != 500 || got.Body != failed {
			t.Errorf("a result the database refuses: %d %s, want 500 %s", got.Status, got.Body, failed)
		}
		w.Exec("DROP TABLE remote_commands", nil)
		for _, req := range []replay.Request{
			{Path: "/api/bd/commands", Headers: deviceA},
			{Method: "POST", Path: "/api/bd/commands/1/result", Body: replay.Text(`{"status":"completed"}`), Headers: deviceA},
		} {
			if got := w.Do(req); got.Status != 500 || got.Body != failed {
				t.Errorf("%s %s without the table: %d %s, want 500 %s", req.Method, req.Path, got.Status, got.Body, failed)
			}
		}
		if got := w.Do(replay.Request{Method: "POST", Path: "/api/bd/commands/1/result", Body: replay.Text(`{"status":"done"}`), Headers: deviceA}); got.Status != 400 {
			t.Errorf("a status that is none without the table: %d %s, want 400", got.Status, got.Body)
		}
	})
}

// A device that reports a result for another device's command gets the
// answer of a command nobody has, so it cannot learn which ids exist, and
// the command stays as it was (BUGS AU14). A status that is none is still
// refused first.
func TestAnotherDevicesCommandIsNotFound(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := deviceWorld(t, db)
		w.Exec("INSERT INTO access_tokens (token, token_hash, user_id, client_id, expires_at) VALUES (?, ?, 5, 'device-b', '2999-01-01 00:00:00')",
			[]any{"tok-device-b", "638c3bcb28bd43d785bdb2b9555c0d22e2eb931e3a0298384de110a801b52b46"})
		deviceB := map[string]string{"Authorization": "Bearer tok-device-b"}
		notFound := `{"error":"Command not found"}`
		for _, path := range []string{"/api/bd/commands/1/result", "/api/bd/commands/999/result"} {
			for _, status := range []string{"running", "completed", "failed"} {
				got := w.Do(replay.Request{Method: "POST", Path: path, Body: replay.Text(`{"status":"` + status + `","result":"x"}`), Headers: deviceB})
				if got.Status != 404 || got.Body != notFound {
					t.Errorf("%s as device-b with %s: %d %s, want 404 %s", path, status, got.Status, got.Body, notFound)
				}
			}
		}
		if got := w.Do(replay.Request{Method: "POST", Path: "/api/bd/commands/1/result", Body: replay.Text(`{"status":"done"}`), Headers: deviceB}); got.Status != 400 {
			t.Errorf("a status that is none for another device's command: %d %s, want 400", got.Status, got.Body)
		}
		if got := w.Rows("SELECT status, result, executed_at, completed_at FROM remote_commands", nil); got != `[{"status":"pending","result":null,"executed_at":null,"completed_at":null}]` {
			t.Errorf("the command after device-b's reports: %s", got)
		}
	})
}

// refuseCommandUpdates makes every UPDATE of remote_commands fail.
func refuseCommandUpdates(w *replay.World) {
	if w.DB.IsPostgres() {
		w.Exec(`CREATE FUNCTION refuse_update() RETURNS trigger AS $body$ BEGIN RAISE EXCEPTION 'no updates'; END $body$ LANGUAGE plpgsql`, nil)
		w.Exec(`CREATE TRIGGER refuse_update BEFORE UPDATE ON remote_commands FOR EACH ROW EXECUTE FUNCTION refuse_update()`, nil)
		return
	}
	w.Exec(`CREATE TRIGGER refuse_update BEFORE UPDATE ON remote_commands BEGIN SELECT RAISE(ABORT, 'no updates'); END`, nil)
}
