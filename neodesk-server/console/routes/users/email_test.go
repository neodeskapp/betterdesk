package users_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/mail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
)

var confirmPattern = regexp.MustCompile(`https://desk\.example/confirm-email#([A-Za-z0-9_-]+)`)

// confirmTokenOf is the token of the confirmation link in a mail.
func confirmTokenOf(t *testing.T, m mail.Outgoing) string {
	t.Helper()
	match := confirmPattern.FindStringSubmatch(*m.Text)
	if match == nil {
		t.Fatalf("no confirmation link in\n%s", *m.Text)
	}
	return match[1]
}

// expectHas is expect for an answer that holds part.
func expectHas(t *testing.T, what string, got routetest.Answer, status int, part string) {
	t.Helper()
	if got.Status != status || !strings.Contains(got.Body, part) {
		t.Errorf("%s: %d %s, want %d with %s", what, got.Status, got.Body, status, part)
	}
}

// mailTo returns the mails sent to an address.
func (o *outbox) mailTo(addr string) []mail.Outgoing {
	o.mu.Lock()
	defer o.mu.Unlock()
	var out []mail.Outgoing
	for _, m := range o.sent {
		if strings.Join(m.To, ",") == addr {
			out = append(out, m)
		}
	}
	return out
}

func emailOf(t *testing.T, w *routetest.World, id int64) string {
	t.Helper()
	var email string
	if err := w.DB.QueryRow(context.Background(), `SELECT COALESCE(email, '') FROM users WHERE id = ?`, id).Scan(&email); err != nil {
		t.Fatal(err)
	}
	return email
}

func setColumn(t *testing.T, w *routetest.World, id int64, column, value string) {
	t.Helper()
	if _, err := w.DB.Exec(context.Background(), `UPDATE users SET `+column+` = ? WHERE id = ?`, value, id); err != nil {
		t.Fatal(err)
	}
}

// langText is the text of a key in a language file of the console.
func langText(t *testing.T, lang, key string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "static", "files", "lang", lang+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var tree map[string]any
	if err := json.Unmarshal(raw, &tree); err != nil {
		t.Fatal(err)
	}
	var node any = tree
	for _, part := range strings.Split(key, ".") {
		node = node.(map[string]any)[part]
	}
	return node.(string)
}

// An admin's new address for an account takes effect only when the link
// mailed to it is used; the old address gets a notice. Using the link
// changes the address once, ends the account's sessions and is audited.
func TestEmailChangeWaitsForConfirmation(t *testing.T) {
	w, box := mailWorld(t, true)
	viewerID := routetest.RoleIDs["viewer"]
	setColumn(t, w, viewerID, "email", "old@example.test")
	viewer := w.As("viewer")
	admin := w.As("admin")

	got := admin.Do("PATCH", "/api/users/6", `{"email":"new@example.test"}`)
	if got.Status != http.StatusOK || !strings.Contains(got.Body, `"email":"old@example.test"`) || !strings.Contains(got.Body, `"email_pending":{"email":"new@example.test"`) {
		t.Fatalf("change: %d %s", got.Status, got.Body)
	}
	if e := emailOf(t, w, viewerID); e != "old@example.test" {
		t.Fatalf("the address changed before the confirmation: %q", e)
	}
	confirm := box.mailTo("new@example.test")
	notice := box.mailTo("old@example.test")
	if len(confirm) != 1 || len(notice) != 1 {
		t.Fatalf("mails: %d to the new address, %d to the old", len(confirm), len(notice))
	}
	if !strings.Contains(*notice[0].Text, "new@example.test") || confirm[0].HTML == nil || len(confirm[0].Inline) == 0 {
		t.Errorf("the notice doesn't name the new address, or the confirmation has no branded part:\n%s", *notice[0].Text)
	}
	if actor, target, _, found := auditAbout(t, w, "email_change_requested"); !found || actor != routetest.RoleIDs["admin"] || target != viewerID {
		t.Errorf("audit of the request: %v %d %d", found, actor, target)
	}
	if got := admin.Do("GET", "/api/users", ""); !strings.Contains(got.Body, `"email_pending":{"email":"new@example.test"`) {
		t.Errorf("the list doesn't show the pending address: %s", got.Body)
	}
	token := confirmTokenOf(t, confirm[0])

	person := w.SignedOut()
	expectHas(t, "lookup", person.Do("POST", "/api/auth/email-change/lookup", `{"token":"`+token+`"}`), 200, `"email":"new@example.test"`)
	expectHas(t, "confirm", person.Do("POST", "/api/auth/email-change/confirm", `{"token":"`+token+`"}`), 200, `"username":"e2e_viewer"`)
	if e := emailOf(t, w, viewerID); e != "new@example.test" {
		t.Fatalf("after the confirmation: %q", e)
	}
	expect(t, "used link", person.Do("POST", "/api/auth/email-change/confirm", `{"token":"`+token+`"}`), 404, "")
	expect(t, "viewer's session", viewer.Do("GET", "/api/auth/verify", ""), 401, "")
	if _, target, details, found := auditAbout(t, w, "email_changed"); !found || target != viewerID || !strings.Contains(details, "old@example.test") {
		t.Errorf("audit of the change: %v %d %q", found, target, details)
	}
	if got := admin.Do("GET", "/api/users", ""); strings.Contains(got.Body, `"email_pending"`) {
		t.Errorf("still pending: %s", got.Body)
	}
}

// The person changes their own address on Your account the same way, and
// can send the link again or cancel. Without the notice switch the old
// address hears nothing. The confirmation is in the person's language.
func TestOwnEmailChange(t *testing.T) {
	w, box := mailWorld(t, true)
	if _, err := w.DB.Exec(context.Background(), `CREATE TABLE IF NOT EXISTS server_config (key TEXT PRIMARY KEY, value TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.DB.Exec(context.Background(), `INSERT INTO server_config (key, value) VALUES ('security_notice_email_changed', 'false')`); err != nil {
		t.Fatal(err)
	}
	viewerID := routetest.RoleIDs["viewer"]
	setColumn(t, w, viewerID, "email", "old@example.test")
	setColumn(t, w, viewerID, "preferred_language", "de")
	viewer := w.As("viewer")

	got := viewer.Do("POST", "/api/users/me/email", `{"email":"mine@example.test"}`)
	if got.Status != http.StatusOK || !strings.Contains(got.Body, `"email":"old@example.test"`) || !strings.Contains(got.Body, `"email_pending":{"email":"mine@example.test"`) {
		t.Fatalf("own change: %d %s", got.Status, got.Body)
	}
	if n := len(box.mailTo("old@example.test")); n != 0 {
		t.Errorf("the notice went out with the switch off: %d", n)
	}
	first := box.mailTo("mine@example.test")
	if len(first) != 1 {
		t.Fatalf("confirmation mails: %d", len(first))
	}
	if want := strings.ReplaceAll(langText(t, "de", "email_change.confirm_subject"), "{server}", "NeoDesk"); first[0].Subject != want {
		t.Errorf("subject %q, want the German %q", first[0].Subject, want)
	}
	expectHas(t, "resend", viewer.Do("POST", "/api/users/me/email-change/resend", "{}"), 200, `"email_pending":{"email":"mine@example.test"`)
	second := box.mailTo("mine@example.test")
	if len(second) != 2 {
		t.Fatalf("after resend: %d mails", len(second))
	}
	expect(t, "first link after resend", w.SignedOut().Do("POST", "/api/auth/email-change/lookup", `{"token":"`+confirmTokenOf(t, second[0])+`"}`), 404, "")

	expectHas(t, "cancel", viewer.Do("DELETE", "/api/users/me/email-change", ""), 200, `"email_pending":null`)
	expect(t, "link after cancel", w.SignedOut().Do("POST", "/api/auth/email-change/confirm", `{"token":"`+confirmTokenOf(t, second[1])+`"}`), 404, "")
	expect(t, "cancel again", viewer.Do("DELETE", "/api/users/me/email-change", ""), 409, "")
	if e := emailOf(t, w, viewerID); e != "old@example.test" {
		t.Errorf("the address changed: %q", e)
	}
	// The profile route takes the same way
	viewer.Do("PUT", "/api/users/me/profile", `{"email":"profile@example.test"}`)
	if e := emailOf(t, w, viewerID); e != "old@example.test" || len(box.mailTo("profile@example.test")) != 1 {
		t.Errorf("the profile set the address at once: %q", e)
	}
}

// A directory or single sign-on account gets its address from there:
// both routes refuse to change it. Without mail settings an address
// changes at once, and the audit log says it was not confirmed.
func TestEmailChangeRefusalsAndWithoutMail(t *testing.T) {
	w, box := mailWorld(t, false)
	viewerID := routetest.RoleIDs["viewer"]
	setColumn(t, w, viewerID, "auth_provider", "ldap")
	admin := w.As("admin")
	expectHas(t, "ldap by admin", admin.Do("PATCH", "/api/users/6", `{"email":"x@example.test"}`), 400, "LDAP")
	expectHas(t, "ldap own", w.As("viewer").Do("POST", "/api/users/me/email", `{"email":"x@example.test"}`), 400, "LDAP")
	if e := emailOf(t, w, viewerID); e != "" {
		t.Errorf("the directory account's address changed: %q", e)
	}

	operatorID := routetest.RoleIDs["operator"]
	setColumn(t, w, operatorID, "email", "op@example.test")
	got := admin.Do("PATCH", "/api/users/5", `{"email":"direct@example.test"}`)
	if got.Status != http.StatusOK || emailOf(t, w, operatorID) != "direct@example.test" || box.count() != 0 {
		t.Fatalf("without mail: %d %s, %q, %d mails", got.Status, got.Body, emailOf(t, w, operatorID), box.count())
	}
	if _, target, details, found := auditAbout(t, w, "email_changed"); !found || target != operatorID || !strings.Contains(details, "without confirmation") {
		t.Errorf("audit: %v %d %q", found, target, details)
	}
	expect(t, "viewer may not cancel", w.As("viewer").Do("DELETE", "/api/users/5/email-change", ""), 403, "")
}

// A reset link an admin sends is in the language of the account it goes
// to, not in the admin's.
func TestResetLinkInTheRecipientsLanguage(t *testing.T) {
	w, box := mailWorld(t, true)
	viewerID := routetest.RoleIDs["viewer"]
	setColumn(t, w, viewerID, "email", "viewer@example.test")
	setColumn(t, w, viewerID, "preferred_language", "de")
	got := w.As("admin").Do("POST", "/api/users/6/password-reset-link", "{}", "Accept-Language", "en")
	if got.Status != http.StatusOK {
		t.Fatalf("reset link: %d %s", got.Status, got.Body)
	}
	want := strings.ReplaceAll(langText(t, "de", "password_link.reset_subject"), "{server}", "NeoDesk")
	if s := box.last(t).Subject; s != want {
		t.Errorf("subject %q, want the German %q", s, want)
	}
}
