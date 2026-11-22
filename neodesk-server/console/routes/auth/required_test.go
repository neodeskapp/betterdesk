package auth_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"testing"
)

// requireTwoFactor turns the switch of Settings, Sign-in on, in the
// server's configuration table (which a console-only test database lacks).
func requireTwoFactor(w *world) {
	w.exec(`CREATE TABLE IF NOT EXISTS server_config (key TEXT PRIMARY KEY, value TEXT)`)
	w.exec(`INSERT INTO server_config (key, value) VALUES ('require_two_factor', 'true')`)
}

// With two-factor sign-in mandatory, a local account without it is not
// signed in by its password: it sets the second factor up first, and the
// first code signs it in and shows its recovery codes. Until then it can
// do nothing else. An account that has it signs in as before (A153).
func TestMandatoryTwoFactorSetUpAtSignIn(t *testing.T) {
	eachWorld(t, setup{}, func(t *testing.T, w *world) {
		requireTwoFactor(w)
		sam := w.addUser(user{name: "sam"})
		a := w.agent()
		want(t, "set-up without a password step", a.post("/api/auth/totp/required/setup", `{}`), http.StatusBadRequest, noPendingSignIn)
		want(t, "password step", a.post("/api/auth/login", login("sam", password)), http.StatusOK, `{"success":true,"totpSetupRequired":true}`)
		want(t, "not signed in yet", a.get("/api/auth/verify"), http.StatusUnauthorized, notSignedIn)
		want(t, "nothing else yet", a.get("/api/auth/totp/status"), http.StatusUnauthorized, notSignedIn)

		res := a.post("/api/auth/totp/required/setup", `{}`)
		var setup struct {
			Secret string `json:"secret"`
			QR     string `json:"qrCode"`
		}
		if err := json.Unmarshal([]byte(res.body), &setup); res.status != http.StatusOK || err != nil || setup.Secret == "" || setup.QR == "" {
			t.Fatalf("set-up: %d %s", res.status, res.body)
		}
		want(t, "wrong code", a.post("/api/auth/totp/required/enable", fmt.Sprintf(`{"code":%q}`, wrongCode(t, setup.Secret))), http.StatusUnauthorized, invalidCode)
		want(t, "still not signed in", a.get("/api/auth/verify"), http.StatusUnauthorized, notSignedIn)

		res = a.post("/api/auth/totp/required/enable", fmt.Sprintf(`{"code":%q,"return":"/devices"}`, currentCode(t, setup.Secret)))
		var done struct {
			Success       bool     `json:"success"`
			Redirect      string   `json:"redirect"`
			RecoveryCodes []string `json:"recoveryCodes"`
		}
		if err := json.Unmarshal([]byte(res.body), &done); res.status != http.StatusOK || err != nil || !done.Success || done.Redirect != "/devices" || len(done.RecoveryCodes) != 8 {
			t.Fatalf("first code: %d %s", res.status, res.body)
		}
		if got := a.get("/api/auth/verify"); got.status != http.StatusOK {
			t.Errorf("signed in after the set-up: %d %s", got.status, got.body)
		}
		if on := w.text(`SELECT CASE WHEN `+w.db.Pick("totp_enabled = 1", "totp_enabled")+` THEN 'on' ELSE 'off' END FROM users WHERE id = ?`, sam); on != "on" {
			t.Errorf("two-factor sign-in after the set-up: %s", on)
		}
		rows := w.audit()
		if len(rows) < 2 || !regexp.MustCompile(`^totp_enabled\|`).MatchString(rows[0]) || !regexp.MustCompile(`^login\|User logged in \(2FA set up at sign-in\)`).MatchString(rows[1]) {
			t.Errorf("audit: %v", rows)
		}

		// Next time the code is asked as for any account with a second factor
		want(t, "next password step", w.agent().post("/api/auth/login", login("sam", password)), http.StatusOK, secondFactorDue)
	})
}

// The switch off, or a database without the server's table, changes
// nothing (A153).
func TestTwoFactorNotRequired(t *testing.T) {
	eachWorld(t, setup{}, func(t *testing.T, w *world) {
		w.addUser(user{name: "olaf"})
		w.agent().signIn("olaf")
		requireTwoFactor(w)
		w.exec(`UPDATE server_config SET value = 'false' WHERE key = 'require_two_factor'`)
		w.agent().signIn("olaf")
	})
}
