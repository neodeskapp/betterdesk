package users_test

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
)

// auditAbout returns the acting account (0 for the system) and the
// account an entry is about of the last audit entry with the action, and
// whether there is one.
func auditAbout(t *testing.T, w *routetest.World, action string) (actor, target int64, details string, found bool) {
	t.Helper()
	var a, tg sql.NullInt64
	err := w.DB.QueryRow(context.Background(), `SELECT user_id, target_user_id, COALESCE(details, '') FROM audit_log WHERE action = ? ORDER BY id DESC LIMIT 1`, action).Scan(&a, &tg, &details)
	if err == sql.ErrNoRows {
		return 0, 0, "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return a.Int64, tg.Int64, details, true
}

// Withdrawing an invite ends its link and deletes the account, which never
// signed in. An account that signed in, or one without an invite, is
// refused, and so is someone without user.create.
func TestRevokeInvite(t *testing.T) {
	w, box := mailWorld(t, true)
	admin := w.As("admin")
	got := admin.Do("POST", "/api/users/invite", `{"email":"mia@example.test","username":"mia","role":"viewer"}`)
	if got.Status != http.StatusOK {
		t.Fatalf("invite: %d %s", got.Status, got.Body)
	}
	id := createdID(t, got)
	token := tokenOf(t, box.last(t))

	expect(t, "viewer", w.As("viewer").Do("DELETE", "/api/users/"+id+"/invite", ""), 403, "")
	expect(t, "an account without an invite", admin.Do("DELETE", "/api/users/6/invite", ""), 409, "")

	expect(t, "withdraw", admin.Do("DELETE", "/api/users/"+id+"/invite", ""), 200, `{"success":true}`)
	if n := countAccounts(t, w, "mia"); n != 0 {
		t.Errorf("the account is still there: %d", n)
	}
	expect(t, "the link", w.SignedOut().Do("POST", "/api/auth/password-link/lookup", `{"token":"`+token+`"}`), 404, "")
	actor, target, details, found := auditAbout(t, w, "user_invite_revoked")
	if !found || actor != routetest.RoleIDs["admin"] || strconv.FormatInt(target, 10) != id || !strings.Contains(details, "mia") {
		t.Errorf("audit: found %v, actor %d, target %d, %q", found, actor, target, details)
	}
	expect(t, "again", admin.Do("DELETE", "/api/users/"+id+"/invite", ""), 404, "")

	// Signed in already: Delete is the way
	got = admin.Do("POST", "/api/users/invite", `{"email":"leo@example.test","username":"leo"}`)
	leo := createdID(t, got)
	if _, err := w.DB.Exec(context.Background(), `UPDATE users SET last_login = CURRENT_TIMESTAMP WHERE id = ?`, leo); err != nil {
		t.Fatal(err)
	}
	expect(t, "signed in", admin.Do("DELETE", "/api/users/"+leo+"/invite", ""), 409, "")
	if n := countAccounts(t, w, "leo"); n != 1 {
		t.Errorf("a signed-in account was deleted: %d", n)
	}
}
