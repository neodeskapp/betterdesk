package auth_test

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/qr"
)

const (
	secondFactorDue = `{"success":true,"totpRequired":true}`
	noPendingSignIn = `{"success":false,"error":"Session expired. Please log in again."}`
	recoveryCodes   = `["3FA9-0C7E","AAAA-BBBB","1234-5678"]`
)

func TestPasswordStepOfATwoFactorAccount(t *testing.T) {
	eachWorld(t, setup{}, func(t *testing.T, w *world) {
		alice := w.addUser(user{name: "alice", totpEnabled: true, totpSecret: totpSecret, language: "pl"})
		a := w.agent("X-Client-Fingerprint", "fp-of-the-password-step")
		res := a.post("/api/auth/login", login("alice", password))
		want(t, "password step", res, http.StatusOK, secondFactorDue)
		if len(res.cookies()) != 1 || res.cookie("bd.sid") == "" {
			t.Errorf("cookies of the password step: %q", res.cookies())
		}
		// The session waits for the code and is not signed in
		wantStored := regexp.MustCompile(`^\{"cookie":\{[^}]+\},` +
			fmt.Sprintf(`"pendingTotpUserId":%d,"pendingTotpUser":\{"id":%d,"username":"alice","role":"operator","preferred_language":"pl","totpRequired":true,"goPartialToken":null\},`, alice, alice) +
			`"pendingClientMeta":\{"ip":"127\.0\.0\.1","userAgent":"auth-test/1","clientFingerprint":"fp-of-the-password-step"\}\}$`)
		if stored := w.stored(a); !wantStored.MatchString(stored) {
			t.Errorf("stored session:\n%s", stored)
		}
		want(t, "verify while pending", a.get("/api/auth/verify"), http.StatusUnauthorized, notSignedIn)
		if at := w.text(`SELECT CAST(last_login AS TEXT) FROM users WHERE id = ?`, alice); at != "" {
			t.Errorf("the password step noted a sign-in on the account: %q", at)
		}
		if rows := w.audit(); len(rows) != 0 {
			t.Errorf("the password step wrote audit entries: %v", rows)
		}
		equal(t, "attempts", w.attempts(), []string{"alice|127.0.0.1|1"})
	})
}

func TestSecondFactor(t *testing.T) {
	eachWorld(t, setup{}, func(t *testing.T, w *world) {
		alice := w.addUser(user{name: "alice", totpEnabled: true, totpSecret: totpSecret, recoveryCodes: recoveryCodes})
		a := w.agent("X-Client-Fingerprint", "fp-of-the-password-step")

		want(t, "without the password step", a.post("/api/auth/totp/verify", `{"code":"123456"}`), http.StatusBadRequest, noPendingSignIn)
		a.post("/api/auth/login", login("alice", password))
		pending := a.sessionID()

		want(t, "wrong code", a.post("/api/auth/totp/verify", fmt.Sprintf(`{"code":%q}`, wrongCode(t, totpSecret))), http.StatusUnauthorized, invalidCode)
		want(t, "no code", a.post("/api/auth/totp/verify", `{}`), http.StatusUnauthorized, invalidCode)
		want(t, "code with a space", a.post("/api/auth/totp/verify", fmt.Sprintf(`{"code":%q}`, currentCode(t, totpSecret)+" ")), http.StatusUnauthorized, invalidCode)
		want(t, "code as a number", a.post("/api/auth/totp/verify", `{"code":1`+currentCode(t, totpSecret)+`}`), http.StatusUnauthorized, invalidCode)
		want(t, "wrong recovery code", a.post("/api/auth/totp/verify", `{"recoveryCode":"0000-0000"}`), http.StatusUnauthorized, invalidCode)
		// A recovery code that is not text was a fault
		want(t, "recovery code as a number", a.post("/api/auth/totp/verify", `{"recoveryCode":12345678}`), http.StatusInternalServerError, serverErrorBody)
		if a.sessionID() != pending {
			t.Error("a refused code changed the session")
		}

		// The right code, sent from another client than the password
		res := a.post("/api/auth/totp/verify", fmt.Sprintf(`{"code":%q,"return":"/devices"}`, currentCode(t, totpSecret)),
			"User-Agent", "another-agent/2", "X-Client-Fingerprint", "fp-of-the-code-step")
		want(t, "right code", res, http.StatusOK, strings.Replace(signedInAlice, `"redirect":"/"`, `"redirect":"/devices"`, 1))
		if got := res.cookies(); len(got) != 2 || got[0] != clearedGuest || !strings.HasPrefix(got[1], "bd.sid=s%3A") {
			t.Errorf("cookies of the code step: %q", got)
		}
		if a.sessionID() == pending {
			t.Error("the code step kept the session id")
		}
		// The session carries the client of the password step and nothing of the wait
		wantStored := regexp.MustCompile(`^\{"cookie":\{[^}]+\},` +
			fmt.Sprintf(`"userId":%d,"user":\{"id":%d,"username":"alice","role":"operator","preferred_language":null\},`, alice, alice) +
			`"createdAt":\d{13},"lastActivityAt":\d{13},"loginIp":"127\.0\.0\.1","loginUserAgent":"auth-test/1","clientFingerprint":"fp-of-the-password-step"\}$`)
		if stored := w.stored(a); !wantStored.MatchString(stored) {
			t.Errorf("stored session:\n%s", stored)
		}
		if at := w.text(`SELECT CAST(last_login AS TEXT) FROM users WHERE id = ?`, alice); at == "" {
			t.Error("the sign-in is not noted on the account")
		}
		want(t, "a code after sign-in", a.post("/api/auth/totp/verify", `{"code":"123456"}`), http.StatusBadRequest, noPendingSignIn)
		equal(t, "audit", w.audit(), []string{
			fmt.Sprintf("totp_failed|Method: totp|127.0.0.1|%d", alice),
			fmt.Sprintf("totp_failed|Method: totp|127.0.0.1|%d", alice),
			fmt.Sprintf("totp_failed|Method: totp|127.0.0.1|%d", alice),
			fmt.Sprintf("totp_failed|Method: totp|127.0.0.1|%d", alice),
			fmt.Sprintf("totp_failed|Method: recovery|127.0.0.1|%d", alice),
			fmt.Sprintf("login|User logged in (2FA: totp (local)); ip=127.0.0.1; ua=auth-test/1|127.0.0.1|%d", alice),
		})
	})
}

func TestRecoveryCodes(t *testing.T) {
	eachWorld(t, setup{}, func(t *testing.T, w *world) {
		alice := w.addUser(user{name: "alice", totpEnabled: true, totpSecret: totpSecret, recoveryCodes: recoveryCodes})
		first := w.agent()
		first.signInWithRecoveryCode(" aaaa-bbbb ")
		if left := w.text(`SELECT totp_recovery_codes FROM users WHERE id = ?`, alice); left != `["3FA9-0C7E","1234-5678"]` {
			t.Errorf("codes left after one was used: %s", left)
		}
		// A used code is gone; the sign-in from a second browser ends the first session
		second := w.agent()
		second.post("/api/auth/login", login("alice", password))
		want(t, "a used code", second.post("/api/auth/totp/verify", `{"recoveryCode":"AAAA-BBBB"}`), http.StatusUnauthorized, invalidCode)
		// With both, the recovery code is the one that counts
		res := second.post("/api/auth/totp/verify", `{"code":"000000","recoveryCode":"1234-5678"}`)
		if res.status != http.StatusOK || !strings.Contains(res.body, `"replacedPriorSessions":1`) {
			t.Errorf("second sign-in: %d %s", res.status, res.body)
		}
		rows := w.audit()
		equal(t, "audit", rows[len(rows)-2:], []string{
			fmt.Sprintf("login|User logged in (2FA: recovery (local)); ip=127.0.0.1; ua=auth-test/1; replaced=1 prior session(s)|127.0.0.1|%d", alice),
			fmt.Sprintf("session_replaced|2FA login replaced 1 prior session(s); newIp=127.0.0.1|127.0.0.1|%d", alice),
		})
		want(t, "the first browser", first.get("/api/auth/verify"), http.StatusUnauthorized, notSignedIn)
	})
}

// signInWithRecoveryCode signs alice in with her password and a recovery
// code.
func (a *agent) signInWithRecoveryCode(code string) {
	a.w.t.Helper()
	a.post("/api/auth/login", login("alice", password))
	if res := a.post("/api/auth/totp/verify", fmt.Sprintf(`{"recoveryCode":%q}`, code)); res.status != http.StatusOK {
		a.w.t.Fatalf("sign in with the recovery code %q: %d %s", code, res.status, res.body)
	}
}

func TestSecondFactorOfASessionAnOlderConsoleLeft(t *testing.T) {
	// The id of the account is text where the users table has 64-bit ids,
	// and the pending client can be missing
	w := newWorld(t, nil, setup{})
	alice := w.addUser(user{name: "alice", totpEnabled: true, totpSecret: totpSecret, language: "de"})
	a := w.agent("X-Client-Fingerprint", "fp-of-the-code-step")
	a.setSession(fmt.Sprintf(`{"pendingTotpUserId":"%d","pendingTotpUser":{"id":"%d","username":"alice","role":null,"preferred_language":"de","totpRequired":true}}`, alice, alice))
	res := a.post("/api/auth/totp/verify", fmt.Sprintf(`{"code":%q}`, currentCode(t, totpSecret)))
	want(t, "code step", res, http.StatusOK, strings.Replace(signedInAlice, `"role":"operator"`, `"role":null`, 1))
	stored := w.stored(a)
	if !strings.Contains(stored, fmt.Sprintf(`"userId":"%d","user":{"id":"%d","username":"alice","role":null,"preferred_language":"de"},`, alice, alice)) ||
		!strings.Contains(stored, `"clientFingerprint":"fp-of-the-code-step"`) {
		t.Errorf("stored session:\n%s", stored)
	}
	if got := w.int(`SELECT user_id FROM panel_sessions WHERE sid = ?`, a.sessionID()); got != alice {
		t.Errorf("the session row belongs to account %d, want %d", got, alice)
	}
	if res := a.get("/api/auth/totp/status"); res.status != http.StatusOK {
		t.Errorf("the session is not signed in: %d %s", res.status, res.body)
	}

	// Without a pending user, or with an id that counts as none, nothing waits
	for _, fields := range []string{
		`{"pendingTotpUserId":7}`,
		`{"pendingTotpUser":{"id":7}}`,
		`{"pendingTotpUserId":0,"pendingTotpUser":{"id":0}}`,
		`{"pendingTotpUserId":"","pendingTotpUser":{"id":7}}`,
		`{"pendingTotpUserId":7,"pendingTotpUser":null}`,
	} {
		b := w.agent()
		b.setSession(fields)
		want(t, fields, b.post("/api/auth/totp/verify", `{"code":"123456"}`), http.StatusBadRequest, noPendingSignIn)
	}
}

func TestSecondFactorCheckedByTheSignalServer(t *testing.T) {
	w := newWorld(t, nil, setup{})
	alice := w.addUser(user{name: "alice", totpEnabled: true, totpSecret: totpSecret})
	pending := fmt.Sprintf(`{"pendingTotpUserId":%d,"pendingTotpUser":{"id":%d,"username":"alice","role":"operator","preferred_language":null},"goPartialToken":"partial-1"}`, alice, alice)

	// The signal server accepts
	w.api.json("POST /api/auth/login/2fa", 200, `{"token":"jwt"}`)
	a := w.agent()
	a.setSession(pending)
	res := a.post("/api/auth/totp/verify", `{"code":"654321"}`)
	want(t, "accepted", res, http.StatusOK, signedInAlice)
	if calls := w.api.seen("POST /api/auth/login/2fa"); len(calls) != 1 || calls[0].Body != `{"partial_token":"partial-1","code":"654321"}` {
		t.Errorf("call to the signal server: %+v", calls)
	}

	// It refuses
	w.api.json("POST /api/auth/login/2fa", 401, `{"error":"Invalid code"}`)
	b := w.agent()
	b.setSession(pending)
	want(t, "refused", b.post("/api/auth/totp/verify", `{"recoveryCode":"AAAA-BBBB"}`), http.StatusUnauthorized, invalidCode)
	if calls := w.api.seen("POST /api/auth/login/2fa"); calls[len(calls)-1].Body != `{"partial_token":"partial-1","code":"AAAA-BBBB"}` {
		t.Errorf("the recovery code did not go to the signal server: %+v", calls)
	}

	// It has had enough
	w.api.json("POST /api/auth/login/2fa", 429, `{"error":"slow down"}`)
	want(t, "limited", b.post("/api/auth/totp/verify", `{"code":"654321"}`), http.StatusTooManyRequests, `{"success":false,"error":"Too many attempts. Please try again later."}`)

	// It does not answer: the console checks the code itself
	w.api.json("POST /api/auth/login/2fa", 500, `{"error":"down"}`)
	want(t, "no answer, wrong code", b.post("/api/auth/totp/verify", fmt.Sprintf(`{"code":%q}`, wrongCode(t, totpSecret))), http.StatusUnauthorized, invalidCode)
	res = b.post("/api/auth/totp/verify", fmt.Sprintf(`{"code":%q}`, currentCode(t, totpSecret)))
	if res.status != http.StatusOK {
		t.Errorf("no answer, right code: %d %s", res.status, res.body)
	}
	equal(t, "audit", w.audit(), []string{
		fmt.Sprintf("login|User logged in (2FA: totp (Go delegated)); ip=127.0.0.1; ua=auth-test/1|127.0.0.1|%d", alice),
		fmt.Sprintf("totp_failed|Go server rejected TOTP|127.0.0.1|%d", alice),
		fmt.Sprintf("totp_failed|Method: totp|127.0.0.1|%d", alice),
		fmt.Sprintf("login|User logged in (2FA: totp (local)); ip=127.0.0.1; ua=auth-test/1; replaced=1 prior session(s)|127.0.0.1|%d", alice),
		fmt.Sprintf("session_replaced|2FA login replaced 1 prior session(s); newIp=127.0.0.1|127.0.0.1|%d", alice),
	})
}

func TestTwoFactorEnrolment(t *testing.T) {
	eachWorld(t, setup{}, func(t *testing.T, w *world) {
		alice := w.addUser(user{name: "alice"})
		a := w.agent()
		for _, target := range []string{"/api/auth/totp/setup", "/api/auth/totp/enable", "/api/auth/totp/disable"} {
			want(t, target+" signed out", a.post(target, `{}`), http.StatusUnauthorized, notSignedIn)
		}
		want(t, "status signed out", a.get("/api/auth/totp/status"), http.StatusUnauthorized, notSignedIn)
		a.signIn("alice")
		const off, on = `{"success":true,"enabled":false}`, `{"success":true,"enabled":true}`
		want(t, "status before", a.get("/api/auth/totp/status"), http.StatusOK, off)
		want(t, "enable before set-up", a.post("/api/auth/totp/enable", `{"code":"123456"}`), http.StatusBadRequest, `{"success":false,"error":"TOTP not set up"}`)

		res := a.post("/api/auth/totp/setup", `{}`)
		setup := regexp.MustCompile(`^\{"success":true,"qrCode":"(data:image/png;base64,[A-Za-z0-9+/=]+)","secret":"([A-Z2-7]{32})","otpauthUrl":"otpauth://totp/BetterDesk:alice\?secret=([A-Z2-7]{32})&issuer=BetterDesk&digits=6&period=30"\}$`).FindStringSubmatch(res.body)
		if res.status != http.StatusOK || setup == nil || setup[2] != setup[3] {
			t.Fatalf("set-up: %d %s", res.status, res.body)
		}
		secret := setup[2]
		// The picture is the address as a QR code, 256 pixels wide with a margin of 2
		if picture, err := qr.PNGDataURL("otpauth://totp/BetterDesk:alice?secret="+secret+"&issuer=BetterDesk&digits=6&period=30", qr.Options{Width: 256, Margin: 2}); err != nil || picture != setup[1] {
			t.Errorf("the QR code is not the one of the address (%v)", err)
		}
		if stored := w.text(`SELECT totp_secret FROM users WHERE id = ?`, alice); stored != secret {
			t.Error("the secret of the set-up is not the stored one")
		}
		want(t, "status while pending", a.get("/api/auth/totp/status"), http.StatusOK, off)

		for name, body := range map[string]string{
			"no code": `{}`, "empty": `{"code":""}`, "five digits": `{"code":"12345"}`, "seven digits": `{"code":"1234567"}`,
			"a number": `{"code":123456}`, "a list of five": `{"code":[1,2,3,4,5]}`, "a length as text": `{"code":{"length":"6"}}`,
		} {
			want(t, "enable with "+name, a.post("/api/auth/totp/enable", body), http.StatusBadRequest, invalidCode)
		}
		const refused = `{"success":false,"error":"Invalid verification code"}`
		want(t, "wrong code", a.post("/api/auth/totp/enable", fmt.Sprintf(`{"code":%q}`, wrongCode(t, secret))), http.StatusBadRequest, refused)
		// A list of six items has the length of a code and is never one
		want(t, "a list of six", a.post("/api/auth/totp/enable", `{"code":[1,2,3,4,5,6]}`), http.StatusBadRequest, refused)
		want(t, "an object of length six", a.post("/api/auth/totp/enable", `{"code":{"length":6}}`), http.StatusBadRequest, refused)
		want(t, "status after refusals", a.get("/api/auth/totp/status"), http.StatusOK, off)

		res = a.post("/api/auth/totp/enable", fmt.Sprintf(`{"code":%q}`, currentCode(t, secret)))
		if res.status != http.StatusOK || !regexp.MustCompile(`^\{"success":true,"recoveryCodes":\["[0-9A-F]{4}-[0-9A-F]{4}"(,"[0-9A-F]{4}-[0-9A-F]{4}"){7}\]\}$`).MatchString(res.body) {
			t.Fatalf("enable: %d %s", res.status, res.body)
		}
		if stored := w.text(`SELECT totp_recovery_codes FROM users WHERE id = ?`, alice); !strings.Contains(res.body, `"recoveryCodes":`+stored) {
			t.Error("the recovery codes of the answer are not the stored ones")
		}
		want(t, "status enabled", a.get("/api/auth/totp/status"), http.StatusOK, on)
		want(t, "set-up when enabled", a.post("/api/auth/totp/setup", `{}`), http.StatusBadRequest, `{"success":false,"error":"Two-factor authentication is already enabled"}`)
		if stored := w.text(`SELECT totp_secret FROM users WHERE id = ?`, alice); stored != secret {
			t.Error("a refused set-up replaced the secret")
		}

		// Off again, with the password
		want(t, "disable without password", a.post("/api/auth/totp/disable", `{}`), http.StatusBadRequest, `{"success":false,"error":"Password is required"}`)
		want(t, "disable with an empty password", a.post("/api/auth/totp/disable", `{"password":""}`), http.StatusBadRequest, `{"success":false,"error":"Password is required"}`)
		want(t, "disable with a wrong password", a.post("/api/auth/totp/disable", `{"password":"wrong"}`), http.StatusUnauthorized, invalidCredentials)
		want(t, "disable with a number", a.post("/api/auth/totp/disable", `{"password":12345}`), http.StatusInternalServerError, serverErrorBody)
		want(t, "status still enabled", a.get("/api/auth/totp/status"), http.StatusOK, on)
		want(t, "disable", a.post("/api/auth/totp/disable", fmt.Sprintf(`{"password":%q}`, password)), http.StatusOK, `{"success":true}`)
		want(t, "status disabled", a.get("/api/auth/totp/status"), http.StatusOK, off)
		if left := w.text(`SELECT COALESCE(totp_secret, '') || '|' || COALESCE(totp_recovery_codes, '') FROM users WHERE id = ?`, alice); left != "|" {
			t.Errorf("secret and codes after disable: %q", left)
		}
		rows := w.audit()
		equal(t, "audit", rows[1:], []string{
			fmt.Sprintf("totp_enabled|2FA enabled|127.0.0.1|%d", alice),
			fmt.Sprintf("totp_disabled|2FA disabled|127.0.0.1|%d", alice),
		})

		// An account that is gone
		w.exec(`DELETE FROM audit_log`)
		w.exec(`DELETE FROM users WHERE id = ?`, alice)
		want(t, "set-up, account gone", a.post("/api/auth/totp/setup", `{}`), http.StatusBadRequest, `{"success":false,"error":"User not found"}`)
		want(t, "disable, account gone", a.post("/api/auth/totp/disable", `{"password":"x"}`), http.StatusNotFound, `{"success":false,"error":"User not found"}`)
		want(t, "status, account gone", a.get("/api/auth/totp/status"), http.StatusOK, off)
	})
}

func TestTwoFactorRoutesWhenTheAuditLogFails(t *testing.T) {
	w := newWorld(t, nil, setup{})
	alice := w.addUser(user{name: "alice", totpSecret: totpSecret})
	a := w.agent()
	a.signIn("alice")
	w.exec(`DROP TABLE audit_log`)
	want(t, "enable", a.post("/api/auth/totp/enable", fmt.Sprintf(`{"code":%q}`, currentCode(t, totpSecret))), http.StatusInternalServerError, serverErrorBody)
	want(t, "disable", a.post("/api/auth/totp/disable", fmt.Sprintf(`{"password":%q}`, password)), http.StatusInternalServerError, serverErrorBody)
	// Both had done their work before the entry failed
	if left := w.text(`SELECT COALESCE(totp_secret, '') FROM users WHERE id = ?`, alice); left != "" {
		t.Errorf("secret after disable: %q", left)
	}
}
