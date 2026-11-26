package users_test

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/lifecycle"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/users"
)

func disabled(t *testing.T, w *routetest.World, id int64) bool {
	t.Helper()
	var flag bool
	if err := w.DB.QueryRow(context.Background(), `SELECT COALESCE(disabled, `+w.DB.Pick("0", "FALSE")+`) FROM users WHERE id = ?`, id).Scan(&flag); err != nil {
		t.Fatal(err)
	}
	return flag
}

func auditCount(t *testing.T, w *routetest.World, action string) int {
	t.Helper()
	var n int
	if err := w.DB.QueryRow(context.Background(), `SELECT COUNT(*) FROM audit_log WHERE action = ?`, action).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// An admin gives an account an end date in Edit; a date in the past, the
// own account and text that is no date are refused. When the date comes
// the job disables the account once, ends its sessions and notes it with
// no acting account; enabling it again takes the passed date away.
func TestAccountEndDate(t *testing.T) {
	w := routetest.New(t, routetest.Options{})
	admin := w.As("admin")
	viewer := w.As("viewer")
	viewerID := routetest.RoleIDs["viewer"]
	end := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)

	expect(t, "in the past", admin.Do("PATCH", "/api/users/6", `{"expires_at":"2020-01-01"}`), 400, "")
	expect(t, "no date", admin.Do("PATCH", "/api/users/6", `{"expires_at":"soon"}`), 400, "")
	expect(t, "a number", admin.Do("PATCH", "/api/users/6", `{"expires_at":5}`), 400, "")
	expect(t, "own account", admin.Do("PATCH", "/api/users/1", `{"expires_at":"`+end.Format(time.RFC3339)+`"}`), 400, "")
	expect(t, "viewer", viewer.Do("PATCH", "/api/users/5", `{"expires_at":"`+end.Format(time.RFC3339)+`"}`), 403, "")

	got := admin.Do("PATCH", "/api/users/6", `{"expires_at":"`+end.Format(time.RFC3339)+`"}`)
	if got.Status != http.StatusOK || !strings.Contains(got.Body, `"expires_at":"`+end.Format(time.RFC3339)+`"`) || strings.Contains(got.Body, `"expired":true`) {
		t.Fatalf("set: %d %s", got.Status, got.Body)
	}
	if actor, target, details, found := auditAbout(t, w, "user_expiry_set"); !found || actor != routetest.RoleIDs["admin"] || target != viewerID || !strings.Contains(details, end.Format("2006-01-02")) {
		t.Errorf("audit: %v %d %d %q", found, actor, target, details)
	}
	if got := admin.Do("GET", "/api/users", ""); !strings.Contains(got.Body, `"expires_at":"`+end.Format(time.RFC3339)+`"`) {
		t.Errorf("the list has no end date: %s", got.Body)
	}

	d := w.Server.Deps()
	ctx := context.Background()
	if err := users.Sweep(ctx, d, end.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if disabled(t, w, viewerID) {
		t.Fatal("disabled before the end date")
	}
	if err := users.Sweep(ctx, d, end); err != nil {
		t.Fatal(err)
	}
	if !disabled(t, w, viewerID) {
		t.Fatal("not disabled on the end date")
	}
	expect(t, "viewer's session", viewer.Do("GET", "/api/auth/verify", ""), 401, "")
	if actor, target, _, found := auditAbout(t, w, "user_expired"); !found || actor != 0 || target != viewerID {
		t.Errorf("audit of the end: %v actor %d target %d", found, actor, target)
	}
	if err := users.Sweep(ctx, d, end.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if n := auditCount(t, w, "user_expired"); n != 1 {
		t.Errorf("%d user_expired entries", n)
	}
	if got := admin.Do("GET", "/api/users", ""); !strings.Contains(got.Body, `"expired":true`) {
		t.Errorf("the list doesn't say it ended: %s", got.Body)
	}

	// Enabled again: the end date that passed goes, the job leaves it alone
	expect(t, "enable", admin.Do("PATCH", "/api/users/6", `{"disabled":false}`), 200, "")
	if err := users.Sweep(ctx, d, end.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if disabled(t, w, viewerID) {
		t.Error("disabled again after it was enabled")
	}
	expect(t, "remove", admin.Do("PATCH", "/api/users/5", `{"expires_at":null}`), 200, "")
}

// The sign-in refuses an account whose end date has passed even before
// the job disabled it.
func TestEndDateStopsTheSignIn(t *testing.T) {
	w := routetest.New(t, routetest.Options{})
	got := w.As("admin").Do("POST", "/api/users", `{"username":"contractor","password":"Strong-Pass-1!","role":"viewer"}`)
	id, err := strconv.ParseInt(createdID(t, got), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	login := `{"username":"contractor","password":"Strong-Pass-1!"}`
	if got := w.SignedOut().Do("POST", "/api/auth/login", login); got.Status != http.StatusOK {
		t.Fatalf("sign-in before the end date: %d %s", got.Status, got.Body)
	}
	if err := lifecycle.New(w.DB, nil).SetExpiry(context.Background(), id, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := w.SignedOut().Do("POST", "/api/auth/login", login); got.Status == http.StatusOK {
		t.Errorf("sign-in after the end date: %d %s", got.Status, got.Body)
	}
}

// A week before the end date the admins who may change the account hear
// of it, once, each in their language; others don't.
func TestEndDateNotice(t *testing.T) {
	w, box := mailWorld(t, true)
	setColumn(t, w, routetest.RoleIDs["admin"], "email", "admin@example.test")
	setColumn(t, w, routetest.RoleIDs["global_admin"], "email", "global@example.test")
	setColumn(t, w, routetest.RoleIDs["global_admin"], "preferred_language", "de")
	setColumn(t, w, routetest.RoleIDs["viewer"], "email", "viewer@example.test")
	setColumn(t, w, routetest.RoleIDs["operator"], "email", "operator@example.test")
	end := time.Now().Add(30 * 24 * time.Hour).UTC().Truncate(time.Second)
	expect(t, "set", w.As("admin").Do("PATCH", "/api/users/5", `{"expires_at":"`+end.Format(time.RFC3339)+`"}`), 200, "")

	ctx := context.Background()
	d := w.Server.Deps()
	if err := users.Sweep(ctx, d, end.Add(-8*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if box.count() != 0 {
		t.Fatalf("%d mails eight days before", box.count())
	}
	if err := users.Sweep(ctx, d, end.Add(-6*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if len(box.mailTo("admin@example.test")) != 1 || len(box.mailTo("global@example.test")) != 1 {
		t.Fatalf("notices: admin %d, global admin %d", len(box.mailTo("admin@example.test")), len(box.mailTo("global@example.test")))
	}
	if len(box.mailTo("viewer@example.test"))+len(box.mailTo("operator@example.test")) != 0 {
		t.Error("a notice went to an account that may not change it")
	}
	if s := box.mailTo("global@example.test")[0].Subject; !strings.Contains(s, "e2e_operator") || s == box.mailTo("admin@example.test")[0].Subject {
		t.Errorf("subjects: %q and %q", s, box.mailTo("admin@example.test")[0].Subject)
	}
	if err := users.Sweep(ctx, d, end.Add(-5*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if box.count() != 2 {
		t.Errorf("%d mails after a second run", box.count())
	}

	// A Super admin's end date: the global admin may not change that account
	soon := time.Now().Add(3 * 24 * time.Hour).UTC().Truncate(time.Second)
	expect(t, "set on a Super admin", w.As("admin").Do("PATCH", "/api/users/2", `{"expires_at":"`+soon.Format(time.RFC3339)+`"}`), 200, "")
	if err := users.Sweep(ctx, d, time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(box.mailTo("admin@example.test")) != 2 || len(box.mailTo("global@example.test")) != 1 {
		t.Errorf("notices about a Super admin: admin %d, global admin %d (want 2 and 1)", len(box.mailTo("admin@example.test")), len(box.mailTo("global@example.test")))
	}
}

// The last Super admin that can sign in is never deleted, disabled,
// demoted or given an end date, and the job never disables it: a disabled
// Super admin does not count.
func TestLastSuperAdmin(t *testing.T) {
	w := routetest.New(t, routetest.Options{})
	admin := w.As("admin")
	// e2e_admin (the old admin role) is disabled; e2e_super_admin is the last one
	setColumn(t, w, routetest.RoleIDs["admin"], "disabled", w.DB.Pick("1", "true"))
	end := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	expect(t, "end date", admin.Do("PATCH", "/api/users/2", `{"expires_at":"`+end+`"}`), 400, "")
	expect(t, "disable", admin.Do("PATCH", "/api/users/2", `{"disabled":true}`), 400, "")
	expect(t, "demote", admin.Do("PATCH", "/api/users/2", `{"role":"operator"}`), 400, "")
	expect(t, "delete", admin.Do("DELETE", "/api/users/2", ""), 400, "")
	if disabled(t, w, 2) {
		t.Fatal("the last Super admin was disabled")
	}

	// An end date set while another could sign in: the job keeps it enabled
	if err := lifecycle.New(w.DB, nil).SetExpiry(context.Background(), 2, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := users.Sweep(context.Background(), w.Server.Deps(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if disabled(t, w, 2) {
		t.Error("the job disabled the last Super admin")
	}
	// With the other enabled again, both can be changed
	setColumn(t, w, routetest.RoleIDs["admin"], "disabled", w.DB.Pick("0", "false"))
	if err := users.Sweep(context.Background(), w.Server.Deps(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if !disabled(t, w, 2) {
		t.Error("the end date stays without effect once another Super admin can sign in")
	}
}
