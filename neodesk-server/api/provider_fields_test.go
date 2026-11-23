package api

import (
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

// providerFieldsDB records what the sign-ins store from the provider.
type providerFieldsDB struct {
	db.Database
	stored []string
}

func (d *providerFieldsDB) UpdateUserFromProvider(id int64, email, displayName string) error {
	d.stored = append(d.stored, email+"|"+displayName)
	return d.Database.UpdateUserFromProvider(id, email, displayName)
}

// TestSignInsStoreTheProvidersEmailAndName: a directory sign-in, for a new
// and for an existing account, and a single sign-on store the email
// address and display name the provider gave (L113).
func TestSignInsStoreTheProvidersEmailAndName(t *testing.T) {
	base := testSetupDB(t)
	defer base.Close()
	database := &providerFieldsDB{Database: base}
	createLDAPTestUser(t, database, "known.ad", auth.RoleOperator)
	srv := newClientLoginTestServer(database)
	srv.ldapProvider = &mockLDAPProvider{
		enabled: true,
		fn: func(username, password string) (*auth.LDAPResult, error) {
			return &auth.LDAPResult{Authenticated: true, Username: username, Role: auth.RoleOperator,
				Email: username + "@example.org", DisplayName: "Name of " + username}, nil
		},
	}
	for _, name := range []string{"known.ad", "new.ad"} {
		if result := srv.authenticatePasswordLogin(name, "ad-pass", "127.0.0.1"); result.Status != passwordLoginOK {
			t.Fatalf("%s: status %v", name, result.Status)
		}
	}

	if err := base.CreateUser(&db.User{Username: "sso.anna", PasswordHash: "x", Role: auth.RoleViewer, AuthProvider: db.AuthProviderOIDC}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"sso.anna", "sso.new"} {
		user, code := srv.ensureOIDCUser(&auth.OIDCResult{Username: name, Email: name + "@idp.example", DisplayName: "SSO " + name},
			&auth.OIDCConfig{AllowSignup: true})
		if user == nil || code != "" {
			t.Fatalf("%s: %+v %q", name, user, code)
		}
	}

	want := []string{
		"known.ad@example.org|Name of known.ad", "new.ad@example.org|Name of new.ad",
		"sso.anna@idp.example|SSO sso.anna", "sso.new@idp.example|SSO sso.new",
	}
	if len(database.stored) != len(want) {
		t.Fatalf("stored %q, want %q", database.stored, want)
	}
	for i := range want {
		if database.stored[i] != want[i] {
			t.Errorf("stored[%d] = %q, want %q", i, database.stored[i], want[i])
		}
	}
}
