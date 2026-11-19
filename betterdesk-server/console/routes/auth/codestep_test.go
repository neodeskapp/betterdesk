package auth_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

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
