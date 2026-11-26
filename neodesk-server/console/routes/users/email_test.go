package users_test

import (
	"context"
	"encoding/json"
	"fmt"
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

// A272: without mail settings nothing can confirm a new address, so a
// person can't change their own: Your account, the operator profile and
// the admin's own row in Users all refuse with email_change_needs_mail and
// leave the address. Taking it away still works, and so does a profile
// save that keeps the address.
func TestOwnEmailChangeWithoutMailIsRefused(t *testing.T) {
	w, box := mailWorld(t, false)
	operatorID := routetest.RoleIDs["operator"]
	adminID := routetest.RoleIDs["admin"]
	setColumn(t, w, operatorID, "email", "op@example.test")
	setColumn(t, w, adminID, "email", "admin@example.test")
	operator := w.As("operator")
	refused := func(name string, got routetest.Answer) {
		t.Helper()
		var body struct {
			Success bool   `json:"success"`
			Error   string `json:"error"`
			Code    string `json:"code"`
		}
		_ = json.Unmarshal([]byte(got.Body), &body)
		if got.Status != http.StatusConflict || body.Success || body.Code != "email_change_needs_mail" || !strings.Contains(body.Error, "Settings, Email") {
			t.Errorf("%s: %d %s", name, got.Status, got.Body)
		}
	}

	refused("Your account", operator.Do("POST", "/api/users/me/email", `{"email":"mine@example.test"}`))
	refused("operator profile", operator.Do("PUT", "/api/users/me/profile", `{"email":"profile@example.test","phone":"555"}`))
	refused("own row in Users", w.As("admin").Do("PATCH", fmt.Sprintf("/api/users/%d", adminID), `{"email":"other@example.test"}`))
	if e := emailOf(t, w, operatorID); e != "op@example.test" {
		t.Errorf("the operator's address changed: %q", e)
	}
	if e := emailOf(t, w, adminID); e != "admin@example.test" {
		t.Errorf("the admin's address changed: %q", e)
	}
	if _, _, _, found := auditAbout(t, w, "email_changed"); found || box.count() != 0 {
		t.Errorf("a refused change was audited or mailed: %v, %d mails", found, box.count())
	}
	// The profile's other fields are saved
	var phone string
	if err := w.DB.QueryRow(context.Background(), `SELECT COALESCE(phone, '') FROM users WHERE id = ?`, operatorID).Scan(&phone); err != nil || phone != "555" {
		t.Errorf("phone after the refused address: %q %v", phone, err)
	}

	expect(t, "profile with the same address", operator.Do("PUT", "/api/users/me/profile", `{"email":"op@example.test","phone":"556"}`), 200, "")
	expectHas(t, "take the address away", operator.Do("POST", "/api/users/me/email", `{"email":""}`), 200, `"email":""`)
	if e := emailOf(t, w, operatorID); e != "" {
		t.Errorf("after taking it away: %q", e)
	}
}

// A274: a save whose new address is refused keeps its other fields and
// says so: the answer has the refusal's status and code, names the
// fields it saved, and its text says only the address stayed, and why.
// The mail server's refusal of the confirmation has a code of its own,
// and the operator profile still fetches the picture. Your account sends
// only the address, so its answer is the refusal alone.
func TestRefusedAddressKeepsTheRestOfTheSave(t *testing.T) {
	w, box := mailWorld(t, true)
	box.fail = true
	operatorID := routetest.RoleIDs["operator"]
	adminID := routetest.RoleIDs["admin"]
	viewerID := routetest.RoleIDs["viewer"]
	setColumn(t, w, operatorID, "email", "op@example.test")
	setColumn(t, w, adminID, "email", "admin@example.test")
	setColumn(t, w, viewerID, "email", "viewer@example.test")
	why := langText(t, "en", "users.email_confirm_mail_failed")
	rest := strings.ReplaceAll(langText(t, "en", "users.email_not_changed_rest_saved"), "{reason}", why)
	type answer struct {
		Success     bool     `json:"success"`
		Error       string   `json:"error"`
		Code        string   `json:"code"`
		Saved       []string `json:"saved"`
		Avatar      string   `json:"avatar"`
		AvatarError string   `json:"avatar_error"`
	}
	refused := func(name string, got routetest.Answer, text string, saved ...string) answer {
		t.Helper()
		var a answer
		if err := json.Unmarshal([]byte(got.Body), &a); err != nil {
			t.Fatalf("%s: %d %s", name, got.Status, got.Body)
		}
		if got.Status != http.StatusBadGateway || a.Success || a.Code != "email_confirmation_not_sent" || a.Error != text || strings.Join(a.Saved, " ") != strings.Join(saved, " ") {
			t.Errorf("%s: %d %s, want 502 email_confirmation_not_sent %q saved %v", name, got.Status, got.Body, text, saved)
		}
		return a
	}
	operator, admin := w.As("operator"), w.As("admin")

	got := operator.Do("POST", "/api/users/me/email", `{"email":"mine@example.test"}`)
	refused("Your account", got, why)
	if strings.Contains(got.Body, `"saved"`) {
		t.Errorf("Your account names saved fields: %s", got.Body)
	}

	a := refused("operator profile", operator.Do("PUT", "/api/users/me/profile",
		`{"email":"profile@example.test","phone":"555","time_zone":"Europe/Berlin","avatar_url":"http://127.0.0.1/x.png"}`),
		rest, "phone", "avatar_url", "time_zone")
	if a.Avatar != "failed" || a.AvatarError != "fetch_failed" {
		t.Errorf("the picture step after the refused address: %q %q", a.Avatar, a.AvatarError)
	}
	var phone, zone string
	if err := w.DB.QueryRow(context.Background(), `SELECT COALESCE(phone, '') FROM users WHERE id = ?`, operatorID).Scan(&phone); err != nil || phone != "555" {
		t.Errorf("phone: %q %v", phone, err)
	}
	if err := w.DB.QueryRow(context.Background(), `SELECT COALESCE(time_zone, '') FROM account_profile WHERE user_id = ?`, operatorID).Scan(&zone); err != nil || zone != "Europe/Berlin" {
		t.Errorf("time zone: %q %v", zone, err)
	}

	// Users: the own row and another account's, each with a field beside the address
	refused("own row in Users", admin.Do("PATCH", fmt.Sprintf("/api/users/%d", adminID), `{"email":"other@example.test","strategyGuid":""}`), rest, "strategyGuid")
	refused("another account in Users", admin.Do("PATCH", fmt.Sprintf("/api/users/%d", viewerID), `{"email":"v2@example.test","inactive_exempt":true}`), rest, "inactive_exempt")
	if _, target, _, found := auditAbout(t, w, "user_inactive_exempt_set"); !found || target != viewerID {
		t.Errorf("the viewer was not exempted beside the refused address: %v %d", found, target)
	}
	if _, _, details, found := auditAbout(t, w, "user_updated"); !found || !strings.Contains(details, "e2e_viewer") {
		t.Errorf("no audit of the update beside the refused address: %v %q", found, details)
	}

	for id, want := range map[int64]string{operatorID: "op@example.test", adminID: "admin@example.test", viewerID: "viewer@example.test"} {
		if e := emailOf(t, w, id); e != want {
			t.Errorf("account %d: address %q, want %q", id, e, want)
		}
	}
	// Only the address: the refusal as it was
	refused("only the address in Users", admin.Do("PATCH", fmt.Sprintf("/api/users/%d", viewerID), `{"email":"v3@example.test"}`), why)
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
