package auth_test

import (
	"fmt"
	"net/http"
	"testing"
)

const accountDisabled = `{"success":false,"error":"This account is disabled. Ask an administrator.","code":"account_disabled"}`

// A disabled account learns it is disabled at the password step once its
// password is right, and an account disabled while it waits for its
// second factor gets no session for the code (L107).
func TestDisabledAccountAtTheSignIn(t *testing.T) {
	eachWorld(t, setup{}, func(t *testing.T, w *world) {
		dora := w.addUser(user{name: "dora"})
		w.exec(`UPDATE users SET disabled = `+w.db.Pick("1", "TRUE")+` WHERE id = ?`, dora)
		want(t, "right password", w.agent().post("/api/auth/login", login("dora", password)), http.StatusForbidden, accountDisabled)
		want(t, "wrong password", w.agent().post("/api/auth/login", login("dora", "wrong")), http.StatusUnauthorized, invalidCredentials)

		tina := w.addUser(user{name: "tina", totpEnabled: true, totpSecret: totpSecret})
		a := w.agent()
		want(t, "password step", a.post("/api/auth/login", login("tina", password)), http.StatusOK, secondFactorDue)
		w.exec(`UPDATE users SET disabled = `+w.db.Pick("1", "TRUE")+` WHERE id = ?`, tina)
		want(t, "code of a disabled account", a.post("/api/auth/totp/verify", fmt.Sprintf(`{"code":%q}`, currentCode(t, totpSecret))),
			http.StatusForbidden, accountDisabled)
		want(t, "verify afterwards", a.get("/api/auth/verify"), http.StatusUnauthorized, notSignedIn)
		want(t, "a second code", a.post("/api/auth/totp/verify", `{"code":"123456"}`), http.StatusBadRequest, noPendingSignIn)
	})
}
