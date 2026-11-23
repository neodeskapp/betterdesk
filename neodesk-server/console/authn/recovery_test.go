package authn

import (
	"context"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// TestRecoveryCodeWithAStoredListOfOtherValues: a stored list of recovery
// codes that is JSON but not a list of texts holds no codes, so a code is
// wrong for it instead of failing (BUGS.md AN7).
func TestRecoveryCodeWithAStoredListOfOtherValues(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		s, _, _ := testService(t, db, nil)
		id := addUser(t, s, "alice", "pw", "admin", "local")
		for _, stored := range []string{`{}`, `null`, `[1]`, `["AAAA-BBBB",1]`, `[1,"AAAA-BBBB"]`, `"AAAA-BBBB"`, `[["AAAA-BBBB"]]`, `[null]`, `["AAAA-BBBB",null]`} {
			mustExec(t, db, `UPDATE users SET totp_secret = 'GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ', totp_enabled = `+db.Pick("1", "TRUE")+`,
				totp_recovery_codes = ? WHERE id = ?`, stored, id)
			for _, code := range []string{"AAAA-BBBB", "CCCC-DDDD", ""} {
				if ok, err := s.VerifyRecoveryCode(ctx, id, code); err != nil || ok {
					t.Errorf("stored %s, code %s: %v, %v; want a wrong code", stored, code, ok, err)
				}
			}
			var left string
			if err := db.QueryRow(ctx, `SELECT totp_recovery_codes FROM users WHERE id = ?`, id).Scan(&left); err != nil || left != stored {
				t.Errorf("stored %s became %s (%v)", stored, left, err)
			}
		}
	})
}
