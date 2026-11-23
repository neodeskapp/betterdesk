package authn

import (
	"context"
	"fmt"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// An IPv6 address counts as its /64 in the lockout of an account at an
// address and in the limit of an address: one host commonly has a whole
// /64, and a new address for every guess would never block (RV118).
func TestLockoutCountsAnIPv6NetworkAsOneAddress(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		s, _, _ := testService(t, db, func(o *Options) {
			o.Settings = Settings{MaxFailedAttempts: 3, LockoutMinutes: 15, IPRateLimit: 5, AttemptWindowMinutes: 15}
		})
		record := func(name, ip string) {
			t.Helper()
			if err := s.RecordAttempt(ctx, name, ip, false); err != nil {
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

		for i := 1; i <= 3; i++ {
			record("alice", fmt.Sprintf("2001:db8:1:1::%x", i))
		}
		if got := check("alice", "2001:db8:1:1:ffff::9"); !got.Blocked || got.Reason != reasonAccountLocked {
			t.Errorf("alice from the same /64: %+v", got)
		}
		if got := check("alice", "2001:DB8:1:1:0:0:0:1"); !got.Blocked {
			t.Errorf("alice from the same /64 written out: %+v", got)
		}
		// Another /64 is another address (A68)
		if got := check("alice", "2001:db8:1:2::1"); got.Blocked {
			t.Errorf("alice from another /64: %+v", got)
		}

		// The limit of an address counts the /64 too
		for i := 1; i <= 5; i++ {
			record(fmt.Sprintf("user%d", i), fmt.Sprintf("2001:db8:2:1::%x", i))
		}
		if got := check("someone", "2001:db8:2:1::abcd"); !got.Blocked || got.Reason != reasonIPLimited {
			t.Errorf("the address limit of a /64: %+v", got)
		}

		// IPv4, also written as IPv6, stays as it is
		record("bob", "198.51.100.1")
		record("bob", "::ffff:198.51.100.2")
		var stored []string
		rows, err := db.Query(ctx, `SELECT ip_address FROM login_attempts WHERE username IN ('alice', 'bob') ORDER BY id`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var ip string
			if err := rows.Scan(&ip); err != nil {
				t.Fatal(err)
			}
			stored = append(stored, ip)
		}
		want := []string{"2001:db8:1:1::/64", "2001:db8:1:1::/64", "2001:db8:1:1::/64", "198.51.100.1", "::ffff:198.51.100.2"}
		if fmt.Sprint(stored) != fmt.Sprint(want) {
			t.Errorf("stored addresses %v, want %v", stored, want)
		}
	})
}
