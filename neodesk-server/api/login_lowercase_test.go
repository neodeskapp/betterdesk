package api

import (
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

// TestDirectorySignInComparesNamesInLowerCase: the directory names an
// account in lower case, and the sign-in finds the account whose name is
// that in lower case, so a directory account made under a mixed-case name
// (before the start's migration renamed it, or after it) signs in as
// itself and gets no second, lower-case account. A name two accounts
// share in lower case, or a local account of that name in another case,
// is refused and makes no account either. With both the mixed-case and
// the lower-case account there (what the migration leaves and logs), the
// lower-case one signs in (BUGS L128, DECISIONS A142).
func TestDirectorySignInComparesNamesInLowerCase(t *testing.T) {
	database := testSetupDB(t)
	defer database.Close()
	for _, name := range []string{"Alice", "Gina", "Bob", "bob", "Dan", "DAN"} {
		createLDAPTestUser(t, database, name, auth.RoleViewer)
	}
	if err := database.CreateUser(&db.User{Username: "Erin", PasswordHash: "hash", Role: auth.RoleAdmin, AuthProvider: db.AuthProviderLocal}); err != nil {
		t.Fatal(err)
	}
	accounts := func() int {
		t.Helper()
		users, err := database.ListUsers()
		if err != nil {
			t.Fatal(err)
		}
		return len(users)
	}
	before := accounts()
	srv := newClientLoginTestServer(database)
	srv.ldapProvider = &mockLDAPProvider{
		enabled: true,
		fn: func(username, password string) (*auth.LDAPResult, error) {
			return &auth.LDAPResult{Authenticated: true, Username: strings.ToLower(strings.TrimSpace(username)), Role: auth.RoleViewer}, nil
		},
	}

	for _, c := range []struct{ typed, account string }{
		{"alice", "Alice"},
		{"Alice", "Alice"},
		{"GINA", "Gina"},
		{"Bob", "bob"},
		{"bob", "bob"},
	} {
		result := srv.authenticatePasswordLogin(c.typed, "dir-pass", "127.0.0.1")
		if result.Status != passwordLoginOK || result.User == nil || result.User.Username != c.account {
			t.Errorf("sign-in as %q: status %v user %+v, want the account %q", c.typed, result.Status, result.User, c.account)
		}
	}
	for _, typed := range []string{"dan", "Dan", "erin"} {
		if result := srv.authenticatePasswordLogin(typed, "dir-pass", "127.0.0.1"); result.Status != passwordLoginInvalid {
			t.Errorf("sign-in as %q: status %v, want invalid", typed, result.Status)
		}
	}
	if got := accounts(); got != before {
		t.Errorf("%d accounts after the sign-ins, want the %d there were", got, before)
	}
	if u, _ := database.GetUser("Erin"); u == nil || u.AuthProvider != db.AuthProviderLocal {
		t.Errorf("the local account Erin after the refused sign-in: %+v", u)
	}

	// The start's migration renames Alice and Gina, and keeps Bob, Dan
	// and DAN, whose names another account has in lower case
	if err := database.Migrate(); err != nil {
		t.Fatal(err)
	}
	for typed, account := range map[string]string{"Alice": "alice", "gina": "gina", "BOB": "bob"} {
		result := srv.authenticatePasswordLogin(typed, "dir-pass", "127.0.0.1")
		if result.Status != passwordLoginOK || result.User == nil || result.User.Username != account {
			t.Errorf("after the migration, sign-in as %q: status %v user %+v, want the account %q", typed, result.Status, result.User, account)
		}
	}
	if got := accounts(); got != before {
		t.Errorf("%d accounts after the migration and the sign-ins, want %d", got, before)
	}
}
