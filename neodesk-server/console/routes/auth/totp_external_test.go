package auth_test

import (
	"fmt"
	"net/http"
	"testing"
)

// A directory or single sign-on account has no password of the console's:
// it turns its second factor off with a current code. The status says
// which the account gives. A wrong code is a failed attempt, and a code
// works once (L112).
func TestExternalAccountsTurnTwoFactorOffWithACode(t *testing.T) {
	for _, provider := range []string{"ldap", "oidc"} {
		t.Run(provider, func(t *testing.T) {
			eachWorld(t, setup{}, func(t *testing.T, w *world) {
				erin := w.addUser(user{name: "erin", totpSecret: totpSecret})
				a := w.agent()
				a.signIn("erin")
				w.exec(`UPDATE users SET auth_provider = ?, totp_enabled = `+w.db.Pick("1", "TRUE")+` WHERE id = ?`, provider, erin)
				w.exec(`DELETE FROM login_attempts`)

				want(t, "status", a.get("/api/auth/totp/status"), http.StatusOK, `{"success":true,"enabled":true,"disable_with":"code"}`)
				want(t, "the password", a.post("/api/auth/totp/disable", fmt.Sprintf(`{"password":%q}`, password)),
					http.StatusBadRequest, `{"success":false,"error":"Please enter a 6-digit code"}`)
				want(t, "a wrong code", a.post("/api/auth/totp/disable", fmt.Sprintf(`{"code":%q}`, wrongCode(t, totpSecret))),
					http.StatusBadRequest, invalidCode)
				if got := w.attempts(); len(got) != 1 || got[0] != "erin|127.0.0.1|0" {
					t.Errorf("attempts after a wrong code: %v", got)
				}
				code := currentCode(t, totpSecret)
				want(t, "the current code", a.post("/api/auth/totp/disable", fmt.Sprintf(`{"code":%q}`, code)), http.StatusOK, `{"success":true}`)
				want(t, "status after", a.get("/api/auth/totp/status"), http.StatusOK, `{"success":true,"enabled":false,"disable_with":"code"}`)
				if left := w.text(`SELECT COALESCE(totp_secret, '') || '|' || COALESCE(totp_recovery_codes, '') FROM users WHERE id = ?`, erin); left != "|" {
					t.Errorf("secret and codes after disable: %q", left)
				}
			})
		})
	}
}

// A local account still turns its second factor off with its password,
// and its status says so (L112).
func TestLocalAccountsTurnTwoFactorOffWithThePassword(t *testing.T) {
	eachWorld(t, setup{}, func(t *testing.T, w *world) {
		lena := w.addUser(user{name: "lena", totpSecret: totpSecret})
		a := w.agent()
		a.signIn("lena")
		w.exec(`UPDATE users SET totp_enabled = `+w.db.Pick("1", "TRUE")+` WHERE id = ?`, lena)
		want(t, "status", a.get("/api/auth/totp/status"), http.StatusOK, `{"success":true,"enabled":true,"disable_with":"password"}`)
		want(t, "a code", a.post("/api/auth/totp/disable", fmt.Sprintf(`{"code":%q}`, currentCode(t, totpSecret))),
			http.StatusBadRequest, `{"success":false,"error":"Password is required"}`)
		want(t, "the password", a.post("/api/auth/totp/disable", fmt.Sprintf(`{"password":%q}`, password)), http.StatusOK, `{"success":true}`)
	})
}

// A wrong code or password in the forms of a signed-in person is a
// refusal of that form, not a lost session: the answer is 400, so the
// console keeps the person signed in and says what was wrong, in their
// language (L344, L419).
func TestWrongCodeOrPasswordKeepsTheSession(t *testing.T) {
	eachWorld(t, setup{}, func(t *testing.T, w *world) {
		nina := w.addUser(user{name: "nina", totpSecret: totpSecret})
		a := w.agent()
		a.signIn("nina")
		w.exec(`UPDATE users SET totp_enabled = `+w.db.Pick("1", "TRUE")+` WHERE id = ?`, nina)
		w.exec(`DELETE FROM login_attempts`)
		want(t, "new codes, a wrong code", a.post("/api/auth/totp/recovery-codes", fmt.Sprintf(`{"code":%q}`, wrongCode(t, totpSecret))),
			http.StatusBadRequest, invalidCode)
		want(t, "turn off, a wrong password", a.post("/api/auth/totp/disable", `{"password":"wrong"}`), http.StatusBadRequest, wrongPassword)
		want(t, "turn off, a wrong password in German", a.post("/api/auth/totp/disable", `{"password":"wrong"}`, "Accept-Language", "de"),
			http.StatusBadRequest, `{"success":false,"error":"Das aktuelle Passwort ist falsch."}`)
		want(t, "still signed in", a.get("/api/auth/totp/status"), http.StatusOK, `{"success":true,"enabled":true,"disable_with":"password"}`)
	})
}
