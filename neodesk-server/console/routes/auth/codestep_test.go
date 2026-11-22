package auth_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// waitingSince is the session field that says when the password step of a
// waiting sign-in was passed.
func waitingSince(at time.Time) string {
	return fmt.Sprintf(`"pendingTotpAt":%d`, at.UnixMilli())
}

// TestWrongCodesEndThePendingSignIn: five wrong codes end the wait for the
// second factor, so the right code needs the password again (BUGS.md AR4).
func TestWrongCodesEndThePendingSignIn(t *testing.T) {
	eachWorld(t, setup{}, func(t *testing.T, w *world) {
		alice := w.addUser(user{name: "alice", totpEnabled: true, totpSecret: totpSecret})
		a := w.agent()
		want(t, "password step", a.post("/api/auth/login", login("alice", password)), http.StatusOK, secondFactorDue)
		for i := 1; i <= 5; i++ {
			want(t, fmt.Sprintf("wrong code %d", i), a.post("/api/auth/totp/verify", fmt.Sprintf(`{"code":%q}`, wrongCode(t, totpSecret))), http.StatusUnauthorized, invalidCode)
		}
		want(t, "the right code after five wrong ones", a.post("/api/auth/totp/verify", fmt.Sprintf(`{"code":%q}`, currentCode(t, totpSecret))), http.StatusBadRequest, noPendingSignIn)
		if stored := w.stored(a); strings.Contains(stored, "pendingTotp") {
			t.Errorf("the session still waits: %s", stored)
		}
		// The password starts a new wait with its own five codes
		a.post("/api/auth/login", login("alice", password))
		for i := 1; i <= 4; i++ {
			want(t, fmt.Sprintf("wrong code %d of the new wait", i), a.post("/api/auth/totp/verify", `{"code":"abcdef"}`), http.StatusUnauthorized, invalidCode)
		}
		res := a.post("/api/auth/totp/verify", fmt.Sprintf(`{"code":%q}`, currentCode(t, totpSecret)))
		if res.status != http.StatusOK || !strings.Contains(res.body, `"success":true`) {
			t.Errorf("the right code of the new wait: %d %s", res.status, res.body)
		}
		failed := 0
		for _, row := range w.audit() {
			if row == fmt.Sprintf("totp_failed|Method: totp|127.0.0.1|%d", alice) {
				failed++
			}
		}
		if failed != 9 {
			t.Errorf("%d totp_failed entries, want 9", failed)
		}
	})
}

// TestPendingSecondFactorExpires: the code must come within five minutes
// of the password step; a wait without that time, as an older console
// left one, is over too (BUGS.md AR5).
func TestPendingSecondFactorExpires(t *testing.T) {
	w := newWorld(t, nil, setup{})
	alice := w.addUser(user{name: "alice", totpEnabled: true, totpSecret: totpSecret})
	waiting := func(fields string) string {
		return fmt.Sprintf(`{"pendingTotpUserId":%d,"pendingTotpUser":{"id":%d,"username":"alice","role":"operator","preferred_language":null,"totpRequired":true}%s}`, alice, alice, fields)
	}
	for _, c := range []struct {
		what, fields string
		status       int
	}{
		{"six minutes after the password step", "," + waitingSince(time.Now().Add(-6*time.Minute)), http.StatusBadRequest},
		{"without the time of the password step", "", http.StatusBadRequest},
		{"four minutes after the password step", "," + waitingSince(time.Now().Add(-4*time.Minute)), http.StatusOK},
	} {
		a := w.agent()
		a.setSession(waiting(c.fields))
		res := a.post("/api/auth/totp/verify", fmt.Sprintf(`{"code":%q}`, currentCode(t, totpSecret)))
		if res.status != c.status {
			t.Errorf("%s: %d %s, want %d", c.what, res.status, res.body, c.status)
		}
		if c.status == http.StatusBadRequest {
			want(t, c.what, res, http.StatusBadRequest, noPendingSignIn)
			if stored := w.stored(a); strings.Contains(stored, "pendingTotp") {
				t.Errorf("%s: the session still waits: %s", c.what, stored)
			}
		}
	}

	// A sign-in through the password step waits five minutes
	a := w.agent()
	a.post("/api/auth/login", login("alice", password))
	a.setSession(`{` + waitingSince(time.Now().Add(-5*time.Minute-time.Second)) + `}`)
	want(t, "a password step over five minutes ago", a.post("/api/auth/totp/verify", fmt.Sprintf(`{"code":%q}`, currentCode(t, totpSecret))), http.StatusBadRequest, noPendingSignIn)
}
