package users_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
)

// switchNotice sets a security notice's switch in the server's
// configuration.
func switchNotice(t *testing.T, w *routetest.World, key, value string) {
	t.Helper()
	ctx := context.Background()
	if _, err := w.DB.Exec(ctx, `CREATE TABLE IF NOT EXISTS server_config (key TEXT PRIMARY KEY, value TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.DB.Exec(ctx, `DELETE FROM server_config WHERE key = ?`, key); err != nil {
		t.Fatal(err)
	}
	if _, err := w.DB.Exec(ctx, `INSERT INTO server_config (key, value) VALUES (?, ?)`, key, value); err != nil {
		t.Fatal(err)
	}
}

// A sign-in from a browser the account has not used before mails the
// person, with when, from where and which browser; the first browser of an
// account and a known one do not. The switch turns it off.
func TestNewSignInNotice(t *testing.T) {
	w, box := mailWorld(t, true)
	admin := w.As("admin")
	makeAccount(t, w, admin, "kim", "kim@example.test")
	const firefox = "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:140.0) Gecko/20100101 Firefox/140.0"
	login := `{"username":"kim","password":"Old-Pass-123!"}`

	first := w.SignedOut()
	expect(t, "first browser", first.Do("POST", "/api/auth/login", login), 200, "")
	expect(t, "same browser", first.Do("POST", "/api/auth/login", login), 200, "")
	if got := mailsTo(box, "kim@example.test", 300*time.Millisecond); len(got) != 0 {
		t.Fatalf("mails for known browsers: %q", subjects(got))
	}
	expect(t, "other browser", w.SignedOut().Do("POST", "/api/auth/login", login, "User-Agent", firefox, "X-Forwarded-For", "203.0.113.7"), 200, "")
	got := mailsTo(box, "kim@example.test", 300*time.Millisecond)
	if len(got) != 1 || got[0].Subject != "New sign-in to your account kim" {
		t.Fatalf("mails: %q", subjects(got))
	}
	text := *got[0].Text
	for _, want := range []string{"IP address: 203.0.113.7", "Browser: Firefox on Windows", "If it wasn't, change your password", "Open your account:\nhttps://desk.example/account"} {
		if !strings.Contains(text, want) {
			t.Errorf("no %q in\n%s", want, text)
		}
	}

	switchNotice(t, w, "security_notice_new_signin", "false")
	expect(t, "switched off", w.SignedOut().Do("POST", "/api/auth/login", login), 200, "")
	if got := mailsTo(box, "kim@example.test", 300*time.Millisecond); len(got) != 1 {
		t.Errorf("mails with the switch off: %q", subjects(got))
	}
}

// Changing one's own password mails the person; with the switch off it
// doesn't.
func TestPasswordChangedNotice(t *testing.T) {
	w, box := mailWorld(t, true)
	admin := w.As("admin")
	makeAccount(t, w, admin, "lou", "lou@example.test")
	lou := w.SignedOut()
	expect(t, "sign in", lou.Do("POST", "/api/auth/login", `{"username":"lou","password":"Old-Pass-123!"}`), 200, "")
	expect(t, "change", lou.Do("POST", "/api/auth/password", `{"currentPassword":"Old-Pass-123!","newPassword":"New-Pass-456!","confirmPassword":"New-Pass-456!"}`), 200, "")
	got := mailsTo(box, "lou@example.test", 300*time.Millisecond)
	if len(got) != 1 || got[0].Subject != "The password of lou was changed" || !strings.Contains(*got[0].Text, "The password of your account lou") {
		t.Fatalf("mails: %q", subjects(got))
	}
	if !strings.Contains(*got[0].HTML, "Your password was changed") {
		t.Error("the HTML part lacks the heading")
	}
	// In the person's language
	mia := makeAccount(t, w, admin, "mia", "mia@example.test")
	if _, err := w.DB.Exec(context.Background(), `UPDATE users SET preferred_language = 'de' WHERE id = ?`, mia); err != nil {
		t.Fatal(err)
	}
	expect(t, "admin sets mia's password", admin.Do("POST", "/api/users/"+mia+"/reset-password", `{"newPassword":"Admin-Set-123!"}`), 200, "")
	if got := mailsTo(box, "mia@example.test", 300*time.Millisecond); len(got) != 1 || got[0].Subject != "Das Passwort von mia wurde geändert" ||
		!strings.Contains(*got[0].Text, "Ein Admin hat ein neues Passwort") {
		t.Errorf("mia's mail: %q", subjects(got))
	}

	switchNotice(t, w, "security_notice_password_changed", "false")
	expect(t, "change again", lou.Do("POST", "/api/auth/password", `{"currentPassword":"New-Pass-456!","newPassword":"Third-Pass-789!","confirmPassword":"Third-Pass-789!"}`), 200, "")
	if got := mailsTo(box, "lou@example.test", 300*time.Millisecond); len(got) != 1 {
		t.Errorf("mails with the switch off: %q", subjects(got))
	}
}

// Forgot password: one address gets five requests worked on in 15
// minutes; the sixth is answered the same and sends nothing. Another
// address has its own five.
func TestForgotPasswordPerAddress(t *testing.T) {
	w, box := mailWorld(t, true)
	admin := w.As("admin")
	for i := 1; i <= 7; i++ {
		makeAccount(t, w, admin, "per"+strconv.Itoa(i), "per"+strconv.Itoa(i)+"@example.test")
	}
	person := w.SignedOut()
	for i := 1; i <= 6; i++ {
		expect(t, "request", person.Do("POST", "/api/auth/password-reset/request", `{"login":"per`+strconv.Itoa(i)+`"}`, "X-Forwarded-For", "198.51.100.4"), 200, `{"success":true}`)
	}
	time.Sleep(300 * time.Millisecond)
	if n := box.count(); n != 5 {
		t.Errorf("%d mails for six requests from one address", n)
	}
	expect(t, "other address", person.Do("POST", "/api/auth/password-reset/request", `{"login":"per7"}`, "X-Forwarded-For", "198.51.100.5"), 200, `{"success":true}`)
	waitForMails(t, box, 6)
}
