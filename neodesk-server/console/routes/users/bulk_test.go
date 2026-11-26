package users_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/console/admintoken"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// roleOf is the role the users table has for an account.
func roleOf(t *testing.T, w *routetest.World, id string) string {
	t.Helper()
	return text(t, w, `SELECT role FROM users WHERE id = ?`, id)
}

// outcomes are the results of a bulk answer as "username:outcome" or
// "username:outcome:error", with "dry" in front for a dry run.
func outcomes(t *testing.T, got routetest.Answer) string {
	t.Helper()
	var out struct {
		Data struct {
			DryRun  bool `json:"dry_run"`
			Results []struct {
				ID       json.RawMessage `json:"id"`
				Username string          `json:"username"`
				Outcome  string          `json:"outcome"`
				Error    string          `json:"error"`
			} `json:"results"`
		} `json:"data"`
	}
	if got.Status != http.StatusOK || json.Unmarshal([]byte(got.Body), &out) != nil {
		t.Fatalf("bulk answer: %d %s", got.Status, got.Body)
	}
	var parts []string
	if out.Data.DryRun {
		parts = append(parts, "dry")
	}
	for _, r := range out.Data.Results {
		name := r.Username
		if name == "" {
			name = string(r.ID)
		}
		part := name + ":" + r.Outcome
		if r.Error != "" {
			part += ":" + r.Error
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, " | ")
}

func ldap(t *testing.T, w *routetest.World, id string) {
	t.Helper()
	exec(t, w, `UPDATE users SET auth_provider = 'ldap' WHERE id = ?`, id)
}

// A role for several accounts: the dry run says what would happen and
// writes nothing; the run changes each account the single route would
// change, refuses the rest with the reason, and audits each change about
// its account. A role the caller may not give is refused for the request.
func TestBulkRole(t *testing.T) {
	w, _ := mailWorld(t, false)
	admin := w.As("admin")
	carol := makeAccount(t, w, admin, "carol", "")
	dirk := makeAccount(t, w, admin, "dirk", "")
	ldap(t, w, dirk)
	body := `{"ids":[` + carol + `,` + dirk + `,1,99999,"x",` + carol + `],"role":"operator"`
	want := "carol:changed | dirk:refused:This account gets its role from LDAP/AD. | e2e_admin:refused:Your own account is left out. Change it on its own. | 99999:refused:User not found | \"x\":refused:Invalid user ID | carol:unchanged"
	if got := outcomes(t, admin.Do("POST", "/api/users/bulk/role", body+`,"dry_run":true}`)); got != "dry | "+want {
		t.Errorf("dry run:\n got %s\nwant dry | %s", got, want)
	}
	if roleOf(t, w, carol) != "viewer" {
		t.Fatal("the dry run changed the role")
	}
	if got := outcomes(t, admin.Do("POST", "/api/users/bulk/role", body+`}`)); got != want {
		t.Errorf("run:\n got %s\nwant %s", got, want)
	}
	if roleOf(t, w, carol) != "operator" || roleOf(t, w, dirk) != "viewer" {
		t.Errorf("roles: carol %s, dirk %s", roleOf(t, w, carol), roleOf(t, w, dirk))
	}
	rows := strings.Join(auditRowsAbout(t, w, carol), "\n")
	if !strings.Contains(rows, "user_role_changed|Changed the role of user carol from viewer to operator|1") {
		t.Errorf("audit:\n%s", rows)
	}
	if got := outcomes(t, admin.Do("POST", "/api/users/bulk/role", `{"ids":[`+carol+`],"role":"operator"}`)); got != "carol:unchanged" {
		t.Errorf("again: %s", got)
	}

	global := w.As("global_admin")
	expect(t, "a role above the caller's", global.Do("POST", "/api/users/bulk/role", `{"ids":[`+carol+`],"role":"super_admin"}`), 403,
		`{"success":false,"error":"Cannot assign a role higher than your own"}`)
	if got := outcomes(t, global.Do("POST", "/api/users/bulk/role", `{"ids":[2,`+carol+`],"role":"viewer"}`)); got != "e2e_super_admin:refused:Cannot assign a role higher than your own | carol:changed" {
		t.Errorf("global admin: %s", got)
	}
	expect(t, "unknown role", admin.Do("POST", "/api/users/bulk/role", `{"ids":[`+carol+`],"role":"boss"}`), 400, "")
	expect(t, "viewer", w.As("viewer").Do("POST", "/api/users/bulk/role", `{"ids":[`+carol+`],"role":"viewer"}`), 403, "")
}

// One request names at most 500 accounts.
func TestBulkLimit(t *testing.T) {
	w, _ := mailWorld(t, false)
	admin := w.As("admin")
	ids := func(n int) string {
		list := make([]string, n)
		for i := range list {
			list[i] = strconv.Itoa(100000 + i)
		}
		return `{"ids":[` + strings.Join(list, ",") + `],"dry_run":true}`
	}
	expect(t, "501", admin.Do("POST", "/api/users/bulk/disable", ids(501)), 400, `{"success":false,"error":"One request takes up to 500 accounts."}`)
	expect(t, "501 on the older route", admin.Do("POST", "/api/users/require-password-change", ids(501)), 400, "")
	if got := admin.Do("POST", "/api/users/bulk/disable", ids(500)); got.Status != http.StatusOK || strings.Count(got.Body, `"outcome":"refused"`) != 500 {
		t.Errorf("500: %d %.200s", got.Status, got.Body)
	}
	expect(t, "no list", admin.Do("POST", "/api/users/bulk/disable", `{"ids":[]}`), 400, "")
	expect(t, "no list at all", admin.Do("POST", "/api/users/bulk/disable", `{"ids":"5"}`), 400, "")
	expect(t, "dry_run not a flag", admin.Do("POST", "/api/users/bulk/disable", `{"ids":[5],"dry_run":"yes"}`), 400, "")
}

// Disable and enable several accounts: never the caller's own, an account
// already so is unchanged, each change is audited about the account, and
// disabling ends the account's sign-in.
func TestBulkDisableAndEnable(t *testing.T) {
	w, _ := mailWorld(t, false)
	admin := w.As("admin")
	carol := makeAccount(t, w, admin, "carol", "")
	dave := makeAccount(t, w, admin, "dave", "")
	expect(t, "disable dave alone", admin.Do("PATCH", "/api/users/"+dave, `{"disabled":true}`), 200, "")
	if got := outcomes(t, admin.Do("POST", "/api/users/bulk/disable", `{"ids":[`+carol+`,`+dave+`,1]}`)); got != "carol:changed | dave:unchanged | e2e_admin:refused:Your own account is left out. Change it on its own." {
		t.Errorf("disable: %s", got)
	}
	if signsIn(w, "carol", "Old-Pass-123!") {
		t.Error("a disabled account signs in")
	}
	if got := outcomes(t, admin.Do("POST", "/api/users/bulk/enable", `{"ids":[`+carol+`,`+dave+`]}`)); got != "carol:changed | dave:changed" {
		t.Errorf("enable: %s", got)
	}
	if !signsIn(w, "carol", "Old-Pass-123!") {
		t.Error("an enabled account doesn't sign in")
	}
	rows := strings.Join(auditRowsAbout(t, w, carol), "\n")
	if !strings.Contains(rows, "user_disabled|Disabled user: carol|1") || !strings.Contains(rows, "user_enabled|Enabled user: carol|1") {
		t.Errorf("audit:\n%s", rows)
	}
	if got := outcomes(t, w.As("global_admin").Do("POST", "/api/users/bulk/disable", `{"ids":[2]}`)); got != "e2e_super_admin:refused:Cannot assign a role higher than your own" {
		t.Errorf("global admin: %s", got)
	}
}

// The last Super admin that can sign in is never disabled or deleted, also
// not as the second of two in one request; the dry run counts the first.
func TestBulkKeepsTheLastSuperAdmin(t *testing.T) {
	w, _ := mailWorld(t, false)
	admin := w.As("admin")
	sam := makeAccount(t, w, admin, "sam", "")
	exec(t, w, `UPDATE users SET role = 'super_admin' WHERE id = ?`, sam)
	// The signed-in account no longer counts: its row says viewer
	exec(t, w, `UPDATE users SET role = 'viewer' WHERE id = 1`)
	want := "e2e_super_admin:changed | sam:refused:This is the only active Super admin. Make another account Super admin first."
	if got := outcomes(t, admin.Do("POST", "/api/users/bulk/disable", `{"ids":[2,`+sam+`],"dry_run":true}`)); got != "dry | "+want {
		t.Errorf("dry run: %s", got)
	}
	if got := outcomes(t, admin.Do("POST", "/api/users/bulk/disable", `{"ids":[2,`+sam+`]}`)); got != want {
		t.Errorf("run: %s", got)
	}
	if disabled(t, w, mustInt(t, sam)) {
		t.Error("the last Super admin was disabled")
	}
	if got := outcomes(t, admin.Do("POST", "/api/users/bulk/delete", `{"ids":[`+sam+`],"dry_run":true}`)); got != "dry | sam:refused:Cannot delete the last administrator" {
		t.Errorf("delete: %s", got)
	}
}

func mustInt(t *testing.T, s string) int64 {
	t.Helper()
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// Delete several accounts: the dry run deletes nothing; the run deletes
// each the single route would, never the caller's own, and audits each.
func TestBulkDelete(t *testing.T) {
	w, _ := mailWorld(t, false)
	admin := w.As("admin")
	carol := makeAccount(t, w, admin, "carol", "")
	dave := makeAccount(t, w, admin, "dave", "")
	want := "carol:changed | dave:changed | e2e_admin:refused:You cannot delete your own account"
	body := `{"ids":[` + carol + `,` + dave + `,1]`
	if got := outcomes(t, admin.Do("POST", "/api/users/bulk/delete", body+`,"dry_run":true}`)); got != "dry | "+want {
		t.Errorf("dry run: %s", got)
	}
	if countAccounts(t, w, "carol") != 1 {
		t.Fatal("the dry run deleted carol")
	}
	if got := outcomes(t, admin.Do("POST", "/api/users/bulk/delete", body+`}`)); got != want {
		t.Errorf("run: %s", got)
	}
	if countAccounts(t, w, "carol") != 0 || countAccounts(t, w, "dave") != 0 {
		t.Error("not deleted")
	}
	if rows := auditRowsAbout(t, w, dave); len(rows) == 0 || !strings.HasPrefix(rows[len(rows)-1], "user_deleted|Deleted user: dave|1") {
		t.Errorf("audit: %v", rows)
	}
	expect(t, "without user.delete", w.As("operator").Do("POST", "/api/users/bulk/delete", `{"ids":[5]}`), 403, "")
}

// Two-factor sign-in required of several accounts: user.security, local
// accounts only, never the caller's own.
func TestBulkTwoFactorRequired(t *testing.T) {
	w, _ := mailWorld(t, false)
	admin := w.As("admin")
	carol := makeAccount(t, w, admin, "carol", "")
	dirk := makeAccount(t, w, admin, "dirk", "")
	ldap(t, w, dirk)
	got := outcomes(t, admin.Do("POST", "/api/users/bulk/two-factor-required", `{"ids":[`+carol+`,`+dirk+`,1],"required":true}`))
	if got != "carol:changed | dirk:refused:This account signs in with LDAP/AD. Its two-factor sign-in is set there. | e2e_admin:refused:Change your own sign-in on Your account." {
		t.Errorf("require: %s", got)
	}
	if listedAs(t, admin, "carol").TwoFactorState != "required" {
		t.Error("carol isn't required to set it up")
	}
	if got := outcomes(t, admin.Do("POST", "/api/users/bulk/two-factor-required", `{"ids":[`+carol+`],"required":true}`)); got != "carol:unchanged" {
		t.Errorf("again: %s", got)
	}
	if rows := strings.Join(auditRowsAbout(t, w, carol), "\n"); !strings.Contains(rows, "two_factor_required|Requires two-factor sign-in of user: carol|1") {
		t.Errorf("audit:\n%s", rows)
	}
	expect(t, "no required", admin.Do("POST", "/api/users/bulk/two-factor-required", `{"ids":[`+carol+`]}`), 400, "")
	expect(t, "without user.security", w.As("operator").Do("POST", "/api/users/bulk/two-factor-required", `{"ids":[`+carol+`],"required":true}`), 403, "")
}

// Reset links for several accounts: one mail each to the accounts with an
// address, none for a directory account or one without an address; the
// dry run sends nothing; without email settings the request is refused.
func TestBulkResetLinks(t *testing.T) {
	w, box := mailWorld(t, true)
	admin := w.As("admin")
	carol := makeAccount(t, w, admin, "carol", "carol@example.test")
	dave := makeAccount(t, w, admin, "dave", "")
	dirk := makeAccount(t, w, admin, "dirk", "dirk@example.test")
	ldap(t, w, dirk)
	body := `{"ids":[` + carol + `,` + dave + `,` + dirk + `]`
	want := "carol:changed | dave:refused:This account has no email address. | dirk:refused:This account signs in with LDAP/AD. Reset its password there."
	if got := outcomes(t, admin.Do("POST", "/api/users/bulk/password-reset-link", body+`,"dry_run":true}`)); got != "dry | "+want {
		t.Errorf("dry run: %s", got)
	}
	if box.count() != 0 {
		t.Fatal("the dry run sent mail")
	}
	if got := outcomes(t, admin.Do("POST", "/api/users/bulk/password-reset-link", body+`}`)); got != want {
		t.Errorf("run: %s", got)
	}
	if n := len(mailsTo(box, "carol@example.test", 0)); n != 1 || box.count() != 1 {
		t.Errorf("%d mails to carol, %d in all", n, box.count())
	}
	if rows := strings.Join(auditRowsAbout(t, w, carol), "\n"); !strings.Contains(rows, "password_reset_link_sent|Sent a password reset link to user: carol|1") {
		t.Errorf("audit:\n%s", rows)
	}

	without, _ := mailWorld(t, false)
	expect(t, "no email settings", without.As("admin").Do("POST", "/api/users/bulk/password-reset-link", `{"ids":[5]}`), 409, "")
}

// Adding to and removing from an organization: the API server is asked
// for each account; one that is a member already (or no longer) is
// unchanged, one the API server refuses is refused with its text, and
// each change is audited about the account.
func TestBulkOrganization(t *testing.T) {
	api := routetest.NewAPI()
	w := routetest.New(t, routetest.Options{API: api})
	admin := w.As("admin")
	carol := makeAccount(t, w, admin, "carol", "")
	dave := makeAccount(t, w, admin, "dave", "")
	erin := makeAccount(t, w, admin, "erin", "")
	api.On("GET /api/org/o1", 200, `{"id":"o1","name":"Sales"}`)
	api.On("GET /api/users/"+carol+"/organizations", 200, `{"organizations":[]}`)
	api.On("GET /api/users/"+dave+"/organizations", 200, `{"organizations":[{"id":"o1","org_id":"o1"}]}`)
	api.On("GET /api/users/"+erin+"/organizations", 200, `{"organizations":[]}`)
	api.On("POST /api/users/"+carol+"/organizations", 201, `{}`)
	api.On("POST /api/users/"+erin+"/organizations", 403, `{"error":"you are not a member of this organization"}`)
	body := `{"ids":[` + carol + `,` + dave + `,` + erin + `],"org_id":"o1"`
	if got := outcomes(t, admin.Do("POST", "/api/users/bulk/organization-add", body+`,"dry_run":true}`)); got != "dry | carol:changed | dave:unchanged | erin:changed" {
		t.Errorf("dry run: %s", got)
	}
	for _, c := range api.Calls() {
		if !strings.HasPrefix(c, "GET ") {
			t.Errorf("the dry run wrote: %s", c)
		}
	}
	if got := outcomes(t, admin.Do("POST", "/api/users/bulk/organization-add", body+`}`)); got != "carol:changed | dave:unchanged | erin:refused:you are not a member of this organization" {
		t.Errorf("add: %s", got)
	}
	if calls := strings.Join(api.Calls(), "\n"); !strings.Contains(calls, "POST /api/users/"+carol+`/organizations {"org_id":"o1"}`) {
		t.Errorf("calls:\n%s", calls)
	}
	if rows := strings.Join(auditRowsAbout(t, w, carol), "\n"); !strings.Contains(rows, "user_org_assigned|Added user carol to organization Sales|1") {
		t.Errorf("audit:\n%s", rows)
	}
	api.On("DELETE /api/org/o1/members/"+dave, 204, ``)
	if got := outcomes(t, admin.Do("POST", "/api/users/bulk/organization-remove", `{"ids":[`+carol+`,`+dave+`],"org_id":"o1"}`)); got != "carol:unchanged | dave:changed" {
		t.Errorf("remove: %s", got)
	}
	if rows := strings.Join(auditRowsAbout(t, w, dave), "\n"); !strings.Contains(rows, "org_user_unlinked|Removed user dave from organization Sales|1") {
		t.Errorf("audit:\n%s", rows)
	}
	api.On("GET /api/org/gone", 404, `{"error":"organization not found"}`)
	expect(t, "unknown organization", admin.Do("POST", "/api/users/bulk/organization-add", `{"ids":[`+carol+`],"org_id":"gone"}`), 404, `{"success":false,"error":"organization not found"}`)
	expect(t, "no organization", admin.Do("POST", "/api/users/bulk/organization-add", `{"ids":[`+carol+`]}`), 400, "")
	expect(t, "without org.manage_users", w.As("viewer").Do("POST", "/api/users/bulk/organization-add", body+`}`), 403, "")
}

// An API token reaches the bulk routes with the same checks: its
// permissions, and the account's rights over each account.
func TestBulkWithAnAPIToken(t *testing.T) {
	w, _ := mailWorld(t, false)
	admin := w.As("admin")
	carol := makeAccount(t, w, admin, "carol", "")
	token := func(id int64, name string, permissions ...string) string {
		creator := admintoken.Creator{ID: id, Username: name, Holds: func(string) bool { return true }}
		value, _, err := w.Server.Deps().AdminTokens.Create(context.Background(), creator, admintoken.Input{
			Name: "script", Permissions: permissions, ExpiresAt: time.Now().Add(24 * time.Hour),
		}, auth.AllPermissions)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	// Changes to accounts take a token with every permission of its account
	editor := token(routetest.RoleIDs["global_admin"], "e2e_global_admin", auth.AllPermissions...)
	got := w.SignedOut().Do("POST", "/api/users/bulk/disable", `{"ids":[`+carol+`,2]}`, "Authorization", "Bearer "+editor)
	if o := outcomes(t, got); o != "carol:changed | e2e_super_admin:refused:Cannot assign a role higher than your own" {
		t.Errorf("token: %s", o)
	}
	viewer := token(routetest.RoleIDs["viewer"], "e2e_viewer", auth.AllPermissions...)
	if got := w.SignedOut().Do("POST", "/api/users/bulk/enable", `{"ids":[`+carol+`]}`, "Authorization", "Bearer "+viewer); got.Status != http.StatusForbidden || strings.Contains(got.Body, "token_not_full") {
		t.Errorf("the token of an account without user.edit: %d %s", got.Status, got.Body)
	}
	partial := token(routetest.RoleIDs["global_admin"], "e2e_global_admin", auth.PermUserView, auth.PermUserEdit)
	if got := w.SignedOut().Do("POST", "/api/users/bulk/enable", `{"ids":[`+carol+`]}`, "Authorization", "Bearer "+partial); got.Status != http.StatusForbidden {
		t.Errorf("a token with only some permissions: %d %s", got.Status, got.Body)
	}
}

// The queries of the actions and the import run on both databases: the
// count of Super admins a dry run keeps, and the names and language an
// import writes.
func TestBulkAndImportOnEachDatabase(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := routetest.New(t, routetest.Options{DB: db})
		// The test accounts came with their ids: PostgreSQL's counter starts after them
		exec(t, w, w.DB.Pick(`SELECT 1`, `SELECT setval(pg_get_serial_sequence('users', 'id'), 100)`))
		admin := w.As("admin")
		sam := makeAccount(t, w, admin, "sam", "")
		exec(t, w, `UPDATE users SET role = 'super_admin' WHERE id = ?`, sam)
		exec(t, w, `UPDATE users SET role = 'viewer' WHERE id = 1`)
		if got := outcomes(t, admin.Do("POST", "/api/users/bulk/disable", `{"ids":[2,`+sam+`],"dry_run":true}`)); got != "dry | e2e_super_admin:changed | sam:refused:users.last_super_admin" {
			t.Errorf("dry run: %s", got)
		}
		results := importResults(t, admin.Do("POST", "/api/users/import", importBody(t, "username,first name,last name,language\nzoe,Zoe,Zander,DE\n", "none")))
		if len(results) != 1 || results[0].Outcome != "created" {
			t.Fatalf("import: %+v", results)
		}
		if got := text(t, w, `SELECT first_name || ' ' || last_name || ' ' || preferred_language FROM users WHERE username = 'zoe'`); got != "Zoe Zander de" {
			t.Errorf("zoe's row: %q", got)
		}
	})
}
