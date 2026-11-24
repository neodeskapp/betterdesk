package ndapi_test

import (
	"context"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// The desktop client's operator sign-in tells a disabled account that it
// is disabled once its password is right, and gives it no token (L107).
func TestOperatorLoginOfADisabledAccount(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := routetest.New(t, routetest.Options{DB: db, Env: map[string]string{"RATE_LIMIT_MAX": "1000", "LOGIN_RATE_LIMIT_MAX": "1000"}})
		ctx := context.Background()
		hash, err := w.Server.Deps().Authn.HashPassword(ctx, "L107-Op-1!x")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `UPDATE users SET password_hash = ?, disabled = `+db.Pick("1", "TRUE")+` WHERE id = 5`, hash); err != nil {
			t.Fatal(err)
		}
		got := w.SignedOut().Do("POST", "/api/nd/operator/login", `{"username":"e2e_operator","password":"L107-Op-1!x","device_id":"900100001"}`)
		if got.Status != 403 || got.Body != `{"error":"This account is disabled","code":"account_disabled"}` {
			t.Errorf("operator sign-in of a disabled account: %d %s", got.Status, got.Body)
		}
		var tokens int
		if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM access_tokens WHERE user_id = 5`).Scan(&tokens); err != nil || tokens != 0 {
			t.Errorf("tokens of the disabled account: %d, %v", tokens, err)
		}
	})
}
