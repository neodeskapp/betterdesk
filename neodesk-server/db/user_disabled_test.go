package db

import "testing"

// An account is enabled when it is made; the disabled flag another writer
// sets (the console's Users page) reads back through every lookup, and an
// UpdateUser of the account's other fields leaves it as it is (L107).
func TestUserDisabledFlag(t *testing.T) {
	freshStores(t, func(t *testing.T, store Database) {
		u := &User{Username: "disabled-dora", PasswordHash: "hash", Role: "operator"}
		if err := store.CreateUser(u); err != nil {
			t.Fatal(err)
		}
		got, err := store.GetUser("disabled-dora")
		if err != nil || got == nil {
			t.Fatalf("GetUser: %v, %v", got, err)
		}
		if got.Disabled {
			t.Fatal("a new account is disabled")
		}

		rawExec(t, store, `UPDATE users SET disabled = ? WHERE id = ?`, true, u.ID)
		byName, err := store.GetUser("disabled-dora")
		if err != nil || byName == nil || !byName.Disabled {
			t.Fatalf("GetUser after disabling: %+v, %v", byName, err)
		}
		byID, err := store.GetUserByID(u.ID)
		if err != nil || byID == nil || !byID.Disabled {
			t.Fatalf("GetUserByID after disabling: %+v, %v", byID, err)
		}
		all, err := store.ListUsers()
		if err != nil {
			t.Fatal(err)
		}
		listed := false
		for _, each := range all {
			if each.ID == u.ID {
				listed = each.Disabled
			}
		}
		if !listed {
			t.Error("ListUsers does not report the account as disabled")
		}

		// A role change written from a copy read before the account was
		// disabled does not enable it again
		got.Role = "viewer"
		if err := store.UpdateUser(got); err != nil {
			t.Fatal(err)
		}
		after, err := store.GetUser("disabled-dora")
		if err != nil || after == nil || !after.Disabled || after.Role != "viewer" {
			t.Fatalf("after UpdateUser: %+v, %v", after, err)
		}
	})
}
