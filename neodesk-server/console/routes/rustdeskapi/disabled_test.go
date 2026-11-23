package rustdeskapi_test

import (
	"context"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// The client sign-in tells a disabled account that it is disabled once its
// password is right; a wrong password gets the plain refusal (L107).
func TestClientSignInOfADisabledAccount(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := routetest.New(t, routetest.Options{DB: db, Env: map[string]string{"RATE_LIMIT_MAX": "1000", "LOGIN_RATE_LIMIT_MAX": "1000"}})
		hash, err := w.Server.Deps().Authn.HashPassword(context.Background(), "L107-Test-1!x")
		if err != nil {
			t.Fatal(err)
		}
		mustExec(t, db, `UPDATE users SET password_hash = ?, disabled = `+db.Pick("1", "TRUE")+` WHERE id = 6`, hash)
		got := w.SignedOut().Do("POST", "/api/login", `{"username":"e2e_viewer","password":"L107-Test-1!x","id":"900400002"}`)
		if got.Status != 403 || got.Body != `{"error":"This account is disabled","code":"account_disabled"}` {
			t.Errorf("the right password: %d %s", got.Status, got.Body)
		}
		got = w.SignedOut().Do("POST", "/api/login", `{"username":"e2e_viewer","password":"wrong","id":"900400002"}`)
		if got.Status != 401 || got.Body != `{"error":"Invalid credentials"}` {
			t.Errorf("a wrong password: %d %s", got.Status, got.Body)
		}
	})
}
