package users_test

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/config"
	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
	"github.com/neodeskapp/neodesk/neodesk-server/console/mail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
)

// outbox is a mail transport that keeps what it is given.
type outbox struct {
	mu   sync.Mutex
	sent []mail.Outgoing
	fail bool
}

func (o *outbox) Send(_ context.Context, _ mail.Settings, m mail.Outgoing) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.fail {
		return errMailDown
	}
	o.sent = append(o.sent, m)
	return nil
}

func (o *outbox) Verify(context.Context, mail.Settings) error { return nil }

func (o *outbox) last(t *testing.T) mail.Outgoing {
	t.Helper()
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.sent) == 0 {
		t.Fatal("no mail sent")
	}
	return o.sent[len(o.sent)-1]
}

func (o *outbox) count() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.sent)
}

type mailError string

func (e mailError) Error() string { return string(e) }

const errMailDown = mailError("connection refused")

// mailWorld is a console with the real language files whose mail goes to
// an outbox, with SMTP settings when smtp is set.
func mailWorld(t *testing.T, smtp bool) (*routetest.World, *outbox) {
	t.Helper()
	lang, err := filepath.Abs(filepath.Join("..", "..", "static", "files", "lang"))
	if err != nil {
		t.Fatal(err)
	}
	trust, err := httpx.ParseTrustProxy("true")
	if err != nil {
		t.Fatal(err)
	}
	w := routetest.New(t, routetest.Options{
		Env: map[string]string{"PANEL_PUBLIC_URL": "https://desk.example"},
		Config: func(cfg *config.Config) {
			cfg.LangDir = lang
			cfg.TrustProxy = trust
		},
	})
	box := &outbox{}
	w.Server.Deps().Mail = mail.New(mail.Options{DB: w.DB, Transport: box, Getenv: func(name string) string {
		if name == "PANEL_PUBLIC_URL" {
			return "https://desk.example"
		}
		return ""
	}})
	if smtp {
		if _, err := w.DB.Exec(context.Background(), `INSERT INTO settings (key, value) VALUES ('smtp_config', '{"host":"smtp.example.test","port":25,"from":"desk@example.test"}')`); err != nil {
			t.Fatal(err)
		}
	}
	return w, box
}

var linkPattern = regexp.MustCompile(`https://desk\.example/set-password#([A-Za-z0-9_-]+)`)

// tokenOf is the token of the link in a mail.
func tokenOf(t *testing.T, m mail.Outgoing) string {
	t.Helper()
	if m.Text == nil {
		t.Fatal("mail without text")
	}
	match := linkPattern.FindStringSubmatch(*m.Text)
	if match == nil {
		t.Fatalf("no link in\n%s", *m.Text)
	}
	return match[1]
}

func countAccounts(t *testing.T, w *routetest.World, name string) int {
	t.Helper()
	var n int
	if err := w.DB.QueryRow(context.Background(), `SELECT COUNT(*) FROM users WHERE username = ?`, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestInviteByMail: an account made with an email address and no password
// gets an invite mail. Its link names the account, sets the password once,
// and the account then signs in with it. Until then nobody can sign in,
// the users page lists the invite, and the admin can send a new one, which
// ends the first link.
func TestInviteByMail(t *testing.T) {
	w, box := mailWorld(t, true)
	admin := w.As("admin")

	// Without a username the address gives one
	got := admin.Do("POST", "/api/users/invite", `{"email":"Jannis.G+desk@example.test","role":"operator"}`)
	if got.Status != http.StatusOK || !strings.Contains(got.Body, `"username":"Jannis.Gdesk"`) || !strings.Contains(got.Body, `"invite_sent":true`) {
		t.Fatalf("invite: %d %s", got.Status, got.Body)
	}
	id := createdID(t, got)
	m := box.last(t)
	if strings.Join(m.To, ",") != "Jannis.G+desk@example.test" || m.Subject == "" || strings.Contains(m.Subject, "password_link.") {
		t.Fatalf("mail to %v, subject %q", m.To, m.Subject)
	}
	first := tokenOf(t, m)
	if !strings.Contains(*m.Text, "Jannis.Gdesk") {
		t.Errorf("the mail doesn't name the username:\n%s", *m.Text)
	}

	// Nobody signs in to the invited account yet
	if got := w.SignedOut().Do("POST", "/api/auth/login", `{"username":"Jannis.Gdesk","password":""}`); got.Status == http.StatusOK {
		t.Errorf("sign-in without a password: %d %s", got.Status, got.Body)
	}

	// Listed as invited
	got = admin.Do("GET", "/api/users/invites", "")
	if got.Status != http.StatusOK || !strings.Contains(got.Body, `"user_id":`+id+`,`) || !strings.Contains(got.Body, `"expired":false`) || !strings.Contains(got.Body, `"mail_ready":true`) {
		t.Fatalf("invites: %d %s", got.Status, got.Body)
	}

	// A new invite ends the first link
	expect(t, "resend", admin.Do("POST", "/api/users/"+id+"/invite", "{}"), 200, `{"success":true}`)
	second := tokenOf(t, box.last(t))
	if second == first {
		t.Fatal("the same link again")
	}
	person := w.SignedOut()
	expect(t, "first link", person.Do("POST", "/api/auth/password-link/lookup", `{"token":"`+first+`"}`), 404, "")

	got = person.Do("POST", "/api/auth/password-link/lookup", `{"token":"`+second+`"}`)
	if got.Status != http.StatusOK || !strings.Contains(got.Body, `"username":"Jannis.Gdesk"`) || !strings.Contains(got.Body, `"purpose":"invite"`) {
		t.Fatalf("lookup: %d %s", got.Status, got.Body)
	}
	expect(t, "short password", person.Do("POST", "/api/auth/password-link/redeem", `{"token":"`+second+`","password":"short"}`), 400, "")
	expect(t, "set password", person.Do("POST", "/api/auth/password-link/redeem", `{"token":"`+second+`","password":"Chosen-Pass-1!"}`),
		200, `{"success":true,"data":{"username":"Jannis.Gdesk"}}`)
	expect(t, "used link", person.Do("POST", "/api/auth/password-link/redeem", `{"token":"`+second+`","password":"Other-Pass-1!"}`), 404, "")

	if got := w.SignedOut().Do("POST", "/api/auth/login", `{"username":"Jannis.Gdesk","password":"Chosen-Pass-1!"}`); got.Status != http.StatusOK {
		t.Fatalf("sign in with the chosen password: %d %s", got.Status, got.Body)
	}
	got = admin.Do("GET", "/api/users/invites", "")
	var list struct {
		Data struct {
			Invites []json.RawMessage `json:"invites"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(got.Body), &list); err != nil || len(list.Data.Invites) != 0 {
		t.Errorf("invites after use: %s", got.Body)
	}
	// Signed in: no more invites for the account
	expect(t, "resend after sign-in", admin.Do("POST", "/api/users/"+id+"/invite", "{}"), 409, "")

	rows := strings.Join(w.AuditRows(), "\n")
	for _, want := range []string{"user_invite_sent|Sent an invite to user: Jannis.Gdesk", "user_invite_accepted|Password set from the invite"} {
		if !strings.Contains(rows, want) {
			t.Errorf("no audit row %q in\n%s", want, rows)
		}
	}
}

// An invite needs an email address, no password and mail settings; one
// that fails writes nothing. A taken name gets a number. A mail that
// doesn't go out keeps the account and says so.
func TestInviteRefusals(t *testing.T) {
	w, box := mailWorld(t, false)
	admin := w.As("admin")
	expect(t, "no smtp", admin.Do("POST", "/api/users/invite", `{"email":"a@example.test","username":"amy"}`), 409, "")
	if n := countAccounts(t, w, "amy"); n != 0 || box.count() != 0 {
		t.Fatalf("no smtp: %d accounts, %d mails", n, box.count())
	}
	if got := admin.Do("GET", "/api/users/invites", ""); !strings.Contains(got.Body, `"mail_ready":false`) {
		t.Errorf("invites without smtp: %s", got.Body)
	}
	if _, err := w.DB.Exec(context.Background(), `INSERT INTO settings (key, value) VALUES ('smtp_config', '{"host":"smtp.example.test"}')`); err != nil {
		t.Fatal(err)
	}
	expect(t, "no email", admin.Do("POST", "/api/users/invite", `{"username":"amy"}`), 400, "")
	expect(t, "bad email", admin.Do("POST", "/api/users/invite", `{"email":"not-an-address","username":"amy"}`), 400, "")
	expect(t, "with password", admin.Do("POST", "/api/users/invite", `{"email":"a@example.test","username":"amy","password":"Strong-Pass-1!"}`), 400, "")
	if n := countAccounts(t, w, "amy"); n != 0 {
		t.Fatalf("refused invites made %d accounts", n)
	}

	for _, want := range []string{"amy", "amy-2"} {
		got := admin.Do("POST", "/api/users/invite", `{"email":"amy@example.test"}`)
		if !strings.Contains(got.Body, `"username":"`+want+`"`) {
			t.Errorf("want %s: %d %s", want, got.Status, got.Body)
		}
	}

	box.fail = true
	got := admin.Do("POST", "/api/users/invite", `{"email":"bob@example.test","username":"bob"}`)
	if got.Status != http.StatusOK || !strings.Contains(got.Body, `"invite_sent":false`) || countAccounts(t, w, "bob") != 1 {
		t.Fatalf("mail down: %d %s", got.Status, got.Body)
	}
	expect(t, "resend while mail is down", admin.Do("POST", "/api/users/"+createdID(t, got)+"/invite", "{}"), 502, "")

	// A viewer may not invite
	expect(t, "viewer", w.As("viewer").Do("POST", "/api/users/invite", `{"email":"c@example.test"}`), 403, "")
}
