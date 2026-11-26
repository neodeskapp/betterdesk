package auth_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/mail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/auth"
)

const (
	changeDue = `{"success":true,"passwordChangeRequired":true}`
	setupDue  = `{"success":true,"totpSetupRequired":true}`
)

// mustChange makes an account choose a new password at its next sign-in,
// as an admin does.
func (w *world) mustChange(id int64) {
	w.exec(`INSERT INTO account_security (user_id, must_change_password, two_factor_required) VALUES (?, 1, 0)
		ON CONFLICT (user_id) DO UPDATE SET must_change_password = 1`, id)
}

// The new password an admin asked for: nothing but the change route works
// until it is set, a wrong or missing session gets nothing, and the new
// password finishes the sign-in, ends the other sessions and takes the
// requirement away.
func TestPasswordChangeAtSignIn(t *testing.T) {
	eachWorld(t, setup{}, func(t *testing.T, w *world) {
		pat := w.addUser(user{name: "pat"})
		other := w.agent()
		other.signIn("pat")
		w.mustChange(pat)

		a := w.agent()
		want(t, "without a password step", a.post("/api/auth/password/required", `{"newPassword":"x","confirmPassword":"x"}`), http.StatusBadRequest, noPendingSignIn)
		want(t, "password step", a.post("/api/auth/login", login("pat", password)), http.StatusOK, changeDue)
		want(t, "not signed in yet", a.get("/api/auth/verify"), http.StatusUnauthorized, notSignedIn)
		want(t, "too short", a.post("/api/auth/password/required", `{"newPassword":"short","confirmPassword":"short"}`), http.StatusBadRequest, `{"success":false,"error":"Password must be at least 8 characters"}`)
		res := a.post("/api/auth/password/required", fmt.Sprintf(`{"newPassword":%q,"confirmPassword":%q,"return":"/devices"}`, otherPassword, otherPassword))
		if res.status != http.StatusOK || !strings.Contains(res.body, `"redirect":"/devices"`) {
			t.Fatalf("new password: %d %s", res.status, res.body)
		}
		if got := a.get("/api/auth/verify"); got.status != http.StatusOK {
			t.Errorf("not signed in: %d %s", got.status, got.body)
		}
		if got := other.get("/api/auth/verify"); got.status != http.StatusUnauthorized {
			t.Errorf("the other session goes on: %d", got.status)
		}
		if got := w.int(`SELECT must_change_password FROM account_security WHERE user_id = ?`, pat); got != 0 {
			t.Errorf("still required: %d", got)
		}
		b := w.agent()
		if res := b.post("/api/auth/login", login("pat", otherPassword)); res.status != http.StatusOK || strings.Contains(res.body, "passwordChangeRequired") {
			t.Errorf("next sign-in: %d %s", res.status, res.body)
		}
	})
}

// With a second factor, its code comes before the new password, so a
// stolen password alone can't change it; with two-factor sign-in required
// and not set up, the set-up comes after the new password.
func TestPasswordChangeOrderWithTwoFactor(t *testing.T) {
	eachWorld(t, setup{}, func(t *testing.T, w *world) {
		quin := w.addUser(user{name: "quin", totpEnabled: true, totpSecret: totpSecret})
		w.mustChange(quin)
		a := w.agent()
		want(t, "password step", a.post("/api/auth/login", login("quin", password)), http.StatusOK, secondFactorDue)
		want(t, "no change before the code", a.post("/api/auth/password/required", fmt.Sprintf(`{"newPassword":%q,"confirmPassword":%q}`, otherPassword, otherPassword)), http.StatusBadRequest, noPendingSignIn)
		want(t, "code", a.post("/api/auth/totp/verify", fmt.Sprintf(`{"code":%q}`, currentCode(t, totpSecret))), http.StatusOK, changeDue)
		want(t, "not signed in yet", a.get("/api/auth/verify"), http.StatusUnauthorized, notSignedIn)
		if res := a.post("/api/auth/password/required", fmt.Sprintf(`{"newPassword":%q,"confirmPassword":%q}`, otherPassword, otherPassword)); res.status != http.StatusOK || !strings.Contains(res.body, `"success":true,"user"`) {
			t.Fatalf("new password: %d %s", res.status, res.body)
		}
		if got := a.get("/api/auth/verify"); got.status != http.StatusOK {
			t.Errorf("not signed in: %d %s", got.status, got.body)
		}

		ron := w.addUser(user{name: "ron"})
		w.exec(`INSERT INTO account_security (user_id, must_change_password, two_factor_required) VALUES (?, 1, 1)`, ron)
		b := w.agent()
		want(t, "password step", b.post("/api/auth/login", login("ron", password)), http.StatusOK, changeDue)
		want(t, "new password", b.post("/api/auth/password/required", fmt.Sprintf(`{"newPassword":%q,"confirmPassword":%q}`, otherPassword, otherPassword)), http.StatusOK, setupDue)
		want(t, "not signed in before the set-up", b.get("/api/auth/verify"), http.StatusUnauthorized, notSignedIn)
		if res := b.post("/api/auth/totp/required/setup", `{}`); res.status != http.StatusOK || !strings.Contains(res.body, `"secret"`) {
			t.Errorf("set-up: %d %s", res.status, res.body)
		}
	})
}

// The desktop client's sign-in refuses an account that must choose a new
// password, with its own code.
func TestClientSignInWhileThePasswordChangeIsDue(t *testing.T) {
	eachWorld(t, setup{}, func(t *testing.T, w *world) {
		w.mustChange(w.addUser(user{name: "sid"}))
		res := w.agent().post("/api/login", `{"username":"sid","password":"`+password+`","id":"900400011"}`)
		want(t, "client sign-in", res, http.StatusForbidden,
			`{"error":"This account must choose a new password. Sign in to the web console once to set it.","code":"password_change_required"}`)
	})
}

// nowhere is a mail transport that takes every mail and sends none.
type nowhere struct{}

func (nowhere) Send(context.Context, mail.Settings, mail.Outgoing) error { return nil }
func (nowhere) Verify(context.Context, mail.Settings) error              { return nil }

// Forgot password answers before it looks anything up: at the answer no
// link is made and nothing is written for a known account either, so the
// answer's time doesn't tell known from unknown.
func TestForgotPasswordAnswersFirst(t *testing.T) {
	w := newWorld(t, nil, setup{})
	w.s.Deps().Mail = mail.New(mail.Options{DB: w.db, Transport: nowhere{}})
	w.exec(`INSERT INTO settings (key, value) VALUES ('smtp_config', '{"host":"smtp.example.test","port":25,"from":"desk@example.test"}')`)
	id := w.addUser(user{name: "tom"})
	w.exec(`UPDATE users SET email = 'tom@example.test' WHERE id = ?`, id)
	held := auth.HoldResetWork(t)
	a := w.agent()
	want(t, "known", a.post("/api/auth/password-reset/request", `{"login":"tom"}`), http.StatusOK, `{"success":true}`)
	want(t, "unknown", a.post("/api/auth/password-reset/request", `{"login":"nobody"}`), http.StatusOK, `{"success":true}`)
	if n := w.int(`SELECT COUNT(*) FROM password_links`); n != 0 {
		t.Errorf("%d links at the answer", n)
	}
	if rows := w.audit(); len(rows) != 0 {
		t.Errorf("audit at the answer: %v", rows)
	}
	held()
	if n := w.int(`SELECT COUNT(*) FROM password_links WHERE user_id = ?`, id); n != 1 {
		t.Errorf("%d links after the work", n)
	}
}
