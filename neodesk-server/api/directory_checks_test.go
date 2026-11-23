package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Directory and single sign-on settings that can't work are refused at
// the save with the field and a reason, not stored to fail every sign-in
// later; settings that work are saved (L353, L356, L418).
func TestDirectorySettingsThatCannotWorkAreRefused(t *testing.T) {
	srv, _ := newInternalServer(t, nil)
	srv.InitLDAP()
	srv.InitOIDC()
	const ldapBase = `"enabled":true,"host":"ldap.invalid","base_dn":"dc=corp,dc=example"`
	const oidcBase = `"enabled":true,"auto_discovery":true,"client_id":"c","redirect_url":"https://console.example.org/api/auth/oidc/callback"`
	for _, c := range []struct {
		path, body, field string
	}{
		{"/api/auth/ldap/config", `{` + ldapBase + `,"user_filter":"(uid={{username}}"}`, "user_filter"},
		{"/api/auth/ldap/config", `{` + ldapBase + `,"user_filter":"(uid=alice)"}`, "user_filter"},
		{"/api/auth/ldap/config", `{` + ldapBase + `,"group_filter":"member={{dn}})"}`, "group_filter"},
		{"/api/auth/ldap/config", `{` + ldapBase + `,"group_role_map":"neodesk-admins=superuser"}`, "group_role_map"},
		{"/api/auth/ldap/config", `{` + ldapBase + `,"group_role_map":"neodesk-admins=admin\nneodesk-operators"}`, "group_role_map"},
		{"/api/auth/ldap/config", `{` + ldapBase + `,"port":99999}`, "port"},
		{"/api/auth/ldap/config", `{` + ldapBase + `,"port":-1}`, "port"},
		{"/api/auth/ldap/config", `{` + ldapBase + `,"conn_timeout_sec":100000}`, "conn_timeout_sec"},
		{"/api/auth/ldap/config", `{` + ldapBase + `,"default_role":"superuser"}`, "default_role"},
		{"/api/auth/ldap/config", `{"enabled":true,"host":"ldap.invalid:636","base_dn":"dc=corp,dc=example"}`, "host"},
		{"/api/auth/ldap/config", `{"enabled":true,"host":"ldap.invalid","base_dn":"not a dn"}`, "base_dn"},
		{"/api/auth/ldap/config", `{"enabled":true,"host":"ldap.invalid","direct_bind":true,"direct_bind_dn":"uid=alice,dc=corp,dc=example"}`, "direct_bind_dn"},
		{"/api/auth/oidc/config", `{` + oidcBase + `,"issuer_url":"not a url"}`, "issuer_url"},
		{"/api/auth/oidc/config", `{` + oidcBase + `,"issuer_url":"https://10.20.30.40"}`, "issuer_url"},
		{"/api/auth/oidc/config", `{` + oidcBase + `,"auto_discovery":false,"issuer_url":"https://idp.example.org"}`, "authorization_url"},
		{"/api/auth/oidc/config", `{` + oidcBase + `,"auto_discovery":false,"authorization_url":"https://idp.example.org/auth","token_url":"token"}`, "token_url"},
		{"/api/auth/oidc/config", `{` + oidcBase + `,"issuer_url":"https://idp.example.org","default_role":"superuser"}`, "default_role"},
		{"/api/auth/oidc/config", `{` + oidcBase + `,"issuer_url":"https://idp.example.org","group_role_map":"admins=root"}`, "group_role_map"},
	} {
		status, body := asConsole(t, srv, http.MethodPut, c.path, c.body)
		var out struct {
			Error string `json:"error"`
			Field string `json:"field"`
		}
		_ = json.Unmarshal([]byte(body), &out)
		if status != http.StatusBadRequest || out.Field != c.field || out.Error == "" {
			t.Errorf("%s %s: %d %s; want 400 on %s", c.path, c.body, status, body, c.field)
		}
	}
	// Settings that work are taken, also with the private issuer's network allowed
	for _, c := range []struct{ path, body string }{
		{"/api/auth/ldap/config", `{` + ldapBase + `,"port":636,"conn_timeout_sec":5,"user_filter":"(uid={{username}})","group_filter":"(&(objectClass=groupOfNames)(member={{dn}}))","group_role_map":"cn=admins,ou=groups,dc=corp,dc=example=admin|operators=operator\n"}`},
		{"/api/auth/ldap/config", `{"enabled":true,"host":"2001:db8::1","direct_bind":true,"direct_bind_dn":"uid={{username}},ou=people,dc=corp,dc=example"}`},
		{"/api/auth/ldap/config", `{"enabled":false,"host":"","port":0}`},
		{"/api/auth/oidc/config", `{` + oidcBase + `,"issuer_url":"https://10.20.30.40","allowed_private_cidrs":"10.20.0.0/16","group_role_map":"admins=admin\noperators=operator"}`},
		{"/api/auth/oidc/config", `{` + oidcBase + `,"auto_discovery":false,"authorization_url":"https://idp.example.org/auth","token_url":"https://idp.example.org/token"}`},
	} {
		if status, body := asConsole(t, srv, http.MethodPut, c.path, c.body); status != http.StatusOK || strings.Contains(body, "error") {
			t.Errorf("%s %s: %d %s; want it saved", c.path, c.body, status, body)
		}
	}
	// The connection test refuses a broken filter before it connects
	status, body := asConsole(t, srv, http.MethodPost, "/api/auth/ldap/test", `{"host":"127.0.0.1","port":1,"user_filter":"(uid={{username}}"}`)
	if status != http.StatusOK || !strings.Contains(body, `"field":"user_filter"`) || !strings.Contains(body, `"success":false`) {
		t.Errorf("test with a broken filter: %d %s", status, body)
	}
}
