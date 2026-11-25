package auth_test

import (
	"fmt"
	"net/http"
	"sort"
	"testing"
)

// TestOwnTwoFactorChangeEndsTheOtherSessions: turning one's own two-factor
// sign-in on or off ends the account's other sessions, as a new password
// does; the browser that changed it stays signed in, and the others are
// told why at their next request.
func TestOwnTwoFactorChangeEndsTheOtherSessions(t *testing.T) {
	eachWorld(t, setup{}, func(t *testing.T, w *world) {
		alice := w.addUser(user{name: "alice", totpSecret: totpSecret})
		a, b, c := w.agent(), w.agent(), w.agent()
		a.signIn("alice")
		b.signIn("alice")
		if res := a.post("/api/auth/totp/enable", fmt.Sprintf(`{"code":%q}`, currentCode(t, totpSecret))); res.status != http.StatusOK {
			t.Fatalf("turn it on: %d %s", res.status, res.body)
		}
		want(t, "the other browser after turning it on", b.get("/api/auth/verify"), http.StatusUnauthorized, signedOut("session_revoked", accountChanged))
		if res := a.get("/api/auth/verify"); res.status != http.StatusOK {
			t.Errorf("the browser that turned it on: %d %s", res.status, res.body)
		}
		// A browser signed in with the second factor (its session is made
		// here, the code step is tested elsewhere); turning it off ends it
		w.exec(`UPDATE users SET totp_enabled = `+w.db.Pick("0", "FALSE")+` WHERE id = ?`, alice)
		c.signIn("alice")
		w.exec(`UPDATE users SET totp_enabled = `+w.db.Pick("1", "TRUE")+` WHERE id = ?`, alice)
		want(t, "turn it off", a.post("/api/auth/totp/disable", fmt.Sprintf(`{"password":%q}`, password)), http.StatusOK, `{"success":true}`)
		want(t, "the other browser after turning it off", c.get("/api/auth/verify"), http.StatusUnauthorized, signedOut("session_revoked", accountChanged))
		if res := a.get("/api/auth/verify"); res.status != http.StatusOK {
			t.Errorf("the browser that turned it off: %d %s", res.status, res.body)
		}
		rows := w.sessions(alice)
		sort.Strings(rows)
		equal(t, "session rows", rows, []string{"revoked|0", "revoked|0", "|1"})
	})
}
