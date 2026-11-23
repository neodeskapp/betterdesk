package api

import (
	"net/http"
	"reflect"
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

// A save puts into use exactly what a restart would load: an empty Group
// filter, User filter or attribute means its default at once, not "none"
// until the next restart (L350).
func TestSavedLDAPConfigIsWhatARestartLoads(t *testing.T) {
	srv, _ := newInternalServer(t, nil)
	srv.InitLDAP()
	body := `{"enabled":true,"host":"ldap.invalid","base_dn":"dc=corp,dc=example","group_filter":"","user_filter":"","group_attr_name":"","default_role":""}`
	if status, out := asConsole(t, srv, http.MethodPut, "/api/auth/ldap/config", body); status != http.StatusOK {
		t.Fatalf("save: %d %s", status, out)
	}
	running, loaded := srv.ldapProvider.Config(), *srv.loadLDAPConfigFromDB()
	if !reflect.DeepEqual(running, loaded) {
		t.Errorf("in use after the save:\n%+v\nafter a restart:\n%+v", running, loaded)
	}
	if running.GroupFilter != "(member={{dn}})" {
		t.Errorf("group filter in use: %q", running.GroupFilter)
	}
}
