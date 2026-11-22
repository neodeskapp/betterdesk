package authn

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/auth"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// A code of the second factor signs an account in once: the same code, or
// one of an earlier step, is refused afterwards; the next step's code and
// another account's code of the same step work (RV122).
func TestTOTPCodeWorksOnce(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		now := time.UnixMilli(1794287700123)
		s, _, _ := testService(t, db, func(o *Options) { o.Now = func() time.Time { return now } })
		const secret = "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"
		alice := addUser(t, s, "alice", "pw", "admin", "local")
		bob := addUser(t, s, "bob", "pw", "admin", "local")
		mustExec(t, db, `UPDATE users SET totp_secret = ?, totp_enabled = `+db.Pick("1", "TRUE"), secret)
		code := func() string {
			t.Helper()
			c, err := auth.ComputeTOTP(secret, now)
			if err != nil {
				t.Fatal(err)
			}
			return c
		}
		verify := func(id int64, c string) bool {
			t.Helper()
			ok, err := s.VerifyTOTPCode(ctx, id, c)
			if err != nil {
				t.Fatal(err)
			}
			return ok
		}
		first := code()
		if !verify(alice, first) {
			t.Fatal("the first use of the code was refused")
		}
		if verify(alice, first) {
			t.Error("the same code worked twice")
		}
		if !verify(bob, first) {
			t.Error("another account's code of the same step was refused")
		}
		now = now.Add(30 * time.Second)
		if !verify(alice, code()) {
			t.Error("the next step's code was refused")
		}
		now = now.Add(-30 * time.Second)
		if verify(alice, first) {
			t.Error("the code of an earlier step worked after a later one")
		}
	})
}

// The recovery codes are written only over the list they were read from:
// of two requests with the same code, the one that comes second finds the
// list changed and uses nothing (RV122).
func TestRecoveryCodeIsWrittenWithACondition(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		s, _, _ := testService(t, db, nil)
		id := addUser(t, s, "alice", "pw", "admin", "local")
		mustExec(t, db, `UPDATE users SET totp_recovery_codes = ? WHERE id = ?`, `["a","b"]`, id)
		if ok, err := useRecoveryCode(ctx, db, id, `["a","b","c"]`, `["a"]`); err != nil || ok {
			t.Errorf("a list that changed since it was read: %v, %v", ok, err)
		}
		if row, _ := getUserByID(ctx, db, id); row.TOTPRecoveryCodes.String != `["a","b"]` {
			t.Errorf("the list after a refused write: %s", row.TOTPRecoveryCodes.String)
		}
		if ok, err := useRecoveryCode(ctx, db, id, `["a","b"]`, `["a"]`); err != nil || !ok {
			t.Errorf("the list as read: %v, %v", ok, err)
		}

		// Many requests with one code at the same time: one uses it
		codes := []string{"ABCD-EF12", "1234-5678"}
		hashes, err := auth.HashRecoveryCodesCost(codes, testRecoveryCost)
		if err != nil {
			t.Fatal(err)
		}
		mustExec(t, db, `UPDATE users SET totp_recovery_codes = ?, totp_enabled = `+db.Pick("1", "TRUE")+` WHERE id = ?`, hashes, id)
		var wg sync.WaitGroup
		var mu sync.Mutex
		used := 0
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ok, err := s.VerifyRecoveryCode(ctx, id, codes[0])
				if err != nil {
					t.Error(err)
				}
				if ok {
					mu.Lock()
					used++
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		if used != 1 {
			t.Errorf("the code was used %d times", used)
		}
	})
}
