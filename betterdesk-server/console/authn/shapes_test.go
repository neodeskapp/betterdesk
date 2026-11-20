package authn

import (
	"context"
	"testing"

	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// Turning two-factor sign-in off works in each shape of the users table:
// the API server's takes no NULL secret, so there the secret becomes ""
// (BUGS D5, DECISIONS A65). Both read as no secret.
func TestDisableTOTPOnEitherShape(t *testing.T) {
	storetest.EachOrigin(t, func(t *testing.T, db *store.DB, _ storetest.Origin) {
		ctx := context.Background()
		s, _, _ := testService(t, db, nil)
		id := addUser(t, s, "alice", "pw", "admin", "local")
		mustExec(t, db, `UPDATE users SET totp_secret = 'GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ', totp_enabled = `+db.Pick("1", "TRUE")+`,
			totp_recovery_codes = '["3FA9-0C7E"]' WHERE id = ?`, id)
		if err := s.DisableTOTP(ctx, id); err != nil {
			t.Fatalf("disable: %v", err)
		}
		user, err := getUserByID(ctx, db, id)
		if err != nil || user == nil {
			t.Fatal(err)
		}
		if user.TOTPEnabled.True() || user.TOTPSecret.String != "" || user.TOTPRecoveryCodes.Valid {
			t.Errorf("after disable: enabled %v, secret %q, codes %+v", user.TOTPEnabled.True(), user.TOTPSecret.String, user.TOTPRecoveryCodes)
		}
		if _, err := s.GenerateTOTPSetup(ctx, id); err != nil {
			t.Errorf("a new set-up after disable: %v", err)
		}
	})
}
