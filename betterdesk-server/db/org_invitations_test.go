package db

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// RedeemOrgInvitation uses an open invitation once and makes the account
// in the same transaction; a used, expired or unknown invitation and a
// taken username are refused without using anything, and two redemptions
// of one invitation at once make one account (BUGS L42).
func TestRedeemOrgInvitation(t *testing.T) {
	freshStores(t, func(t *testing.T, store Database) {
		if err := store.CreateOrganization(&Organization{ID: "org-i", Name: "Invites", Slug: "invites", Settings: "{}", CreatedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
		invite := func(hash string, expires time.Time) {
			t.Helper()
			if err := store.CreateOrgInvitation(&OrgInvitation{ID: "inv-" + hash, OrgID: "org-i", Token: hash, Email: hash + "@example.invalid", Role: OrgRoleUser, ExpiresAt: expires}); err != nil {
				t.Fatal(err)
			}
		}
		account := func(name string) *OrgUser {
			return &OrgUser{ID: "ou-" + name, Username: name, PasswordHash: "hash", Role: OrgRoleUser, CreatedAt: time.Now().UTC()}
		}
		hour := time.Now().UTC().Add(time.Hour)
		invite("h1", hour)
		invite("h2", hour)
		invite("h3", time.Now().UTC().Add(-time.Minute))
		invite("h4", hour)

		inv, err := store.RedeemOrgInvitation("h1", account("anna"))
		if err != nil || inv == nil || inv.ID != "inv-h1" || inv.UsedAt == nil {
			t.Fatalf("redeem h1: %+v %v", inv, err)
		}
		if u, err := store.GetOrgUserByUsername("org-i", "anna"); err != nil || u == nil || u.ID != "ou-anna" {
			t.Fatalf("the account of h1: %+v %v", u, err)
		}
		if stored, err := store.GetOrgInvitationByToken("h1"); err != nil || stored == nil || stored.UsedAt == nil {
			t.Errorf("h1 after redeeming: %+v %v, want used", stored, err)
		}
		for _, c := range []struct {
			hash, name string
			want       error
		}{
			{"h1", "bert", ErrInvitationNotOpen},
			// a used invitation says so before it says anything of names
			{"h1", "anna", ErrInvitationNotOpen},
			{"h2", "anna", ErrOrgUsernameTaken},
			{"h3", "carl", ErrInvitationNotOpen},
			{"unknown", "dora", ErrInvitationNotOpen},
		} {
			if _, err := store.RedeemOrgInvitation(c.hash, account(c.name)); !errors.Is(err, c.want) {
				t.Errorf("redeem %s as %s: %v, want %v", c.hash, c.name, err, c.want)
			}
			if u, _ := store.GetOrgUserByUsername("org-i", c.name); c.name != "anna" && u != nil {
				t.Errorf("redeem %s made the account %s", c.hash, c.name)
			}
		}
		if stored, err := store.GetOrgInvitationByToken("h2"); err != nil || stored == nil || stored.UsedAt != nil {
			t.Errorf("h2 after a taken username: %+v %v, want still open", stored, err)
		}

		var wg sync.WaitGroup
		results := make([]error, 2)
		for i, name := range []string{"eve", "finn"} {
			wg.Add(1)
			go func(i int, name string) {
				defer wg.Done()
				_, results[i] = store.RedeemOrgInvitation("h4", account(name))
			}(i, name)
		}
		wg.Wait()
		made := 0
		for _, err := range results {
			if err == nil {
				made++
			} else if !errors.Is(err, ErrInvitationNotOpen) {
				t.Errorf("a redemption at the same time: %v", err)
			}
		}
		if made != 1 {
			t.Errorf("two redemptions of h4 at once made %d accounts, want 1", made)
		}
	})
}
