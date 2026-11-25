package users_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
)

// makeAccount adds a local account with a password and an address.
func makeAccount(t *testing.T, w *routetest.World, admin *routetest.Agent, name, email string) string {
	t.Helper()
	got := admin.Do("POST", "/api/users", `{"username":"`+name+`","password":"Old-Pass-123!","role":"viewer","email":"`+email+`"}`)
	if got.Status != http.StatusOK {
		t.Fatalf("create %s: %d %s", name, got.Status, got.Body)
	}
	return createdID(t, got)
}

func signsIn(w *routetest.World, name, password string) bool {
	return w.SignedOut().Do("POST", "/api/auth/login", `{"username":"`+name+`","password":"`+password+`"}`).Status == http.StatusOK
}

// waitForMails waits until the outbox holds n mails: the sign-in page's
// request sends after its answer.
func waitForMails(t *testing.T, box *outbox, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for box.count() < n {
		if time.Now().After(deadline) {
			t.Fatalf("%d mails, want %d", box.count(), n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// An admin mails a reset link: the old password works until the link is
// used, the new one after, and the session of the account ends. Without an
// address or mail settings nothing is sent.
func TestResetLinkFromAdmin(t *testing.T) {
	w, box := mailWorld(t, true)
	admin := w.As("admin")
	id := makeAccount(t, w, admin, "carol", "carol@example.test")
	carol := w.SignedOut()
	if got := carol.Do("POST", "/api/auth/login", `{"username":"carol","password":"Old-Pass-123!"}`); got.Status != http.StatusOK {
		t.Fatalf("sign in: %d %s", got.Status, got.Body)
	}

	expect(t, "reset link", admin.Do("POST", "/api/users/"+id+"/password-reset-link", "{}"), 200, `{"success":true}`)
	m := box.last(t)
	if strings.Join(m.To, ",") != "carol@example.test" || !strings.Contains(m.Subject, "new password") {
		t.Fatalf("mail to %v, subject %q", m.To, m.Subject)
	}
	token := tokenOf(t, m)
	if len(m.Inline) != 2 || !strings.Contains(*m.HTML, "cid:"+m.Inline[0].ID) {
		t.Errorf("the mail doesn't carry the logo: %d images", len(m.Inline))
	}
	if !signsIn(w, "carol", "Old-Pass-123!") {
		t.Fatal("the old password stopped working before the link was used")
	}
	got := w.SignedOut().Do("POST", "/api/auth/password-link/lookup", `{"token":"`+token+`"}`)
	if !strings.Contains(got.Body, `"purpose":"reset"`) {
		t.Fatalf("lookup: %d %s", got.Status, got.Body)
	}
	expect(t, "redeem", w.SignedOut().Do("POST", "/api/auth/password-link/redeem", `{"token":"`+token+`","password":"New-Pass-456!"}`), 200, "")
	if signsIn(w, "carol", "Old-Pass-123!") || !signsIn(w, "carol", "New-Pass-456!") {
		t.Error("the password didn't change")
	}
	if got := carol.Do("GET", "/api/auth/verify", ""); got.Status != http.StatusUnauthorized {
		t.Errorf("carol's session goes on: %d", got.Status)
	}
	if !strings.Contains(strings.Join(w.AuditRows(), "\n"), "password_reset_by_link|Password set from a reset link") {
		t.Errorf("no audit row:\n%s", strings.Join(w.AuditRows(), "\n"))
	}

	noMail := makeAccount(t, w, admin, "dave", "")
	expect(t, "no address", admin.Do("POST", "/api/users/"+noMail+"/password-reset-link", "{}"), 400, "")
	expect(t, "viewer", w.As("viewer").Do("POST", "/api/users/"+id+"/password-reset-link", "{}"), 403, "")
	if _, err := w.DB.Exec(context.Background(), `DELETE FROM settings WHERE key = 'smtp_config'`); err != nil {
		t.Fatal(err)
	}
	expect(t, "no smtp", admin.Do("POST", "/api/users/"+id+"/password-reset-link", "{}"), 409, "")
}

// Forgot password: the answer is the same for a known name, a known
// address in other letter case and nothing known; a known account gets a
// link for an hour, once in two minutes; the page offers it only with mail
// settings.
func TestForgotPassword(t *testing.T) {
	w, box := mailWorld(t, true)
	admin := w.As("admin")
	makeAccount(t, w, admin, "erin", "Erin@Example.test")
	person := w.SignedOut()
	expect(t, "available", person.Do("GET", "/api/auth/password-reset/available", ""), 200, `{"success":true,"data":{"available":true}}`)

	for _, login := range []string{"nobody", "nobody@example.test", "", "erin"} {
		expect(t, "request "+login, person.Do("POST", "/api/auth/password-reset/request", `{"login":"`+login+`"}`), 200, `{"success":true}`)
	}
	waitForMails(t, box, 1)
	m := box.last(t)
	if strings.Join(m.To, ",") != "Erin@Example.test" {
		t.Fatalf("mail to %v", m.To)
	}
	if !strings.Contains(*m.Text, "Someone asked for a new password") {
		t.Errorf("the mail doesn't say someone asked:\n%s", *m.Text)
	}
	first := tokenOf(t, m)
	if len(m.Inline) != 2 || !strings.Contains(*m.HTML, "cid:"+m.Inline[0].ID) {
		t.Errorf("the mail doesn't carry the logo: %d images", len(m.Inline))
	}
	got := person.Do("POST", "/api/auth/password-link/lookup", `{"token":"`+first+`"}`)
	if !strings.Contains(got.Body, `"purpose":"reset"`) {
		t.Fatalf("lookup: %s", got.Body)
	}
	var expires int64
	if err := w.DB.QueryRow(context.Background(), `SELECT expires_ms - created_ms FROM password_links`).Scan(&expires); err != nil || time.Duration(expires)*time.Millisecond != time.Hour {
		t.Errorf("lifetime %v ms, %v", expires, err)
	}

	// Asked again right away, by address: no second mail, the link stays
	expect(t, "again", person.Do("POST", "/api/auth/password-reset/request", `{"login":"erin@example.TEST"}`), 200, `{"success":true}`)
	time.Sleep(200 * time.Millisecond)
	if box.count() != 1 {
		t.Errorf("%d mails after a second request", box.count())
	}
	if got := person.Do("POST", "/api/auth/password-link/lookup", `{"token":"`+first+`"}`); got.Status != http.StatusOK {
		t.Errorf("the first link stopped working: %d", got.Status)
	}

	if _, err := w.DB.Exec(context.Background(), `DELETE FROM settings WHERE key = 'smtp_config'`); err != nil {
		t.Fatal(err)
	}
	expect(t, "unavailable", person.Do("GET", "/api/auth/password-reset/available", ""), 200, `{"success":true,"data":{"available":false}}`)
}
