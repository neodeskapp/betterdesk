package rustdeskapi_test

import (
	"context"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// With two-factor sign-in mandatory, the client sign-in refuses a local
// account that has none yet, with a clear answer and no token (A153).
func TestClientSignInWhileTheSetUpIsDue(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := routetest.New(t, routetest.Options{DB: db, Env: map[string]string{"RATE_LIMIT_MAX": "1000", "LOGIN_RATE_LIMIT_MAX": "1000"}})
		hash, err := w.Server.Deps().Authn.HashPassword(context.Background(), "A153-Test-1!x")
		if err != nil {
			t.Fatal(err)
		}
		mustExec(t, db, `CREATE TABLE IF NOT EXISTS server_config (key TEXT PRIMARY KEY, value TEXT)`)
		mustExec(t, db, `INSERT INTO server_config (key, value) VALUES ('require_two_factor', 'true')`)
		mustExec(t, db, `UPDATE users SET password_hash = ? WHERE id = 6`, hash)
		got := w.SignedOut().Do("POST", "/api/login", `{"username":"e2e_viewer","password":"A153-Test-1!x","id":"900400003"}`)
		if got.Status != 403 || got.Body != `{"error":"Two-factor sign-in is required for this account. Sign in to the web console once to set it up.","code":"totp_setup_required"}` {
			t.Errorf("client sign-in: %d %s", got.Status, got.Body)
		}
	})
}
