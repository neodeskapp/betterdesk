package users_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/users"
)

func setInactiveDays(t *testing.T, w *routetest.World, days string) {
	t.Helper()
	exec(t, w, `CREATE TABLE IF NOT EXISTS server_config (key TEXT PRIMARY KEY, value TEXT)`)
	exec(t, w, `DELETE FROM server_config WHERE key = 'inactive_disable_days'`)
	exec(t, w, `INSERT INTO server_config (key, value) VALUES ('inactive_disable_days', ?)`, days)
}

func setLastLogin(t *testing.T, w *routetest.World, id int64, at time.Time) {
	t.Helper()
	exec(t, w, `UPDATE users SET last_login = ? WHERE id = ?`, at.UTC().Format("2006-01-02 15:04:05"), id)
}

// The rule for inactive accounts tells the person a week before and then
// disables the account, with no acting account in the audit log. An
// account without an address is disabled when it is due. The exception,
// pro accounts, open invites and the last Super admin are left alone, and
// an account an admin enabled again counts from then.
func TestInactiveAccountsRule(t *testing.T) {
	w, box := mailWorld(t, true)
	ctx := context.Background()
	d := w.Server.Deps()
	now := time.Now().UTC()
	old := now.Add(-60 * 24 * time.Hour)
	ids := routetest.RoleIDs
	for _, role := range []string{"admin", "super_admin", "viewer", "operator", "guest_issuer", "pro"} {
		setLastLogin(t, w, ids[role], old)
	}
	setColumn(t, w, ids["viewer"], "email", "viewer@example.test")
	got := w.As("super_admin").Do("PATCH", "/api/users/7", `{"inactive_exempt":true}`)
	if got.Status != http.StatusOK {
		t.Fatalf("exception: %d %s", got.Status, got.Body)
	}
	expectHas(t, "the list", w.As("super_admin").Do("GET", "/api/users", ""), 200, `"inactive_exempt":true`)
	// An open invite to an account made long ago
	invited := w.As("super_admin").Do("POST", "/api/users/invite", `{"email":"new@example.test","username":"newbie"}`)
	exec(t, w, `UPDATE users SET created_at = ? WHERE username = 'newbie'`, old.Format("2006-01-02 15:04:05"))
	_ = invited

	// Off: nothing happens
	if err := users.Sweep(ctx, d, now); err != nil {
		t.Fatal(err)
	}
	if disabled(t, w, ids["operator"]) {
		t.Fatal("the rule ran while it is off")
	}

	setInactiveDays(t, w, "30")
	mails := box.count()
	if err := users.Sweep(ctx, d, now); err != nil {
		t.Fatal(err)
	}
	if n := len(box.mailTo("viewer@example.test")); n != 1 {
		t.Errorf("notices to the viewer: %d", n)
	}
	if disabled(t, w, ids["viewer"]) {
		t.Error("disabled at the notice")
	}
	if !disabled(t, w, ids["operator"]) {
		t.Error("the account without an address is not disabled")
	}
	if actor, target, _, found := auditAbout(t, w, "user_disabled_inactive"); !found || actor != 0 {
		t.Errorf("audit: %v %d %d", found, actor, target)
	}
	for _, role := range []string{"guest_issuer", "pro"} {
		if disabled(t, w, ids[role]) {
			t.Errorf("%s was disabled", role)
		}
	}
	if disabled(t, w, ids["admin"]) == disabled(t, w, ids["super_admin"]) {
		t.Errorf("one Super admin goes, the last one stays: admin %v, super_admin %v", disabled(t, w, ids["admin"]), disabled(t, w, ids["super_admin"]))
	}
	if n := countDisabled(t, w, "newbie"); n != 0 {
		t.Error("the open invite was disabled")
	}
	if box.count() != mails+1 {
		t.Errorf("%d mails, want one", box.count()-mails)
	}

	if err := users.Sweep(ctx, d, now.Add(3*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if disabled(t, w, ids["viewer"]) || len(box.mailTo("viewer@example.test")) != 1 {
		t.Fatal("disabled, or noticed again, less than a week after the notice")
	}
	if err := users.Sweep(ctx, d, now.Add(8*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if !disabled(t, w, ids["viewer"]) {
		t.Fatal("not disabled a week after the notice")
	}

	// Enabled again: it counts from now
	expect(t, "enable", w.As("super_admin").Do("PATCH", "/api/users/6", `{"disabled":false}`), 200, "")
	if err := users.Sweep(ctx, d, now.Add(9*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if disabled(t, w, ids["viewer"]) {
		t.Error("disabled again right after it was enabled")
	}
	expect(t, "bad exception", w.As("super_admin").Do("PATCH", "/api/users/6", `{"inactive_exempt":"yes"}`), 400, "")
	expect(t, "own exception", w.As("super_admin").Do("PATCH", "/api/users/2", `{"inactive_exempt":true}`), 400, "")
}

func countDisabled(t *testing.T, w *routetest.World, name string) int {
	t.Helper()
	var n int
	if err := w.DB.QueryRow(context.Background(), `SELECT COUNT(*) FROM users WHERE username = ? AND disabled = `+w.DB.Pick("1", "TRUE"), name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
