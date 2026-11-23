package automation_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/replay"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// What the old console answered on the three SMTP routes, as the contract
// cases recorded it, here on both databases: nothing saved, a save, the
// masked password, the rule that keeps the stored password, the audit
// entry, and the test button with nothing saved and against a closed
// port. No mail leaves: the only address used is port 1 on loopback.
func TestSMTPSettings(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		const secret = "s3cret-not-real"
		do := func(role, method, path, body string) replay.Answer {
			t.Helper()
			req := replay.Request{Role: role, Method: method, Path: path}
			if body != "" {
				req.Body = replay.Text(body)
			}
			got := w.Do(req)
			if strings.Contains(got.Body, secret) {
				t.Errorf("%s %s as %s shows the password: %s", method, path, role, got.Body)
			}
			return got
		}
		expect := func(got replay.Answer, status int, body, what string) {
			t.Helper()
			if got.Status != status || got.Body != body {
				t.Errorf("%s: %d %s, want %d %s", what, got.Status, got.Body, status, body)
			}
		}
		stored := func() string {
			return w.Rows("SELECT value FROM settings WHERE key = ?", []any{"smtp_config"})
		}

		expect(do("admin", "GET", "/api/automation/smtp", ""), 200, `{"configured":false}`, "nothing saved")
		expect(do("admin", "POST", "/api/automation/smtp/test", "{}"), 200, `{"success":false,"error":"No SMTP configuration"}`, "test with nothing saved")
		expect(do("admin", "PUT", "/api/automation/smtp", `{"port":25}`), 400, `{"error":"SMTP host is required"}`, "save without a host")
		expect(do("admin", "PUT", "/api/automation/smtp", `{"host":""}`), 400, `{"error":"SMTP host is required"}`, "save with an empty host")
		if rows := stored(); rows != "[]" {
			t.Errorf("refused saves stored %s", rows)
		}

		// Who may not: someone signed out, and roles without server.config
		if got := do("", "GET", "/api/automation/smtp", ""); got.Status != http.StatusUnauthorized {
			t.Errorf("signed out: %d %s, want 401", got.Status, got.Body)
		}
		for _, role := range []string{"viewer", "operator"} {
			for _, req := range [][3]string{
				{"GET", "/api/automation/smtp", ""},
				{"PUT", "/api/automation/smtp", `{"host":"127.0.0.1"}`},
				{"POST", "/api/automation/smtp/test", "{}"},
			} {
				if got := do(role, req[0], req[1], req[2]); got.Status != http.StatusForbidden {
					t.Errorf("%s %s as %s: %d %s, want 403", req[0], req[1], role, got.Status, got.Body)
				}
			}
		}
		if rows := stored(); rows != "[]" {
			t.Errorf("a role without server.config stored %s", rows)
		}

		// A save, read back with the password masked
		full := `{"host":"127.0.0.1","port":1,"secure":false,"user":"contract","pass":"` + secret + `","from":"console@example.com","alert_email":"ops@example.com","tlsVerify":false}`
		expect(do("admin", "PUT", "/api/automation/smtp", full), 200, `{"success":true}`, "save")
		masked := `{"configured":true,"host":"127.0.0.1","port":1,"secure":false,"user":"contract","pass":"********","from":"console@example.com","alert_email":"ops@example.com","tlsVerify":false}`
		expect(do("admin", "GET", "/api/automation/smtp", ""), 200, masked, "read back")
		expect(do("super_admin", "GET", "/api/automation/smtp", ""), 200, masked, "read back as super_admin")
		if rows := stored(); !strings.Contains(rows, `\"pass\":\"`+secret+`\"`) {
			t.Errorf("the password is not stored: %s", rows)
		}
		if rows := w.Rows("SELECT user_id, action, details FROM audit_log WHERE action = ?", []any{"smtp_config_updated"}); rows != `[{"user_id":1,"action":"smtp_config_updated","details":"SMTP: 127.0.0.1:1"}]` {
			t.Errorf("audit entries of the save: %s", rows)
		}

		// The masked password and none keep the stored one
		for _, pass := range []string{`,"pass":"********"`, ``} {
			body := `{"host":"127.0.0.1","port":1,"user":"contract"` + pass + `,"from":"console@example.com","alert_email":"ops@example.com","tlsVerify":false}`
			expect(do("admin", "PUT", "/api/automation/smtp", body), 200, `{"success":true}`, "save again")
			if rows := stored(); !strings.Contains(rows, `\"pass\":\"`+secret+`\"`) {
				t.Errorf("a save with %q lost the stored password: %s", pass, rows)
			}
			expect(do("admin", "GET", "/api/automation/smtp", ""), 200, masked, "read back after a save without a new password")
		}

		// The test button connects to the saved server. Nothing listens on
		// port 1 of loopback, and the answer is the connection's error.
		expect(do("admin", "POST", "/api/automation/smtp/test", "{}"), 200, `{"success":false,"error":"connect ECONNREFUSED 127.0.0.1:1"}`, "test against a closed port")

		// The stored password is not kept for another server or user (BUGS
		// RV33), on both mounts of the handler
		for _, path := range []string{"/api/automation/smtp", "/api/settings/email/smtp"} {
			for _, body := range []string{
				`{"host":"mail.example.test","port":1,"user":"contract","pass":"********","from":"console@example.com","tlsVerify":false}`,
				`{"host":"127.0.0.1","port":2,"user":"contract","from":"console@example.com"}`,
				`{"host":"127.0.0.1","port":1,"user":"other","pass":"********","from":"console@example.com"}`,
			} {
				want := `{"error":"SMTP password is required when the host, port or user changes"}`
				if path == "/api/settings/email/smtp" {
					// the Email tab's route says it in the console's language, at the field (BUGS L419)
					want = `{"error":"Enter the password again. The saved password is only sent to the server and user name it was saved for.","field":"pass"}`
				}
				expect(do("admin", "PUT", path, body), 400, want, "save for another account at "+path)
			}
		}
		expect(do("admin", "GET", "/api/automation/smtp", ""), 200, masked, "read back after saves for another account")

		// An empty password removes the stored one, on both mounts (BUGS
		// RV40)
		for _, path := range []string{"/api/automation/smtp", "/api/settings/email/smtp"} {
			expect(do("admin", "PUT", path, full), 200, `{"success":true}`, "save with the password at "+path)
			expect(do("admin", "PUT", path, `{"host":"127.0.0.1","port":1,"user":"","pass":"","from":"console@example.com","alert_email":"ops@example.com","tlsVerify":false}`),
				200, `{"success":true}`, "save with an empty password at "+path)
			if rows := stored(); strings.Contains(rows, secret) || !strings.Contains(rows, `\"pass\":\"\"`) {
				t.Errorf("a save with an empty password at %s kept it: %s", path, rows)
			}
		}

		// A host alone has no sender and is refused (BUGS EM9); with a
		// sender the other members take their defaults
		expect(do("admin", "PUT", "/api/automation/smtp", `{"host":" mail.example.test "}`), 400, `{"error":"SMTP sender is required"}`, "save with a host only")
		expect(do("admin", "PUT", "/api/automation/smtp", `{"host":" mail.example.test ","from":"alerts@example.com","pass":"other-secret"}`), 200, `{"success":true}`, "save with a host and a sender")
		if rows := stored(); !strings.Contains(rows, `\"host\":\"mail.example.test\",\"port\":587,`) || !strings.Contains(rows, `\"from\":\"alerts@example.com\"`) {
			t.Errorf("a host and a sender are stored as %s", rows)
		}
	})
}

// The panel routes step aside for the device mount in any letter case, as
// the mount itself matches in any letter case (BUGS DV8), and so does the
// inventory group, which the console mounts at /api/nd before this one:
// below /API/ND only the device route answers, as below /api/nd. At the
// group's own mount the panel routes answer in any spelling.
func TestTheDeviceMountInAnotherSpelling(t *testing.T) {
	w := replay.NewWorld(t, storetest.Migrated(t), nil)

	for _, path := range []string{"/API/ND/smtp", "/Api/Nd/rules", "/API/ND/Rules/7", "/Api/nd/commands/7", "/api/nd/smtp", "/api/nd/rules", "/api/nd/rules/7", "/api/nd/commands/1"} {
		if got := w.Do(replay.Request{Role: "admin", Path: path}); got.Status != http.StatusNotFound || got.Body != `{"success":false,"error":"Not Found"}` {
			t.Errorf("GET %s as admin: %d %s, want the console's 404", path, got.Status, got.Body)
		}
	}
	// The device route after the panel routes
	for _, path := range []string{"/api/nd/commands", "/API/ND/Commands"} {
		if got := w.Do(replay.Request{Role: "admin", Path: path}); got.Status != http.StatusUnauthorized || got.Body != `{"error":"Bearer access token required"}` {
			t.Errorf("GET %s as admin: %d %s, want the device route's 401", path, got.Status, got.Body)
		}
	}
	if got := w.Do(replay.Request{Role: "admin", Path: "/API/Automation/Rules/7"}); got.Status != http.StatusNotFound || got.Body != `{"error":"Rule not found"}` {
		t.Errorf("GET /API/Automation/Rules/7 as admin: %d %s, want the rule route", got.Status, got.Body)
	}
}
