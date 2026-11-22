package bdapi_test

import (
	"context"
	"testing"

	"github.com/unitronix/betterdesk-server/console/routes/internal/routetest"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// With two-factor sign-in mandatory, the operator sign-in refuses a local
// account that has none yet, with a clear answer and no token: the
// desktop client has no step for the set-up (A153).
func TestOperatorLoginWhileTheSetUpIsDue(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := routetest.New(t, routetest.Options{DB: db, Env: map[string]string{"RATE_LIMIT_MAX": "1000", "LOGIN_RATE_LIMIT_MAX": "1000"}})
		ctx := context.Background()
		hash, err := w.Server.Deps().Authn.HashPassword(ctx, "A153-Op-1!x")
		if err != nil {
			t.Fatal(err)
		}
		for _, statement := range []string{
			`CREATE TABLE IF NOT EXISTS server_config (key TEXT PRIMARY KEY, value TEXT)`,
			`INSERT INTO server_config (key, value) VALUES ('require_two_factor', 'true')`,
		} {
			if _, err := db.Exec(ctx, statement); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := db.Exec(ctx, `UPDATE users SET password_hash = ? WHERE id = 5`, hash); err != nil {
			t.Fatal(err)
		}
		got := w.SignedOut().Do("POST", "/api/bd/operator/login", `{"username":"e2e_operator","password":"A153-Op-1!x","device_id":"900100001"}`)
		if got.Status != 403 || got.Body != `{"error":"Two-factor sign-in is required for this account. Sign in to the web console once to set it up.","code":"totp_setup_required"}` {
			t.Errorf("operator sign-in: %d %s", got.Status, got.Body)
		}
		var tokens int
		if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM access_tokens WHERE user_id = 5`).Scan(&tokens); err != nil || tokens != 0 {
			t.Errorf("tokens: %d, %v", tokens, err)
		}
	})
}
