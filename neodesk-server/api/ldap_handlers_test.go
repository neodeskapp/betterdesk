package api

import (
	"net/http"
	"testing"
)

// TestSaveLDAPConfigClearsAnEmptyPassword: a save keeps the stored bind
// password when the body sends the mask or no bind_password, and clears it
// for an explicit empty one (BUGS SE5).
func TestSaveLDAPConfigClearsAnEmptyPassword(t *testing.T) {
	srv, _ := newInternalServer(t, nil)
	for _, c := range []struct{ body, want string }{
		{`{"host":"ldap.invalid","bind_dn":"cn=x","bind_password":"first-bind-pw"}`, "first-bind-pw"},
		{`{"host":"ldap.invalid","bind_dn":"cn=x","bind_password":"••••••••"}`, "first-bind-pw"},
		{`{"host":"ldap.invalid","bind_dn":"cn=x"}`, "first-bind-pw"},
		{`{"host":"ldap.invalid","bind_dn":"cn=x","bind_password":null}`, "first-bind-pw"},
		{`{"host":"ldap.invalid","bind_dn":"cn=x","bind_password":""}`, ""},
	} {
		if status, body := asConsole(t, srv, http.MethodPut, "/api/auth/ldap/config", c.body); status != http.StatusOK {
			t.Fatalf("save %s: %d %s", c.body, status, body)
		}
		if got := srv.loadLDAPConfigFromDB().BindPassword; got != c.want {
			t.Errorf("bind password after saving %s: %q, want %q", c.body, got, c.want)
		}
	}
}
