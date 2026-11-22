package auth_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// A person with two-factor sign-in makes new recovery codes with a current
// code: eight new ones, shown once, and the old ones stop working. A wrong
// code is a failed attempt; without two-factor sign-in there is nothing to
// renew (L111).
func TestNewRecoveryCodesWithACurrentCode(t *testing.T) {
	eachWorld(t, setup{}, func(t *testing.T, w *world) {
		const old = `["3FA9-0C7E"]`
		mia := w.addUser(user{name: "mia", totpSecret: totpSecret})
		a := w.agent()
		a.signIn("mia")
		want(t, "signed out", w.agent().post("/api/auth/totp/recovery-codes", `{"code":"123456"}`), http.StatusUnauthorized, notSignedIn)
		want(t, "without two-factor sign-in", a.post("/api/auth/totp/recovery-codes", `{"code":"123456"}`),
			http.StatusBadRequest, `{"success":false,"error":"TOTP not set up"}`)
		w.exec(`UPDATE users SET totp_enabled = `+w.db.Pick("1", "TRUE")+`, totp_recovery_codes = ? WHERE id = ?`, old, mia)
		w.exec(`DELETE FROM login_attempts`)

		want(t, "no code", a.post("/api/auth/totp/recovery-codes", `{}`), http.StatusBadRequest, `{"success":false,"error":"Invalid verification code. Please try again."}`)
		want(t, "a wrong code", a.post("/api/auth/totp/recovery-codes", fmt.Sprintf(`{"code":%q}`, wrongCode(t, totpSecret))), http.StatusUnauthorized, invalidCode)
		if got := w.attempts(); len(got) != 1 || got[0] != "mia|127.0.0.1|0" {
			t.Errorf("attempts after a wrong code: %v", got)
		}

		code := currentCode(t, totpSecret)
		res := a.post("/api/auth/totp/recovery-codes", fmt.Sprintf(`{"code":%q}`, code))
		var out struct {
			Success       bool     `json:"success"`
			RecoveryCodes []string `json:"recoveryCodes"`
		}
		if err := json.Unmarshal([]byte(res.body), &out); res.status != http.StatusOK || err != nil || !out.Success || len(out.RecoveryCodes) != 8 {
			t.Fatalf("new codes: %d %s", res.status, res.body)
		}
		shape := regexp.MustCompile(`^[0-9A-F]{4}-[0-9A-F]{4}$`)
		for _, c := range out.RecoveryCodes {
			if !shape.MatchString(c) {
				t.Errorf("code %q", c)
			}
		}
		stored := w.text(`SELECT totp_recovery_codes FROM users WHERE id = ?`, mia)
		if stored == old || strings.Contains(stored, out.RecoveryCodes[0]) {
			t.Errorf("stored codes: %s (the old list, or a code as it was shown)", stored)
		}
		want(t, "the same code again", a.post("/api/auth/totp/recovery-codes", fmt.Sprintf(`{"code":%q}`, code)), http.StatusUnauthorized, invalidCode)
		if rows := w.audit(); len(rows) == 0 || rows[len(rows)-1] != fmt.Sprintf("totp_recovery_codes|New recovery codes|127.0.0.1|%d", mia) {
			t.Errorf("audit: %v", rows)
		}

		// The old code no longer signs in, a new one does
		b := w.agent()
		want(t, "password step", b.post("/api/auth/login", login("mia", password)), http.StatusOK, secondFactorDue)
		want(t, "an old code", b.post("/api/auth/totp/verify", `{"recoveryCode":"3FA9-0C7E"}`), http.StatusUnauthorized, invalidCode)
		if res := b.post("/api/auth/totp/verify", fmt.Sprintf(`{"recoveryCode":%q}`, out.RecoveryCodes[3])); res.status != http.StatusOK {
			t.Errorf("a new code: %d %s", res.status, res.body)
		}
	})
}
