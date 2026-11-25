package auth_test

import (
	"context"
	"net/http"
	"sort"
	"testing"
)

// TestOwnPasswordChangeEndsTheOtherSessions: a new password of one's own
// ends the account's other sessions and revokes its access tokens; the
// session that changed it goes on (BUGS.md S9).
func TestOwnPasswordChangeEndsTheOtherSessions(t *testing.T) {
	eachWorld(t, setup{env: map[string]string{"SESSION_MAX_PER_USER": "3"}}, func(t *testing.T, w *world) {
		ctx := context.Background()
		alice := w.addUser(user{name: "alice"})
		bob := w.addUser(user{name: "bob"})
		authn := w.s.Deps().Authn
		token, err := authn.IssueAccessToken(ctx, alice, "client-1", "uuid-1", "198.51.100.1")
		if err != nil {
			t.Fatal(err)
		}
		bobToken, err := authn.IssueAccessToken(ctx, bob, "client-2", "uuid-2", "198.51.100.2")
		if err != nil {
			t.Fatal(err)
		}
		a, other, third, b := w.agent(), w.agent(), w.agent(), w.agent()
		a.signIn("alice")
		other.signIn("alice")
		third.signIn("alice")
		b.signIn("bob")

		want(t, "change", a.post("/api/auth/password", change(password, otherPassword, otherPassword)), http.StatusOK, `{"success":true}`)
		if res := a.get("/api/auth/verify"); res.status != http.StatusOK {
			t.Errorf("the session that changed the password: %d %s", res.status, res.body)
		}
		for name, browser := range map[string]*agent{"second": other, "third": third} {
			want(t, name+" session of the account", browser.get("/api/auth/verify"), http.StatusUnauthorized, signedOut("session_revoked", accountChanged))
		}
		if user, err := authn.ValidateAccessToken(ctx, token); err != nil || user != nil {
			t.Errorf("the account's access token still works: %v, %v", user, err)
		}
		// Another account keeps its session and its token
		if res := b.get("/api/auth/verify"); res.status != http.StatusOK {
			t.Errorf("the session of another account: %d %s", res.status, res.body)
		}
		if user, err := authn.ValidateAccessToken(ctx, bobToken); err != nil || user == nil {
			t.Errorf("the access token of another account: %v, %v", user, err)
		}
		rows := w.sessions(alice)
		sort.Strings(rows)
		equal(t, "session rows of the account", rows, []string{"revoked|0", "revoked|0", "|1"})
	})
}
