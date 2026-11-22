package db

import (
	"strconv"
	"strings"
	"testing"
)

// TestDatabaseParity: three smaller differences between the databases, each
// answered the same on both (BUGS RV80): a soft-deleted device has no uuid
// lookup, a folder contract's target name is the folder's name, and the
// direct grants work on a database with only the tables Migrate makes.
func TestDatabaseParity(t *testing.T) {
	freshStores(t, func(t *testing.T, database Database) {
		store := database.(PanelGroupStore)

		if err := database.UpsertPeer(&Peer{ID: "RV80GONE", UUID: "cnY4MC1nb25l", Status: "ONLINE"}); err != nil {
			t.Fatal(err)
		}
		if err := database.DeletePeer("RV80GONE"); err != nil {
			t.Fatal(err)
		}
		if p, err := database.GetPeerByUUID("cnY4MC1nb25l"); err != nil || p != nil {
			t.Errorf("uuid lookup of a soft-deleted device: %+v %v", p, err)
		}

		folder := &Folder{Name: "RV80 folder"}
		if err := store.CreatePanelFolder(folder); err != nil {
			t.Fatal(err)
		}
		if err := database.CreateBillingPackage(&BillingPackage{ID: "rv80-package", Name: "RV80", Currency: "EUR"}); err != nil {
			t.Fatal(err)
		}
		contract := &BillingContract{
			ID: "rv80-contract", TargetType: BillingTargetFolder, TargetKey: strconv.FormatInt(folder.ID, 10),
			PackageID: "rv80-package", Status: "active", Currency: "EUR",
		}
		if err := database.CreateBillingContract(contract); err != nil {
			t.Fatal(err)
		}
		if got, err := database.GetBillingContract(contract.ID); err != nil || got == nil || got.TargetName != "RV80 folder" {
			t.Errorf("folder contract: %+v %v; want the target name RV80 folder", got, err)
		}

		user := &User{Username: "rv80user", PasswordHash: "x", Role: "viewer"}
		if err := database.CreateUser(user); err != nil {
			t.Fatal(err)
		}
		if err := store.SetUserPeerGrants(user.ID, []string{"RV80PEER"}); err != nil {
			t.Fatalf("SetUserPeerGrants: %v", err)
		}
		if grants, err := store.ListPanelUserPeerGrants(user.ID); err != nil || strings.Join(grants, ",") != "RV80PEER" {
			t.Errorf("grants %q, %v", grants, err)
		}
	})
}
