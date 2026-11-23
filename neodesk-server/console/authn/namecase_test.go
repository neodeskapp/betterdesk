package authn

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

const (
	ldapOn       = `{"status":200,"body":{"ldap_enabled":true,"oidc_enabled":false,"any_enabled":true}}`
	ldapAccepted = `{"status":200,"body":{"token":"t","role":"viewer","auth_provider":"ldap"}}`
)

// A typed name that differs from a directory account only in letter case
// or in spaces at the ends signs in to that account, as the directory and
// the RustDesk client sign-in do (L115); for a local account it is refused,
// because it would be a second account without the first one's second
// factor and restrictions (RV117). No second account is made.
func TestSignInComparesDirectoryNamesInLowerCase(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		s, stub, _ := testService(t, db, nil)
		alice := addUser(t, s, "alice", "pw", "operator", "ldap")
		addUser(t, s, "Bob", "pw", "operator", "local")
		addUser(t, s, "carl", "pw", "operator", "local")
		for _, typed := range []string{"Alice", "ALICE", " alice", "alice "} {
			stub.load(&goScript{SSO: json.RawMessage(ldapOn), Login: []json.RawMessage{json.RawMessage(ldapAccepted)}})
			outcome, err := s.Authenticate(ctx, typed, "pw")
			if err != nil || outcome.User == nil || outcome.User.ID.V != alice || outcome.User.Username != "alice" {
				t.Errorf("%q: %+v, %v; want the account alice", typed, outcome, err)
			}
		}
		for _, typed := range []string{"bob", "BOB", "Carl", " carl"} {
			stub.load(&goScript{SSO: json.RawMessage(ldapOn), Login: []json.RawMessage{json.RawMessage(ldapAccepted)}})
			outcome, err := s.Authenticate(ctx, typed, "pw")
			if err != nil || outcome.User != nil || outcome.IsFailure() {
				t.Errorf("%q: %+v, %v; want the plain refusal", typed, outcome, err)
			}
		}
		var n int
		if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil || n != 3 {
			t.Errorf("%d accounts afterwards, want 3: %v", n, err)
		}
		// The exact names still sign in
		stub.load(&goScript{SSO: json.RawMessage(ldapOn), Login: []json.RawMessage{json.RawMessage(ldapAccepted)}})
		if outcome, err := s.Authenticate(ctx, "alice", "pw"); err != nil || outcome.User == nil {
			t.Errorf("alice: %+v, %v", outcome, err)
		}
		if outcome, err := s.Authenticate(ctx, "Bob", "pw"); err != nil || outcome.User == nil {
			t.Errorf("Bob: %+v, %v", outcome, err)
		}
	})
}

// A new directory account is made under the name lower-cased and trimmed,
// the name the directory's entry has; a new local account keeps the name
// as typed (RV117).
func TestNewDirectoryAccountsAreLowerCase(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		s, stub, _ := testService(t, db, nil)
		stub.load(&goScript{SSO: json.RawMessage(ldapOn), Login: []json.RawMessage{json.RawMessage(ldapAccepted)}})
		outcome, err := s.Authenticate(ctx, " Carol ", "pw")
		if err != nil || outcome.User == nil || outcome.User.Username != "carol" {
			t.Fatalf("new directory account: %+v, %v", outcome, err)
		}
		if row, err := getUserByUsername(ctx, db, "carol"); err != nil || row == nil || row.AuthProvider.String != "ldap" {
			t.Errorf("the row: %+v, %v", row, err)
		}
		// Next time another spelling signs in to the same account (L115)
		stub.load(&goScript{SSO: json.RawMessage(ldapOn), Login: []json.RawMessage{json.RawMessage(ldapAccepted)}})
		if outcome, _ := s.Authenticate(ctx, "Carol", "pw"); outcome.User == nil || outcome.User.Username != "carol" {
			t.Errorf("Carol after carol was made: %+v", outcome.User)
		}

		local := `{"status":200,"body":{"token":"t","role":"viewer","auth_provider":"local"}}`
		stub.load(&goScript{SSO: json.RawMessage(ldapOn), Login: []json.RawMessage{json.RawMessage(local)}})
		if outcome, err := s.Authenticate(ctx, "Dave", "pw"); err != nil || outcome.User == nil || outcome.User.Username != "Dave" {
			t.Errorf("new local account: %+v, %v", outcome, err)
		}
	})
}

// With a User name attribute other than the typed name (mail), the server
// names the account by the directory entry: the console signs in to that
// account and makes no second one under the typed name (L351).
func TestDirectorySignInUsesTheServersAccountName(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		s, stub, _ := testService(t, db, nil)
		byMail := `{"status":200,"body":{"token":"t","role":"operator","username":"hank@example.org","auth_provider":"ldap"}}`
		// The server made its row in the shared table during its sign-in
		hank := addUser(t, s, "hank@example.org", "pw", "operator", "ldap")
		for _, typed := range []string{"hank", "Hank"} {
			stub.load(&goScript{SSO: json.RawMessage(ldapOn), Login: []json.RawMessage{json.RawMessage(byMail)}})
			outcome, err := s.Authenticate(ctx, typed, "pw")
			if err != nil || outcome.User == nil || outcome.User.ID.V != hank || outcome.User.Username != "hank@example.org" {
				t.Errorf("%q: %+v, %v; want the account hank@example.org", typed, outcome.User, err)
			}
		}
		var n int
		if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil || n != 1 {
			t.Errorf("%d accounts afterwards, want 1: %v", n, err)
		}
		// Where the server made no row, the new one has the server's name too
		ivy := `{"status":200,"body":{"token":"t","role":"viewer","username":"ivy@example.org","auth_provider":"ldap"}}`
		stub.load(&goScript{SSO: json.RawMessage(ldapOn), Login: []json.RawMessage{json.RawMessage(ivy)}})
		if outcome, err := s.Authenticate(ctx, "ivy", "pw"); err != nil || outcome.User == nil || outcome.User.Username != "ivy@example.org" {
			t.Errorf("ivy: %+v, %v", outcome.User, err)
		}
		if row, err := getUserByUsername(ctx, db, "ivy"); err != nil || row != nil {
			t.Errorf("a row under the typed name: %+v, %v", row, err)
		}
	})
}
