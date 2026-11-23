package auth_test

import (
	"net/http"
	"testing"
)

// proOnly is the answer to an account for the desktop client only.
const proOnly = `{"success":false,"error":"This account is for RustDesk desktop client only. Please use the RustDesk app to log in."}`

// TestLoginRefusesProWith403: the right password of a pro account gets the
// 403 that says what the account is for, counts as a good attempt, and
// starts no session; a wrong one gets the answer of any wrong password
// (BUGS.md S11).
func TestLoginRefusesProWith403(t *testing.T) {
	eachWorld(t, setup{}, func(t *testing.T, w *world) {
		pia := w.addUser(user{name: "pia", role: "pro"})
		a := w.agent()
		res := a.post("/api/auth/login", login("pia", password))
		want(t, "right password", res, http.StatusForbidden, proOnly)
		if res.cookie("nd.sid") != "" {
			t.Errorf("the refusal set a session cookie: %v", res.cookies())
		}
		want(t, "wrong password", a.post("/api/auth/login", login("pia", "not-the-password")), http.StatusUnauthorized, invalidCredentials)

		equal(t, "attempts", w.attempts(), []string{"pia|127.0.0.1|1", "pia|127.0.0.1|0"})
		equal(t, "audit", w.audit(), []string{"login_failed|Username: p***a; ip=127.0.0.1; ua=auth-test/1|127.0.0.1|0"})
		if n := w.int(`SELECT COUNT(*) FROM panel_sessions WHERE user_id = ?`, pia); n != 0 {
			t.Errorf("%d sessions of the pro account", n)
		}
		if at := w.text(`SELECT CAST(last_login AS TEXT) FROM users WHERE id = ?`, pia); at != "" {
			t.Errorf("the refusal noted a sign-in on the account: %q", at)
		}
	})
}

// TestOIDCSessionRefusesPro: single sign-on gives a pro account no console
// session either, whether the role is the stored one or the signal
// server's (BUGS.md S11).
func TestOIDCSessionRefusesPro(t *testing.T) {
	w := newWorld(t, nil, setup{})
	desk := w.addUser(user{name: "desk", role: "pro", provider: "oidc"})
	w.addUser(user{name: "ops", role: "operator", provider: "oidc"})
	for _, c := range []struct{ what, user string }{
		{"a stored pro account", `"token":"t1","username":"desk"`},
		{"the role pro from the signal server", `"token":"t2","username":"ops","role":"pro"`},
		{"a new account with the role pro", `"token":"t3","username":"fresh","role":"pro"`},
	} {
		w.provider(c.user)
		a := w.agent()
		res := singleSignOn(t, a, "/devices")
		want(t, c.what, res, http.StatusForbidden, proOnly)
		if res.cookie("nd.sid") != "" {
			t.Errorf("%s: the refusal set a session cookie: %v", c.what, res.cookies())
		}
	}
	if n := w.int(`SELECT COUNT(*) FROM panel_sessions WHERE user_id IS NOT NULL`); n != 0 {
		t.Errorf("%d signed-in sessions after the refusals", n)
	}
	if rows := w.audit(); len(rows) != 0 {
		t.Errorf("audit entries of refused sign-ons: %v", rows)
	}
	if at := w.text(`SELECT CAST(last_login AS TEXT) FROM users WHERE id = ?`, desk); at != "" {
		t.Errorf("the refusal noted a sign-in on the account: %q", at)
	}
}
