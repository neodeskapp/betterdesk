package billing

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

// eachDatabase runs test on a new SQLite database and, when
// NEODESK_TEST_POSTGRES_DSN names a server, on a new PostgreSQL
// database, both migrated.
func eachDatabase(t *testing.T, test func(t *testing.T, database db.Database)) {
	t.Run("sqlite", func(t *testing.T) {
		database, err := db.OpenSQLite(filepath.Join(t.TempDir(), "billing.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { database.Close() })
		if err := database.Migrate(); err != nil {
			t.Fatal(err)
		}
		test(t, database)
	})
	t.Run("postgres", func(t *testing.T) {
		dsn := os.Getenv("NEODESK_TEST_POSTGRES_DSN")
		if dsn == "" {
			t.Skip("NEODESK_TEST_POSTGRES_DSN is not set")
		}
		ctx := context.Background()
		name := fmt.Sprintf("bdgo_billing_%d", time.Now().UnixNano())
		admin, err := pgx.Connect(ctx, dsn)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
			admin.Close(ctx)
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
			admin.Close(ctx)
		})
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		u.Path = "/" + name
		database, err := db.OpenPostgres(u.String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { database.Close() })
		if err := database.Migrate(); err != nil {
			t.Fatal(err)
		}
		test(t, database)
	})
}

// TestSuspendedContractRefusesConnections: a device whose contract is
// suspended is refused with billing_suspended, and is let through again
// when the contract is active (RV226).
func TestSuspendedContractRefusesConnections(t *testing.T) {
	eachDatabase(t, func(t *testing.T, database db.Database) {
		if err := database.CreateBillingPackage(&db.BillingPackage{ID: "pkg", Name: "Support", IncludedMinutes: 60, Currency: "EUR"}); err != nil {
			t.Fatal(err)
		}
		contract := &db.BillingContract{
			ID: "suspended", TargetType: db.BillingTargetDevice, TargetKey: "SUSPDEV1",
			PackageID: "pkg", Status: ContractSuspended, RemainingMinutes: 60, Currency: "EUR",
		}
		if err := database.CreateBillingContract(contract); err != nil {
			t.Fatal(err)
		}
		svc := NewService(database, nil, 1, false)

		check := svc.CheckConnection("SUSPDEV1")
		if check.Allowed || check.Reason != "billing_suspended" || check.ContractID != "suspended" {
			t.Errorf("suspended contract: %+v, want refused with billing_suspended", check)
		}

		contract.Status = ContractActive
		if err := database.UpdateBillingContract(contract); err != nil {
			t.Fatal(err)
		}
		if check := svc.CheckConnection("SUSPDEV1"); !check.Allowed || !check.HasBilling {
			t.Errorf("active contract: %+v, want allowed with billing", check)
		}
	})
}
