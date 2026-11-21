package billing

import (
	"path/filepath"
	"testing"

	"github.com/unitronix/betterdesk-server/db"
	"github.com/unitronix/betterdesk-server/timesync"
)

func TestCheckConnectionWithUnsyncedClock(t *testing.T) {
	database, err := db.OpenSQLite(filepath.Join(t.TempDir(), "billing-clock.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := database.CreateBillingPackage(&db.BillingPackage{ID: "pkg", Name: "Support", IncludedMinutes: 60, Currency: "EUR"}); err != nil {
		t.Fatalf("CreateBillingPackage: %v", err)
	}
	if err := database.CreateBillingContract(&db.BillingContract{
		ID: "contract", TargetType: db.BillingTargetDevice, TargetKey: "CLOCKDEV1",
		PackageID: "pkg", Status: ContractActive, RemainingMinutes: 60, Currency: "EUR",
	}); err != nil {
		t.Fatalf("CreateBillingContract: %v", err)
	}

	for _, tc := range []struct {
		requireSync bool
		allowed     bool
	}{
		{requireSync: false, allowed: true},
		{requireSync: true, allowed: false},
	} {
		// A clock that has not been checked, as on a host that cannot reach NTP.
		clock := timesync.NewService(nil, timesync.Config{RequireSync: tc.requireSync})
		check := NewService(database, clock, 1, false).CheckConnection("CLOCKDEV1")
		if !check.HasBilling {
			t.Fatalf("require sync %v: the device's contract was not found: %+v", tc.requireSync, check)
		}
		if check.Allowed != tc.allowed {
			t.Fatalf("require sync %v: allowed = %v (reason %q), want %v", tc.requireSync, check.Allowed, check.Reason, tc.allowed)
		}
	}
}
