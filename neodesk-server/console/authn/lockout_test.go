package authn

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// TestLockoutIsPerAccountAndAddress: failed sign-ins lock an account for
// the address they came from only, so nobody who knows the name can keep
// its owner out from elsewhere; a success from the address ends its
// lockout there, and the limit of each address stays (BUGS UM21,
// DECISIONS A68).
func TestLockoutIsPerAccountAndAddress(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		s, _, _ := testService(t, db, func(o *Options) {
			o.Settings = Settings{MaxFailedAttempts: 3, LockoutMinutes: 15, IPRateLimit: 5, AttemptWindowMinutes: 15}
		})
		record := func(name, ip string, success bool) {
			t.Helper()
			if err := s.RecordAttempt(ctx, name, ip, success); err != nil {
				t.Fatal(err)
			}
		}
		check := func(name, ip string) BruteForce {
			t.Helper()
			got, err := s.CheckBruteForce(ctx, name, ip)
			if err != nil {
				t.Fatal(err)
			}
			return got
		}

		// Failures spread over many addresses lock nothing
		for i := 0; i < 4; i++ {
			record("alice", fmt.Sprintf("198.51.100.%d", i+1), false)
		}
		for _, ip := range []string{"198.51.100.1", "198.51.100.9"} {
			if got := check("alice", ip); got.Blocked {
				t.Errorf("alice from %s after failures from four addresses: %+v", ip, got)
			}
		}

		// Three from one address lock the account for that address only
		for i := 0; i < 2; i++ {
			record("alice", "203.0.113.7", false)
		}
		if got := check("alice", "203.0.113.7"); got.Blocked {
			t.Errorf("alice after two failures: %+v", got)
		}
		record("alice", "203.0.113.7", false)
		got := check("alice", "203.0.113.7")
		if !got.Blocked || got.Reason != reasonAccountLocked || got.RetryUnknown || got.RetryAfter < 890 || got.RetryAfter > 900 {
			t.Errorf("alice after three failures from one address: %+v", got)
		}
		if got := check("alice", "203.0.113.8"); got.Blocked {
			t.Errorf("alice from another address: %+v", got)
		}
		if got := check("bob", "203.0.113.7"); got.Blocked {
			t.Errorf("another account from the address: %+v", got)
		}

		// A success from the address ends its lockout
		record("alice", "203.0.113.7", true)
		if got := check("alice", "203.0.113.7"); got.Blocked {
			t.Errorf("alice after a success: %+v", got)
		}

		// The address limit counts every account: five failures block the
		// address for anyone
		for _, name := range []string{"c1", "c2", "c3"} {
			record(name, "192.0.2.50", false)
		}
		record("alice", "192.0.2.50", false)
		if got := check("dave", "192.0.2.50"); got.Blocked {
			t.Errorf("an address with four failures: %+v", got)
		}
		record("bob", "192.0.2.50", false)
		if got := check("dave", "192.0.2.50"); !got.Blocked || got.Reason != reasonIPLimited {
			t.Errorf("an address with five failures: %+v", got)
		}

		// The lockout ends LockoutMinutes after the last failure
		s.now = func() time.Time { return time.Now().Add(16 * time.Minute) }
		if got := check("alice", "203.0.113.7"); got.Blocked {
			t.Errorf("alice after the lockout ran out: %+v", got)
		}
	})
}

// Failures further apart than the window lock nothing: the newest
// MaxFailedAttempts have to fall within it.
func TestLockoutCountsWithinTheWindow(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		s, _, _ := testService(t, db, func(o *Options) {
			o.Settings = Settings{MaxFailedAttempts: 3, LockoutMinutes: 15, IPRateLimit: 30, AttemptWindowMinutes: 15}
		})
		for _, minutesAgo := range []int{40, 20, 1} {
			mustExec(t, db, `INSERT INTO login_attempts (username, ip_address, success, created_at) VALUES ('alice', '203.0.113.7', `+db.Pick("0", "FALSE")+`, `+
				db.Pick("datetime('now', ?)", "NOW() + CAST(? AS INTERVAL)")+`)`, fmt.Sprintf("-%d minutes", minutesAgo))
		}
		if got, err := s.CheckBruteForce(ctx, "alice", "203.0.113.7"); err != nil || got.Blocked {
			t.Errorf("three failures over 40 minutes: %+v, %v", got, err)
		}
		mustExec(t, db, `INSERT INTO login_attempts (username, ip_address, success) VALUES ('alice', '203.0.113.7', `+db.Pick("0", "FALSE")+`)`)
		if got, err := s.CheckBruteForce(ctx, "alice", "203.0.113.7"); err != nil || got.Blocked {
			t.Errorf("the newest three failures over 20 minutes: %+v, %v", got, err)
		}
		mustExec(t, db, `INSERT INTO login_attempts (username, ip_address, success) VALUES ('alice', '203.0.113.7', `+db.Pick("0", "FALSE")+`)`)
		if got, err := s.CheckBruteForce(ctx, "alice", "203.0.113.7"); err != nil || !got.Blocked {
			t.Errorf("the newest three failures within a minute: %+v, %v", got, err)
		}
	})
}
