package users_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
)

// withTwoFactor gives an account a second factor and two recovery codes
// as the set-up leaves them.
func withTwoFactor(t *testing.T, w *routetest.World, id string) {
	t.Helper()
	if _, err := w.DB.Exec(context.Background(), `UPDATE users SET totp_enabled = 1, totp_secret = 'JBSWY3DPEHPK3PXP', totp_recovery_codes = '["a","b"]' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
}

func column(t *testing.T, w *routetest.World, query, id string) string {
	t.Helper()
	var v *string
	if err := w.DB.QueryRow(context.Background(), query, id).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v == nil {
		return "NULL"
	}
	return *v
}

// Two-factor sign-in required of one account: the list says required
// until it is set up, the next sign-in asks for the set-up, and taking it
// back leaves the account to the server-wide switch. A directory account
// is refused.
func TestTwoFactorRequiredForOneAccount(t *testing.T) {
	w, _ := mailWorld(t, true)
	admin := w.As("admin")
	id := makeAccount(t, w, admin, "fred", "")
	if s := listedAs(t, admin, "fred"); s.TwoFactorState != "off" || s.TwoFactorRequired {
		t.Fatalf("before: %+v", s)
	}
	expect(t, "require", admin.Do("PUT", "/api/users/"+id+"/two-factor-required", `{"required":true}`), 200, `{"success":true}`)
	if s := listedAs(t, admin, "fred"); s.TwoFactorState != "required" || !s.TwoFactorRequired {
		t.Errorf("required: %+v", s)
	}
	expect(t, "sign-in", signIn(w, "fred", "Old-Pass-123!"), 200, `{"success":true,"totpSetupRequired":true}`)
	got := w.SignedOut().Do("POST", "/api/login", `{"username":"fred","password":"Old-Pass-123!","id":"900400009"}`)
	if got.Status != http.StatusForbidden || !strings.Contains(got.Body, `"code":"totp_setup_required"`) {
		t.Errorf("client sign-in: %d %s", got.Status, got.Body)
	}
	expect(t, "take back", admin.Do("PUT", "/api/users/"+id+"/two-factor-required", `{"required":false}`), 200, `{"success":true}`)
	if got := signIn(w, "fred", "Old-Pass-123!"); got.Status != http.StatusOK || strings.Contains(got.Body, "totpSetupRequired") {
		t.Errorf("sign-in after: %d %s", got.Status, got.Body)
	}
	withTwoFactor(t, w, id)
	if s := listedAs(t, admin, "fred"); s.TwoFactorState != "on" {
		t.Errorf("set up: %+v", s)
	}
	rows := strings.Join(auditRowsAbout(t, w, id), "\n")
	if !strings.Contains(rows, "two_factor_required|Requires two-factor sign-in of user: fred|1") ||
		!strings.Contains(rows, "two_factor_requirement_removed|No longer requires two-factor sign-in of user: fred|1") {
		t.Errorf("audit:\n%s", rows)
	}
	expect(t, "bad value", admin.Do("PUT", "/api/users/"+id+"/two-factor-required", `{"required":"yes"}`), 400, "")
	expect(t, "own", admin.Do("PUT", "/api/users/1/two-factor-required", `{"required":true}`), 400, "")
	expect(t, "viewer", w.As("viewer").Do("PUT", "/api/users/"+id+"/two-factor-required", `{"required":true}`), 403, "")
	if _, err := w.DB.Exec(context.Background(), `UPDATE users SET auth_provider = 'oidc' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	expect(t, "single sign-on", admin.Do("PUT", "/api/users/"+id+"/two-factor-required", `{"required":true}`), 400,
		`{"success":false,"error":"This account signs in with single sign-on. Its two-factor sign-in is set there."}`)
}

// Reset two-factor where it is required: the secret and the codes go, the
// sessions end, and the next sign-in sets it up again; the person gets a
// mail. Where nothing requires it, the same is turning it off, audited as
// such. Recovery codes made unusable stop working without new ones.
func TestResetTwoFactor(t *testing.T) {
	w, box := mailWorld(t, true)
	admin := w.As("admin")
	id := makeAccount(t, w, admin, "gina", "gina@example.test")
	withTwoFactor(t, w, id)
	gina := w.SignedOut()
	// The session as the second factor would leave it
	if got := gina.Do("POST", "/api/auth/login", `{"username":"gina","password":"Old-Pass-123!"}`); !strings.Contains(got.Body, "totpRequired") {
		t.Fatalf("sign-in: %s", got.Body)
	}

	expect(t, "invalidate codes", admin.Do("POST", "/api/users/"+id+"/recovery-codes/invalidate", `{}`), 200, `{"success":true}`)
	if got := column(t, w, `SELECT totp_recovery_codes FROM users WHERE id = ?`, id); got != "[]" {
		t.Errorf("codes after: %s", got)
	}
	if got := column(t, w, `SELECT totp_secret FROM users WHERE id = ?`, id); got != "JBSWY3DPEHPK3PXP" {
		t.Errorf("the secret changed: %s", got)
	}

	expect(t, "require", admin.Do("PUT", "/api/users/"+id+"/two-factor-required", `{"required":true}`), 200, "")
	expect(t, "reset", admin.Do("POST", "/api/users/"+id+"/two-factor/reset", `{}`), 200, `{"success":true,"data":{"set_up_again":true}}`)
	if s := listedAs(t, admin, "gina"); s.TwoFactorState != "required" {
		t.Errorf("after the reset: %+v", s)
	}
	expect(t, "sign-in", signIn(w, "gina", "Old-Pass-123!"), 200, `{"success":true,"totpSetupRequired":true}`)
	expect(t, "nothing to reset", admin.Do("POST", "/api/users/"+id+"/two-factor/reset", `{}`), 400,
		`{"success":false,"error":"This account has no two-factor sign-in."}`)

	expect(t, "not required", admin.Do("PUT", "/api/users/"+id+"/two-factor-required", `{"required":false}`), 200, "")
	withTwoFactor(t, w, id)
	expect(t, "turn off", admin.Do("POST", "/api/users/"+id+"/two-factor/reset", `{}`), 200, `{"success":true,"data":{"set_up_again":false}}`)
	if s := listedAs(t, admin, "gina"); s.TwoFactorState != "off" {
		t.Errorf("after turning off: %+v", s)
	}
	rows := strings.Join(auditRowsAbout(t, w, id), "\n")
	for _, want := range []string{
		"recovery_codes_invalidated|Made the recovery codes of user gina stop working|1",
		"two_factor_reset|Reset two-factor sign-in for user: gina|1",
		"totp_disabled_by_admin|Turned off two-factor sign-in for user: gina|1",
	} {
		if !strings.Contains(rows, want) {
			t.Errorf("no %q in\n%s", want, rows)
		}
	}
	mails := subjects(mailsTo(box, "gina@example.test", 400*time.Millisecond))
	if len(mails) != 3 {
		t.Errorf("mails to gina: %q", mails)
	}
	var texts []string
	for _, m := range mailsTo(box, "gina@example.test", 0) {
		texts = append(texts, *m.Text)
	}
	all := strings.Join(texts, "\n")
	for _, want := range []string{"made the recovery codes", "reset two-factor sign-in", "turned off two-factor sign-in"} {
		if !strings.Contains(all, want) {
			t.Errorf("no mail says %q:\n%s", want, all)
		}
	}
	expect(t, "own", admin.Do("POST", "/api/users/1/two-factor/reset", `{}`), 400, "")
	expect(t, "viewer", w.As("viewer").Do("POST", "/api/users/"+id+"/recovery-codes/invalidate", `{}`), 403, "")
}

// Turning off two-factor sign-in through the account change needs
// user.security too.
func TestTurnOffTwoFactorNeedsSecurity(t *testing.T) {
	w := routetest.New(t, routetest.Options{Roles: routetest.Overrides{"global_admin": {"user.security": false}}})
	if _, err := w.DB.Exec(context.Background(), `UPDATE users SET totp_enabled = 1, totp_secret = 'JBSWY3DPEHPK3PXP' WHERE id = 6`); err != nil {
		t.Fatal(err)
	}
	got := w.As("global_admin").Do("PATCH", "/api/users/6", `{"totp_enabled":false}`)
	if got.Status != http.StatusForbidden {
		t.Errorf("without user.security: %d %s", got.Status, got.Body)
	}
	if got := w.As("super_admin").Do("PATCH", "/api/users/6", `{"totp_enabled":false}`); got.Status != http.StatusOK {
		t.Errorf("with it: %d %s", got.Status, got.Body)
	}
}

// A waiting reset link: the list and the dialog say until when, and the
// admin can cancel it; the link then stops working.
func TestCancelResetLink(t *testing.T) {
	w, box := mailWorld(t, true)
	admin := w.As("admin")
	id := makeAccount(t, w, admin, "hal", "hal@example.test")
	got := admin.Do("POST", "/api/users/"+id+"/password-reset-link", "{}")
	if got.Status != http.StatusOK || !strings.Contains(got.Body, `"expires_at":"`) {
		t.Fatalf("link: %d %s", got.Status, got.Body)
	}
	token := tokenOf(t, box.last(t))
	state := listedAs(t, admin, "hal")
	if state.PasswordResetUntil == nil {
		t.Fatal("the list doesn't show the waiting link")
	}
	if until, err := time.Parse(time.RFC3339, *state.PasswordResetUntil); err != nil || time.Until(until) < 23*time.Hour {
		t.Errorf("until %v %v", until, err)
	}
	expect(t, "cancel", admin.Do("DELETE", "/api/users/"+id+"/password-reset-link", ""), 200, `{"success":true}`)
	expect(t, "lookup", w.SignedOut().Do("POST", "/api/auth/password-link/lookup", `{"token":"`+token+`"}`), 404, "")
	if listedAs(t, admin, "hal").PasswordResetUntil != nil {
		t.Error("the list still shows the link")
	}
	expect(t, "cancel again", admin.Do("DELETE", "/api/users/"+id+"/password-reset-link", ""), 404,
		`{"success":false,"error":"No reset link is waiting for this account."}`)
	if rows := strings.Join(auditRowsAbout(t, w, id), "\n"); !strings.Contains(rows, "password_reset_cancelled|Cancelled the password reset link of user: hal|1") {
		t.Errorf("audit:\n%s", rows)
	}
	expect(t, "viewer", w.As("viewer").Do("DELETE", "/api/users/"+id+"/password-reset-link", ""), 403, "")
}

// The reset mail is in the person's language, not the admin's; without a
// preferred language it is in the console's default.
func TestResetMailInTheRecipientsLanguage(t *testing.T) {
	w, box := mailWorld(t, true)
	admin := w.As("admin")
	id := makeAccount(t, w, admin, "ida", "ida@example.test")
	if _, err := w.DB.Exec(context.Background(), `UPDATE users SET preferred_language = 'de' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	expect(t, "link", admin.Do("POST", "/api/users/"+id+"/password-reset-link", "{}", "Accept-Language", "en"), 200, "")
	if m := box.last(t); m.Subject != "Neues Passwort für NeoDesk festlegen" {
		t.Errorf("subject %q", m.Subject)
	}
	other := makeAccount(t, w, admin, "jon", "jon@example.test")
	expect(t, "link", admin.Do("POST", "/api/users/"+other+"/password-reset-link", "{}", "Accept-Language", "de", "Cookie", "lang=de"), 200, "")
	if m := box.last(t); m.Subject != "Set a new password for NeoDesk" {
		t.Errorf("subject without a preferred language %q", m.Subject)
	}
}
