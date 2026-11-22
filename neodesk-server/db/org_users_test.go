package db

import (
	"testing"
	"time"
)

// UpdateOrgUser stores a new password hash with the other fields, on both
// databases, so changing an organization account's password changes it
// (BUGS L120).
func TestUpdateOrgUserStoresThePassword(t *testing.T) {
	freshStores(t, func(t *testing.T, store Database) {
		if err := store.CreateOrganization(&Organization{ID: "org-u", Name: "Update", Slug: "update", Settings: "{}", CreatedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
		u := &OrgUser{ID: "ou-u", OrgID: "org-u", Username: "person", PasswordHash: "old-hash", Role: OrgRoleUser, CreatedAt: time.Now().UTC()}
		if err := store.CreateOrgUser(u); err != nil {
			t.Fatal(err)
		}
		u.PasswordHash, u.DisplayName = "new-hash", "Person"
		if err := store.UpdateOrgUser(u); err != nil {
			t.Fatal(err)
		}
		got, err := store.GetOrgUser("ou-u")
		if err != nil || got == nil || got.PasswordHash != "new-hash" || got.DisplayName != "Person" {
			t.Fatalf("after the update: %+v (%v)", got, err)
		}
	})
}
