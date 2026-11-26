package users_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/console/admintoken"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
)

// tokenFor makes an API token of the account with the permissions.
func tokenFor(t *testing.T, w *routetest.World, id int64, name string, permissions ...string) string {
	t.Helper()
	creator := admintoken.Creator{ID: id, Username: name, Holds: func(string) bool { return true }}
	_, tok, err := w.Server.Deps().AdminTokens.Create(context.Background(), creator, admintoken.Input{
		Name: "script", Permissions: permissions, ReadOnly: true, ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
	}, auth.AllPermissions)
	if err != nil {
		t.Fatal(err)
	}
	return tok.ID
}

func exec(t *testing.T, w *routetest.World, query string, args ...any) {
	t.Helper()
	if _, err := w.DB.Exec(context.Background(), query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func text(t *testing.T, w *routetest.World, query string, args ...any) string {
	t.Helper()
	var s sql.NullString
	if err := w.DB.QueryRow(context.Background(), query, args...).Scan(&s); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return s.String
}

// The delete preview counts what goes and what stays. A delete with a
// hand-over gives the API tokens (capped to the new owner's permissions),
// the saved reports and the open tickets to the other account; the
// audit log says so.
func TestDeleteWithHandOver(t *testing.T) {
	w := routetest.New(t, routetest.Options{})
	admin := w.As("admin")
	op := routetest.RoleIDs["operator"]
	token := tokenFor(t, w, op, "e2e_operator", auth.PermDeviceView, auth.PermDeviceConnect)
	only := tokenFor(t, w, op, "e2e_operator", auth.PermDeviceConnect)
	exec(t, w, `INSERT INTO saved_reports (title, report_type, created_by) VALUES ('Weekly', 'devices', 'e2e_operator')`)
	exec(t, w, `INSERT INTO tickets (title, created_by, assigned_to, status) VALUES ('Open one', 'e2e_viewer', 'e2e_operator', 'open')`)
	exec(t, w, `INSERT INTO tickets (title, created_by, assigned_to, status) VALUES ('Done one', 'e2e_operator', 'e2e_operator', 'closed')`)
	exec(t, w, `INSERT INTO audit_log (user_id, action, details) VALUES (?, 'login', 'x')`, op)

	expect(t, "viewer", w.As("viewer").Do("GET", "/api/users/5/delete-preview", ""), 403, "")
	got := admin.Do("GET", "/api/users/5/delete-preview", "")
	var preview struct {
		Data struct {
			Counts     map[string]int64 `json:"counts"`
			Candidates []struct {
				ID int64 `json:"id"`
			} `json:"candidates"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(got.Body), &preview); err != nil || got.Status != http.StatusOK {
		t.Fatalf("preview: %d %s", got.Status, got.Body)
	}
	c := preview.Data.Counts
	if c["api_tokens"] != 2 || c["saved_reports"] != 1 || c["open_tickets"] != 1 || c["tickets_opened"] != 1 || c["audit_entries"] < 1 {
		t.Errorf("counts: %v", c)
	}
	var candidates []int64
	for _, a := range preview.Data.Candidates {
		candidates = append(candidates, a.ID)
	}
	for _, id := range candidates {
		if id == op || id == routetest.RoleIDs["pro"] {
			t.Errorf("candidate %d: %v", id, candidates)
		}
	}

	expect(t, "delete", admin.Do("DELETE", "/api/users/5?handover_to=6", ""), 200, `{"success":true}`)
	if owner := text(t, w, `SELECT CAST(user_id AS TEXT) FROM admin_tokens WHERE public_id = ?`, token); owner != "6" {
		t.Errorf("the token's owner: %s", owner)
	}
	if perms := text(t, w, `SELECT permissions FROM admin_tokens WHERE public_id = ?`, token); perms != `["device.view"]` {
		t.Errorf("the token kept more than the viewer holds: %s", perms)
	}
	if revoked := text(t, w, `SELECT CAST(revoked_at AS TEXT) FROM admin_tokens WHERE public_id = ?`, only); revoked == "" {
		t.Error("a token with nothing the viewer holds is still live")
	}
	if by := text(t, w, `SELECT created_by FROM saved_reports WHERE title = 'Weekly'`); by != "e2e_viewer" {
		t.Errorf("the report's owner: %s", by)
	}
	if to := text(t, w, `SELECT assigned_to FROM tickets WHERE title = 'Open one'`); to != "e2e_viewer" {
		t.Errorf("the open ticket: %s", to)
	}
	if to := text(t, w, `SELECT assigned_to FROM tickets WHERE title = 'Done one'`); to != "e2e_operator" {
		t.Errorf("the closed ticket moved: %s", to)
	}
	if _, target, details, found := auditAbout(t, w, "user_deleted"); !found || target != op || !strings.Contains(details, "handed over to e2e_viewer") {
		t.Errorf("audit: %v %d %q", found, target, details)
	}
}

// A delete without a hand-over revokes the account's API tokens and
// clears the lockout of its name, so a new account with the name starts
// clean. A hand-over to an account the caller may not change, or to the
// deleted account, is refused and deletes nothing.
func TestDeleteWithoutHandOver(t *testing.T) {
	w := routetest.New(t, routetest.Options{})
	op := routetest.RoleIDs["operator"]
	token := tokenFor(t, w, op, "e2e_operator", auth.PermDeviceView)
	exec(t, w, `INSERT INTO account_lockouts (username, locked_until, attempt_count) VALUES ('e2e_operator', '2099-01-01 00:00:00', 9)`)
	exec(t, w, `INSERT INTO login_attempts (username, ip_address, success) VALUES ('e2e_operator', '10.0.0.1', 0)`)

	global := w.As("global_admin")
	expect(t, "to a Super admin", global.Do("DELETE", "/api/users/5?handover_to=2", ""), 403, "")
	expect(t, "to itself", global.Do("DELETE", "/api/users/5?handover_to=5", ""), 403, "")
	expect(t, "to nobody", global.Do("DELETE", "/api/users/5?handover_to=abc", ""), 400, "")
	if n := countAccounts(t, w, "e2e_operator"); n != 1 {
		t.Fatalf("a refused hand-over deleted the account: %d", n)
	}

	expect(t, "delete", global.Do("DELETE", "/api/users/5", ""), 200, `{"success":true}`)
	if revoked := text(t, w, `SELECT CAST(revoked_at AS TEXT) FROM admin_tokens WHERE public_id = ?`, token); revoked == "" {
		t.Error("the deleted account's API token is not revoked")
	}
	if n := text(t, w, `SELECT CAST(COUNT(*) AS TEXT) FROM account_lockouts WHERE username = 'e2e_operator'`); n != "0" {
		t.Errorf("the lockout of the name stays: %s", n)
	}
	if n := text(t, w, `SELECT CAST(COUNT(*) AS TEXT) FROM login_attempts WHERE username = 'e2e_operator'`); n != "0" {
		t.Errorf("the failed sign-ins of the name stay: %s", n)
	}
}

// Disable keeps the account's API tokens but refuses them while it is
// disabled, as its sessions and client sign-ins end; enabled again, the
// token works again.
func TestDisableStopsAPITokens(t *testing.T) {
	w := routetest.New(t, routetest.Options{})
	op := routetest.RoleIDs["operator"]
	creator := admintoken.Creator{ID: op, Username: "e2e_operator", Holds: func(string) bool { return true }}
	value, _, err := w.Server.Deps().AdminTokens.Create(context.Background(), creator, admintoken.Input{
		Name: "script", Permissions: []string{auth.PermUserView}, ReadOnly: true, ExpiresAt: time.Now().Add(24 * time.Hour),
	}, auth.AllPermissions)
	if err != nil {
		t.Fatal(err)
	}
	use := func() int {
		return w.SignedOut().Do("GET", "/api/users", "", "Authorization", "Bearer "+value).Status
	}
	if s := use(); s != http.StatusOK {
		t.Fatalf("the token before: %d", s)
	}
	admin := w.As("admin")
	expect(t, "disable", admin.Do("PATCH", "/api/users/5", `{"disabled":true}`), 200, "")
	if s := use(); s == http.StatusOK {
		t.Errorf("the token of a disabled account works: %d", s)
	}
	expect(t, "enable", admin.Do("PATCH", "/api/users/5", `{"disabled":false}`), 200, "")
	if s := use(); s != http.StatusOK {
		t.Errorf("the token after enabling: %d", s)
	}
}
