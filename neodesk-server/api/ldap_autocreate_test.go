package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/unitronix/betterdesk-server/auth"
)

// TestLDAPAutoCreateSwitch: Auto-create users is on for a directory that
// was never saved, a save without the field keeps it, and a save can turn
// it off; the settings show it (L114).
func TestLDAPAutoCreateSwitch(t *testing.T) {
	srv, _ := newInternalServer(t, nil)
	if !srv.loadLDAPConfigFromDB().AllowSignup {
		t.Fatal("a directory that was never saved does not create accounts")
	}
	for _, c := range []struct {
		body string
		want bool
	}{
		{`{"host":"ldap.invalid","allow_signup":false}`, false},
		{`{"host":"ldap.invalid"}`, false},
		{`{"host":"ldap.invalid","allow_signup":true}`, true},
		{`{"host":"ldap.invalid"}`, true},
	} {
		if status, body := asConsole(t, srv, http.MethodPut, "/api/auth/ldap/config", c.body); status != http.StatusOK {
			t.Fatalf("save %s: %d %s", c.body, status, body)
		}
		if got := srv.loadLDAPConfigFromDB().AllowSignup; got != c.want {
			t.Errorf("auto-create after saving %s: %v, want %v", c.body, got, c.want)
		}
	}
	status, body := asConsole(t, srv, http.MethodGet, "/api/auth/ldap/config", "")
	var shown map[string]any
	if err := json.Unmarshal([]byte(body), &shown); status != http.StatusOK || err != nil || shown["allow_signup"] != true {
		t.Errorf("the settings: %d %s", status, body)
	}
}

// noSignupLDAP is a directory with Auto-create users off.
type noSignupLDAP struct{ *mockLDAPProvider }

func (m noSignupLDAP) Config() auth.LDAPConfig {
	cfg := m.mockLDAPProvider.Config()
	cfg.AllowSignup = false
	return cfg
}

// TestLDAPWithoutAutoCreateSignsInOnlyExistingAccounts: with Auto-create
// users off, a person the directory accepts signs in only to an account
// that is there; nobody else gets one (L114).
func TestLDAPWithoutAutoCreateSignsInOnlyExistingAccounts(t *testing.T) {
	database := testSetupDB(t)
	defer database.Close()
	createLDAPTestUser(t, database, "known.ad", auth.RoleOperator)
	srv := newClientLoginTestServer(database)
	srv.ldapProvider = noSignupLDAP{&mockLDAPProvider{
		enabled: true,
		fn: func(username, password string) (*auth.LDAPResult, error) {
			return &auth.LDAPResult{Authenticated: true, Username: username, Role: auth.RoleOperator}, nil
		},
	}}

	if result := srv.authenticatePasswordLogin("new.ad", "ad-pass", "127.0.0.1"); result.Status != passwordLoginInvalid {
		t.Errorf("a person without an account: status %v, want invalid", result.Status)
	}
	if u, err := database.GetUser("new.ad"); err != nil || u != nil {
		t.Errorf("an account was made: %+v, %v", u, err)
	}
	if result := srv.authenticatePasswordLogin("known.ad", "ad-pass", "127.0.0.1"); result.Status != passwordLoginOK || result.User == nil {
		t.Errorf("the existing account: status %v user %+v, want OK", result.Status, result.User)
	}
}
