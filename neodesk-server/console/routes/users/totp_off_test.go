package users_test

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// TestTurnOffAnotherPersonsTwoFactor: an account with user.edit turns off
// the two-factor sign-in of an account it may change: the secret and the
// recovery codes go, the person's sessions end and their access tokens
// are revoked, and the audit log says who did it. Not for the caller's own
// account (that is the profile's, with a code) and not for an account the
// caller may not change; only false is taken (L111).
func TestTurnOffAnotherPersonsTwoFactor(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		w := world(t, storetest.Prepare(t, db, storetest.GoServerFirst), routetest.Options{Env: map[string]string{"LOGIN_RATE_LIMIT_MAX": "1000"}})
		ctx := context.Background()
		if db.IsPostgres() {
			if _, err := db.Exec(ctx, `SELECT setval(pg_get_serial_sequence('users', 'id'), (SELECT MAX(id) FROM users))`); err != nil {
				t.Fatal(err)
			}
		}
		admin := w.As("admin")
		id := createdID(t, admin.Do("POST", "/api/users", `{"username":"tina","password":"First-Pass-1!","role":"operator"}`, "X-Forwarded-For", "10.4.0.1"))
		tinaID, _ := strconv.ParseInt(id, 10, 64)
		tina := w.SignedOut()
		if got := tina.Do("POST", "/api/auth/login", `{"username":"tina","password":"First-Pass-1!"}`); got.Status != http.StatusOK {
			t.Fatalf("sign in: %d %s", got.Status, got.Body)
		}
		if _, err := db.Exec(ctx, `UPDATE users SET totp_enabled = `+db.Pick("1", "TRUE")+`, totp_secret = 'GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ', totp_recovery_codes = '["AAAA-BBBB"]' WHERE id = ?`, tinaID); err != nil {
			t.Fatal(err)
		}
		token, err := w.Server.Deps().Authn.IssueAccessToken(ctx, tinaID, "client-1", "uuid-1", "10.4.0.9")
		if err != nil {
			t.Fatal(err)
		}

		expect(t, "turn it on", admin.Do("PATCH", "/api/users/"+id, `{"totp_enabled":true}`),
			400, `{"success":false,"error":"Invalid value for totp_enabled"}`)
		expect(t, "global_admin on an admin", w.As("global_admin").Do("PATCH", "/api/users/"+strconv.FormatInt(routetest.RoleIDs["admin"], 10), `{"totp_enabled":false}`),
			403, `{"success":false,"error":"Cannot assign a role higher than your own"}`)
		expect(t, "own account", admin.Do("PATCH", "/api/users/"+strconv.FormatInt(routetest.RoleIDs["admin"], 10), `{"totp_enabled":false}`),
			400, `{"success":false,"error":"Turn off your own two-factor sign-in in Settings, Sign-in"}`)

		got := admin.Do("PATCH", "/api/users/"+id, `{"totp_enabled":false}`)
		if got.Status != http.StatusOK || !strings.Contains(got.Body, `"totp_enabled":false`) {
			t.Fatalf("turn off: %d %s", got.Status, got.Body)
		}
		var left string
		if err := db.QueryRow(ctx, `SELECT COALESCE(totp_secret, '') || '|' || COALESCE(totp_recovery_codes, '') FROM users WHERE id = ?`, tinaID).Scan(&left); err != nil || left != "|" {
			t.Errorf("secret and codes afterwards: %q, %v", left, err)
		}
		if got := tina.Do("GET", "/api/auth/verify", ""); got.Status != http.StatusUnauthorized {
			t.Errorf("the person's session goes on: %d %s", got.Status, got.Body)
		}
		if user, err := w.Server.Deps().Authn.ValidateAccessToken(ctx, token); err != nil || user != nil {
			t.Errorf("the person's access token still works: %+v, %v", user, err)
		}
		var details string
		if err := db.QueryRow(ctx, `SELECT details FROM audit_log WHERE action = 'totp_disabled_by_admin'`).Scan(&details); err != nil || details != "Turned off two-factor sign-in for user: tina" {
			t.Errorf("audit: %q, %v", details, err)
		}
	})
}
