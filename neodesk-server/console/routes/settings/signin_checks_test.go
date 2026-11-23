package settings_test

import (
	"net/http"
	"strings"
	"testing"
)

// Directory and single sign-on settings that can't work are refused by the
// console before they reach the server, with the field and the key of the
// reason, whose texts the auth package's test holds against the language
// files (L353, L356, L418, L419). The test site has no language files, so
// an answer names the key.
func TestDirectorySettingsRefusedOnTheField(t *testing.T) {
	s := newSite(t, nil, nil, nil)
	admin := s.As("admin")
	s.API.On("PUT /api/auth/ldap/config", http.StatusOK, `{"status":"ok"}`)
	s.API.On("PUT /api/auth/oidc/config", http.StatusOK, `{"status":"ok"}`)
	s.API.On("POST /api/auth/ldap/test", http.StatusOK, `{"success":true,"message":"Connection successful"}`)
	s.API.Calls()
	const ldap = `"enabled":true,"host":"ldap.example.org","base_dn":"dc=example,dc=org"`
	const oidc = `"enabled":true,"auto_discovery":true,"client_id":"c","redirect_url":"https://console.example.org/api/auth/oidc/callback"`
	for _, c := range []struct {
		method, path, body, lang string
		status                   int
		want                     string
	}{
		{"PUT", "/api/settings/ldap", `{` + ldap + `,"user_filter":"(uid={{username}}"}`, "", 400,
			`{"success":false,"error":"settings.signin.check_filter_invalid","field":"user_filter"}`},
		{"PUT", "/api/settings/ldap", `{` + ldap + `,"port":99999}`, "de", 400,
			`{"success":false,"error":"settings.signin.check_port_range","field":"port"}`},
		{"PUT", "/api/settings/ldap", `{` + ldap + `,"port":2.5}`, "", 400,
			`{"success":false,"error":"settings.signin.check_port_range","field":"port"}`},
		{"PUT", "/api/settings/ldap", `{` + ldap + `,"conn_timeout_sec":"abc"}`, "", 400,
			`{"success":false,"error":"settings.signin.check_timeout_range","field":"conn_timeout_sec"}`},
		{"PUT", "/api/settings/ldap", `{` + ldap + `,"group_role_map":"admins=admin|neodesk-admins=superuser"}`, "de", 400,
			`{"success":false,"error":"settings.signin.check_map_role","field":"group_role_map"}`},
		{"PUT", "/api/settings/ldap", `{"enabled":true}`, "", 400,
			`{"success":false,"error":"settings.signin.check_host_required","field":"host"}`},
		{"POST", "/api/settings/ldap/test", `{"host":"127.0.0.1","group_filter":"(member={{dn}}"}`, "", 200,
			`{"success":false,"error":"settings.signin.check_filter_invalid","field":"group_filter"}`},
		{"PUT", "/api/settings/oidc", `{` + oidc + `,"issuer_url":"not a url"}`, "", 400,
			`{"success":false,"error":"settings.signin.check_url_invalid","field":"issuer_url"}`},
		{"PUT", "/api/settings/oidc", `{` + oidc + `,"issuer_url":"https://10.1.2.3"}`, "", 400,
			`{"success":false,"error":"settings.signin.check_issuer_private","field":"issuer_url"}`},
		{"PUT", "/api/settings/oidc", `{"enabled":true,"auto_discovery":false,"client_id":"c","redirect_url":"https://console.example.org/api/auth/oidc/callback"}`, "", 400,
			`{"success":false,"error":"settings.signin.check_endpoint_required","field":"authorization_url"}`},
	} {
		var header []string
		if c.lang != "" {
			header = []string{"Accept-Language", c.lang}
		}
		res := admin.Do(c.method, c.path, c.body, header...)
		if res.Status != c.status || res.Body != c.want {
			t.Errorf("%s %s %s:\n got %d %s\nwant %d %s", c.method, c.path, c.body, res.Status, res.Body, c.status, c.want)
		}
	}
	if calls := s.API.Calls(); len(calls) != 0 {
		t.Errorf("refused settings reached the server: %v", calls)
	}
	// What works goes on to the server
	for _, c := range []struct{ method, path, body string }{
		{"PUT", "/api/settings/ldap", `{` + ldap + `,"port":636,"conn_timeout_sec":null,"user_filter":"(uid={{username}})","group_role_map":"admins=admin\noperators=operator"}`},
		{"PUT", "/api/settings/ldap", `{"enabled":false,"host":"","port":0}`},
		{"POST", "/api/settings/ldap/test", `{"host":"127.0.0.1","port":389}`},
		{"PUT", "/api/settings/oidc", `{` + oidc + `,"issuer_url":"https://10.1.2.3","allowed_private_cidrs":"10.1.0.0/16"}`},
	} {
		if res := admin.Do(c.method, c.path, c.body); res.Status != http.StatusOK {
			t.Errorf("%s %s %s: %d %s", c.method, c.path, c.body, res.Status, res.Body)
		}
	}
	if calls := s.API.Calls(); len(calls) != 4 {
		t.Errorf("calls of the good settings: %v", calls)
	}
}

// Client session numbers out of range are refused with the field, not
// replaced by a default or the nearest limit (L418).
func TestClientSessionNumbersRefused(t *testing.T) {
	s := newSite(t, nil, nil, nil)
	admin := s.As("admin")
	for _, key := range []string{"expiry_days", "sliding", "max_days"} {
		s.API.On("PUT /api/config/client_session_"+key, http.StatusOK, `{"status":"ok"}`)
		s.API.On("GET /api/config/client_session_"+key, http.StatusOK, `{"value":""}`)
	}
	s.API.Calls()
	for _, c := range []struct{ body, field string }{
		{`{"expiry_days":0,"sliding":true,"max_days":30}`, "expiry_days"},
		{`{"expiry_days":-5,"sliding":true,"max_days":30}`, "expiry_days"},
		{`{"expiry_days":2.6,"sliding":true,"max_days":30}`, "expiry_days"},
		{`{"expiry_days":"","sliding":true,"max_days":30}`, "expiry_days"},
		{`{"expiry_days":7,"sliding":true,"max_days":99999}`, "max_days"},
		{`{"expiry_days":10,"sliding":true,"max_days":5}`, "max_days"},
	} {
		res := admin.Do("PUT", "/api/settings/client-sessions", c.body)
		if res.Status != http.StatusBadRequest || !strings.Contains(res.Body, `"field":"`+c.field+`"`) {
			t.Errorf("%s: %d %s; want 400 on %s", c.body, res.Status, res.Body, c.field)
		}
	}
	if calls := s.API.Calls(); len(calls) != 0 {
		t.Errorf("refused numbers reached the server: %v", calls)
	}
	if res := admin.Do("PUT", "/api/settings/client-sessions", `{"expiry_days":10,"sliding":false,"max_days":5}`); res.Status != http.StatusOK {
		t.Errorf("a maximum below the duration without renewal: %d %s", res.Status, res.Body)
	}
}
