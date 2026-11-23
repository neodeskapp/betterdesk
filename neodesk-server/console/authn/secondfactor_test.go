package authn

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// The API server's answer to a password that is right for an account with
// two-factor sign-in: it names neither role nor provider.
var goAsksSecondFactor = json.RawMessage(`{"status":200,"body":{"requires_2fa":true,"partial_token":"partial"}}`)

// TestLDAPAccountWithTOTPKeepsRoleAndProvider: when the API server asks for
// a second factor, the sign-in writes nothing to the account's row (no
// role, no provider, no hash) and the console asks for its own second
// factor of that row (BUGS.md S3, AN10).
func TestLDAPAccountWithTOTPKeepsRoleAndProvider(t *testing.T) {
	const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	sso := map[string]json.RawMessage{
		"ldap only":     json.RawMessage(`{"status":200,"body":{"ldap_enabled":true,"oidc_enabled":false,"any_enabled":true}}`),
		"ldap and oidc": json.RawMessage(`{"status":200,"body":{"ldap_enabled":true,"oidc_enabled":true,"any_enabled":true}}`),
		"none":          json.RawMessage(`{"status":200,"body":{"ldap_enabled":false,"oidc_enabled":false,"any_enabled":false}}`),
	}
	accounts := []struct {
		name, provider, password string
	}{
		// A directory account: the API server checks every password.
		{"dir", "ldap", "directory password"},
		// A local account whose local check failed: the API server is asked
		// whether it holds a newer hash.
		{"loc", "local", "another password"},
	}
	for ssoName, status := range sso {
		for _, a := range accounts {
			t.Run(a.provider+", single sign-on "+ssoName, func(t *testing.T) {
				db := storetest.Migrated(t)
				ctx := context.Background()
				now := time.UnixMilli(1794287700123)
				s, stub, _ := testService(t, db, func(o *Options) { o.Now = func() time.Time { return now } })
				id := addUser(t, s, a.name, "the stored password", "viewer", a.provider)
				mustExec(t, db, `UPDATE users SET totp_secret = ?, totp_enabled = `+db.Pick("1", "TRUE")+` WHERE id = ?`, secret, id)
				before, _ := getUserByID(ctx, db, id)

				stub.load(&goScript{SSO: status, Login: []json.RawMessage{goAsksSecondFactor}})
				outcome, err := s.Authenticate(ctx, a.name, a.password)
				if err != nil || outcome.User == nil {
					t.Fatalf("sign-in: %+v, %v", outcome, err)
				}
				after, _ := getUserByID(ctx, db, id)
				if after.Role != before.Role || after.AuthProvider != before.AuthProvider || after.PasswordHash != before.PasswordHash {
					t.Errorf("the row changed: role %q to %q, provider %q to %q, hash changed %v",
						before.Role.String, after.Role.String, before.AuthProvider.String, after.AuthProvider.String,
						after.PasswordHash != before.PasswordHash)
				}
				if outcome.User.Role.String != "viewer" || !outcome.User.TOTPRequired {
					t.Errorf("answer: role %q, second factor due %v; want viewer and true", outcome.User.Role.String, outcome.User.TOTPRequired)
				}
				code, _ := totpCode(secret, now)
				if ok, err := s.VerifyTOTPCode(ctx, id, code); err != nil || !ok {
					t.Errorf("the code of the row's own secret: %v, %v", ok, err)
				}
			})
		}
	}
}

// A new account the API server signs in with a second factor gets no role
// from that answer: it starts as viewer, as any new account without one.
func TestNewAccountFromASecondFactorAnswerIsViewer(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		s, stub, _ := testService(t, db, func(o *Options) { o.Settings = Settings{AutoCreate: true} })
		stub.load(&goScript{Login: []json.RawMessage{goAsksSecondFactor}})
		if _, err := s.Authenticate(ctx, "newcomer", "pw"); err != nil {
			t.Fatal(err)
		}
		user, err := getUserByUsername(ctx, db, "newcomer")
		if err != nil || user == nil {
			t.Fatalf("no row for the new account: %v", err)
		}
		if user.Role.String != "viewer" {
			t.Errorf("role of the new row %q, want viewer", user.Role.String)
		}
	})
}
