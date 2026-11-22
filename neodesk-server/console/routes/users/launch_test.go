package users_test

import (
	"context"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/routes/internal/routetest"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// The rows of the launch (BUGS L rows) that the account routes fixed.

// TestGuestIssuerIsGivenByWhoGivesViewer: the roles that may give viewer
// (the super admin roles and global_admin) create guest_issuer accounts
// and make an account one; the others are refused as for any role they
// may not give (BUGS L108).
func TestGuestIssuerIsGivenByWhoGivesViewer(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		w := world(t, storetest.Prepare(t, db, storetest.GoServerFirst), routetest.Options{})
		ctx := context.Background()
		if db.IsPostgres() {
			// The test accounts were inserted with their ids
			if _, err := db.Exec(ctx, `SELECT setval(pg_get_serial_sequence('users', 'id'), (SELECT MAX(id) FROM users))`); err != nil {
				t.Fatal(err)
			}
		}
		role := func(username string) string {
			t.Helper()
			var r string
			if err := db.QueryRow(ctx, `SELECT role FROM users WHERE username = ?`, username).Scan(&r); err != nil {
				t.Fatalf("role of %s: %v", username, err)
			}
			return r
		}
		for i, by := range []string{"global_admin", "super_admin", "admin"} {
			name := "issuer_" + by
			got := w.As(by).Do("POST", "/api/users", `{"username":"`+name+`","password":"Strong-Pass-1!","role":"guest_issuer"}`, "X-Forwarded-For", "10.9.0."+string(rune('1'+i)))
			if got.Status != 200 || !strings.Contains(got.Body, `"role":"guest_issuer"`) {
				t.Fatalf("%s creates a guest_issuer: %d %s", by, got.Status, got.Body)
			}
			if stored := role(name); stored != "guest_issuer" {
				t.Errorf("%s created a guest_issuer stored as %s", by, stored)
			}
		}
		// operator (5) becomes guest_issuer and back
		global := w.As("global_admin")
		expect(t, "operator to guest_issuer", global.Do("PATCH", "/api/users/5", `{"role":"guest_issuer"}`), 200, "")
		if got := role("e2e_operator"); got != "guest_issuer" {
			t.Errorf("e2e_operator is %s", got)
		}
		expect(t, "guest_issuer to viewer", global.Do("PATCH", "/api/users/5", `{"role":"viewer"}`), 200, "")
		expect(t, "server_admin gives guest_issuer", w.As("server_admin").Do("PATCH", "/api/users/6", `{"role":"guest_issuer"}`), 403, "")
		if got := role("e2e_viewer"); got != "viewer" {
			t.Errorf("e2e_viewer is %s after the refused change", got)
		}
	})
}
