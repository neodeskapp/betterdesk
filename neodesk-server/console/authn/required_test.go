package authn

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// With two-factor sign-in mandatory, a local account without it has the
// set-up due at its sign-in; one that has it, and a directory account,
// do not (the directory is left to its provider). The sign-in is noted
// on the row only once the set-up is done (A153).
func TestTwoFactorSetUpDue(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		s, stub, _ := testService(t, db, nil)
		mustExec(t, db, `CREATE TABLE IF NOT EXISTS server_config (key TEXT PRIMARY KEY, value TEXT)`)
		if required, err := s.TwoFactorRequired(ctx); err != nil || required {
			t.Fatalf("switch not set: %v, %v", required, err)
		}
		mustExec(t, db, `INSERT INTO server_config (key, value) VALUES ('require_two_factor', 'true')`)

		local := addUser(t, s, "sam", "pw", "operator", "local")
		outcome, err := s.Authenticate(ctx, "sam", "pw")
		if err != nil || outcome.User == nil || !outcome.User.TOTPSetupRequired || outcome.User.TOTPRequired {
			t.Fatalf("local account without two-factor sign-in: %+v, %v", outcome.User, err)
		}
		var lastLogin store.Time
		_ = store.SelectRow(ctx, db, `SELECT last_login FROM users WHERE id = ?`, local).Scan(&lastLogin)
		if !lastLogin.IsNull() {
			t.Error("the sign-in was noted before the set-up")
		}

		mustExec(t, db, `UPDATE users SET totp_enabled = `+db.Pick("1", "TRUE")+`, totp_secret = 'GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ' WHERE id = ?`, local)
		if outcome, err := s.Authenticate(ctx, "sam", "pw"); err != nil || outcome.User == nil || outcome.User.TOTPSetupRequired || !outcome.User.TOTPRequired {
			t.Errorf("local account with two-factor sign-in: %+v, %v", outcome.User, err)
		}

		addUser(t, s, "dirk", "unused", "viewer", "ldap")
		stub.load(&goScript{SSO: json.RawMessage(ldapOn), Login: []json.RawMessage{json.RawMessage(ldapAccepted)}})
		if outcome, err := s.Authenticate(ctx, "dirk", "pw"); err != nil || outcome.User == nil || outcome.User.TOTPSetupRequired {
			t.Errorf("directory account: %+v, %v", outcome.User, err)
		}

		mustExec(t, db, `UPDATE server_config SET value = 'false' WHERE key = 'require_two_factor'`)
		addUser(t, s, "olga", "pw", "operator", "local")
		if outcome, err := s.Authenticate(ctx, "olga", "pw"); err != nil || outcome.User == nil || outcome.User.TOTPSetupRequired {
			t.Errorf("switch off: %+v, %v", outcome.User, err)
		}
	})
}
