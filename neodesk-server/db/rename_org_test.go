package db

import (
	"errors"
	"testing"
	"time"
)

// TestRenameUserOrgAccounts: renaming a server account renames its own
// membership rows in organizations and leaves an organization's own account
// of the same name alone. A name an organization of the account already
// has is refused, and nothing is renamed. On a database with only the
// tables Migrate makes (no console tables) the rename works on both
// databases (BUGS RV75).
func TestRenameUserOrgAccounts(t *testing.T) {
	freshStores(t, func(t *testing.T, database Database) {
		for _, org := range []*Organization{
			{ID: "rv75-linked", Name: "Linked", Slug: "rv75-linked", Settings: "{}", CreatedAt: time.Now()},
			{ID: "rv75-other", Name: "Other", Slug: "rv75-other", Settings: "{}", CreatedAt: time.Now()},
		} {
			if err := database.CreateOrganization(org); err != nil {
				t.Fatal(err)
			}
		}
		user := &User{Username: "rv75dave", PasswordHash: "x", Role: "admin"}
		if err := database.CreateUser(user); err != nil {
			t.Fatal(err)
		}
		if _, err := database.LinkUserToOrg("rv75-linked", user.ID, "admin"); err != nil {
			t.Fatal(err)
		}
		for _, own := range []*OrgUser{
			// The other organization's own account with the same name
			{ID: "rv75-own-dave", OrgID: "rv75-other", Username: "rv75dave", PasswordHash: "own", Role: "user", CreatedAt: time.Now()},
			// A member of the linked organization
			{ID: "rv75-own-erin", OrgID: "rv75-linked", Username: "rv75erin", PasswordHash: "own", Role: "user", CreatedAt: time.Now()},
		} {
			if err := database.CreateOrgUser(own); err != nil {
				t.Fatal(err)
			}
		}
		orgName := func(id string) string {
			t.Helper()
			u, err := database.GetOrgUser(id)
			if err != nil || u == nil {
				t.Fatalf("GetOrgUser(%s): %v %v", id, u, err)
			}
			return u.Username
		}
		linked, err := database.GetOrgUserByServerUserID("rv75-linked", user.ID)
		if err != nil || linked == nil {
			t.Fatalf("linked row: %v %v", linked, err)
		}

		if err := database.RenameUser(user.ID, "rv75erin"); !errors.Is(err, ErrUsernameTaken) {
			t.Errorf("rename to a member name of a linked organization: %v, want ErrUsernameTaken", err)
		}
		if u, _ := database.GetUserByID(user.ID); u == nil || u.Username != "rv75dave" {
			t.Errorf("account after the refused rename: %+v", u)
		}
		if got := orgName(linked.ID); got != "rv75dave" {
			t.Errorf("linked row after the refused rename: %q", got)
		}

		if err := database.RenameUser(user.ID, "rv75frank"); err != nil {
			t.Fatalf("rename: %v", err)
		}
		if u, _ := database.GetUserByID(user.ID); u == nil || u.Username != "rv75frank" {
			t.Errorf("account after the rename: %+v", u)
		}
		if got := orgName(linked.ID); got != "rv75frank" {
			t.Errorf("linked row after the rename: %q, want rv75frank", got)
		}
		if got := orgName("rv75-own-dave"); got != "rv75dave" {
			t.Errorf("the other organization's own account: %q, want rv75dave", got)
		}
		if got := orgName("rv75-own-erin"); got != "rv75erin" {
			t.Errorf("the linked organization's member: %q, want rv75erin", got)
		}
	})
}
