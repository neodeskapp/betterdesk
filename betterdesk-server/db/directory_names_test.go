package db

import (
	"bytes"
	"log"
	"os"
	"strings"
	"testing"
)

// TestDirectoryAccountsGetTheDirectorysName: at start the migration
// gives a directory account made under a mixed-case name (before the
// sign-in used the directory's own name, BUGS RV117) its name in lower
// case and without spaces at the ends, with what keys on the name (its
// address book). A name that another account has in that form already,
// or that two accounts would share, stays and is named in the log. Local
// and single sign-on accounts keep their spelling. A second start
// changes nothing (BUGS L53).
func TestDirectoryAccountsGetTheDirectorysName(t *testing.T) {
	freshStores(t, func(t *testing.T, store Database) {
		ids := map[string]int64{}
		for _, a := range []struct{ name, provider string }{
			{"Alice", AuthProviderLDAP},
			{" Gina ", AuthProviderLDAP},
			{"Carol", AuthProviderLDAP},
			{"carol", AuthProviderLocal},
			{"Frank", AuthProviderLDAP},
			{"FRANK", AuthProviderLDAP},
			{"erin", AuthProviderLDAP},
			{"Dave", AuthProviderLocal},
			{"Olga", AuthProviderOIDC},
		} {
			u := &User{Username: a.name, PasswordHash: "x", Role: "viewer", AuthProvider: a.provider}
			if err := store.CreateUser(u); err != nil {
				t.Fatalf("create %q: %v", a.name, err)
			}
			ids[a.name] = u.ID
		}
		if err := store.SaveAddressBook("Alice", "legacy", `{"peers":["1"]}`); err != nil {
			t.Fatalf("save the address book: %v", err)
		}

		var logs bytes.Buffer
		log.SetOutput(&logs)
		defer log.SetOutput(os.Stderr)
		if err := store.Migrate(); err != nil {
			t.Fatalf("Migrate: %v", err)
		}

		want := map[string]string{
			"Alice": "alice", " Gina ": "gina", "Carol": "Carol", "carol": "carol", "Frank": "Frank",
			"FRANK": "FRANK", "erin": "erin", "Dave": "Dave", "Olga": "Olga",
		}
		for before, after := range want {
			u, err := store.GetUserByID(ids[before])
			if err != nil || u == nil {
				t.Fatalf("load %q: %v", before, err)
			}
			if u.Username != after {
				t.Errorf("%q is now %q, want %q", before, u.Username, after)
			}
		}
		if book, err := store.GetAddressBook("alice", "legacy"); err != nil || book != `{"peers":["1"]}` {
			t.Errorf("alice's address book: %q, %v", book, err)
		}
		out := logs.String()
		for _, kept := range []string{`"Carol"`, `"Frank"`, `"FRANK"`} {
			if !strings.Contains(out, kept) {
				t.Errorf("the log does not name %s, which keeps its name:\n%s", kept, out)
			}
		}
		for _, other := range []string{`"Dave"`, `"Olga"`, `"erin"`} {
			if strings.Contains(out, other) {
				t.Errorf("the log names %s, which is no directory account to rename:\n%s", other, out)
			}
		}

		logs.Reset()
		if err := store.Migrate(); err != nil {
			t.Fatalf("Migrate again: %v", err)
		}
		if strings.Contains(logs.String(), "renamed to") {
			t.Errorf("the second start renamed accounts:\n%s", logs.String())
		}
		for before, after := range want {
			if u, _ := store.GetUserByID(ids[before]); u == nil || u.Username != after {
				t.Errorf("after the second start %q is %+v, want %q", before, u, after)
			}
		}
	})
}
