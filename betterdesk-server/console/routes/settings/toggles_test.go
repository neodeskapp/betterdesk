package settings_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

func setting(t *testing.T, db *store.DB, key string) string {
	t.Helper()
	var value string
	if err := db.QueryRow(context.Background(), "SELECT value FROM settings WHERE key = ?", key).Scan(&value); err != nil {
		return "<none>"
	}
	return value
}

func TestDeviceScope(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		s := newSite(t, db, nil, nil)
		admin := s.As("admin")
		ctx := context.Background()
		if res := admin.Do("GET", "/api/settings/device-scope", ""); res.Body != `{"success":true,"data":{"mode":"open"}}` {
			t.Errorf("default: %s", res.Body)
		}
		// The default is read once and kept: a save must drop it
		access := s.Server.Deps().Access
		if access.DeviceScopeRestrictedDefault(ctx) {
			t.Fatal("restricted before the save")
		}
		for _, c := range []struct{ body, want, stored string }{
			{`{"mode":"RESTRICTED"}`, `200 {"success":true,"data":{"mode":"restricted"}}`, "restricted"},
			{`{"mode":"closed"}`, `400 {"success":false,"error":"settings.device_scope_invalid"}`, "restricted"},
			// JavaScript lowers a dotted capital I to an i and a dot
			{`{"mode":"RESTRİCTED"}`, `400 {"success":false,"error":"settings.device_scope_invalid"}`, "restricted"},
			{`{"mode":5}`, `400 {"success":false,"error":"settings.device_scope_invalid"}`, "restricted"},
			{`{}`, `200 {"success":true,"data":{"mode":"open"}}`, "open"},
			{`{"mode":["restricted"]}`, `200 {"success":true,"data":{"mode":"restricted"}}`, "restricted"},
			{`{"mode":""}`, `200 {"success":true,"data":{"mode":"open"}}`, "open"},
		} {
			res := admin.Do("POST", "/api/settings/device-scope", c.body)
			if got := strconv.Itoa(res.Status) + " " + res.Body; got != c.want {
				t.Errorf("%s: %s, want %s", c.body, got, c.want)
			}
			if got := setting(t, db, "device_scope_default"); got != c.stored {
				t.Errorf("%s: stored %q", c.body, got)
			}
			if got := access.DeviceScopeRestrictedDefault(ctx); got != (c.stored == "restricted") {
				t.Errorf("%s: the cached default says restricted=%v", c.body, got)
			}
		}
		if row := lastAudit(t, s.World); row != "device_scope_default_updated|Device scope default: open|127.0.0.1|1" {
			t.Errorf("audit %s", row)
		}
		exec(t, db, "UPDATE settings SET value = 'Restricted' WHERE key = 'device_scope_default'")
		if res := admin.Do("GET", "/api/settings/device-scope", ""); res.Body != `{"success":true,"data":{"mode":"restricted"}}` {
			t.Errorf("stored in capitals: %s", res.Body)
		}
	})
}

func TestAvailabilityHistory(t *testing.T) {
	s := newSite(t, nil, nil, nil)
	admin := s.As("admin")
	for _, c := range []struct{ enabled, retention, want string }{
		{`{"value":"Yes "}`, `{"value":"90"}`, `{"enabled":true,"retention_days":90}`},
		{`{"value":"on"}`, `{"value":99999}`, `{"enabled":true,"retention_days":3650}`},
		{`{"value":"no"}`, `{"value":"0"}`, `{"enabled":false,"retention_days":30}`},
		{`{"value":1}`, `{"value":"12days"}`, `{"enabled":true,"retention_days":12}`},
		{`{"error":"gone"}`, `{"error":"gone"}`, `{"enabled":false,"retention_days":30}`},
		{`{}`, `{"value":null}`, `{"enabled":false,"retention_days":30}`},
	} {
		s.API.On("GET /api/config/availability_history_enabled", http.StatusOK, c.enabled)
		s.API.On("GET /api/config/availability_history_retention_days", http.StatusOK, c.retention)
		res := admin.Do("GET", "/api/settings/availability-history", "")
		if want := `{"success":true,"data":` + c.want + `}`; res.Body != want {
			t.Errorf("%s %s: %s, want %s", c.enabled, c.retention, res.Body, want)
		}
	}
	s.API.Calls()

	s.API.On("PUT /api/config/availability_history_enabled", http.StatusOK, `{"status":"ok"}`)
	s.API.On("PUT /api/config/availability_history_retention_days", http.StatusOK, `{"status":"ok"}`)
	for _, c := range []struct{ body, want, calls string }{
		{`{"enabled":"x","retention_days":"45.9"}`, `{"enabled":true,"retention_days":45}`, `"true"|"45"`},
		{`{"enabled":0,"retention_days":-3}`, `{"enabled":false,"retention_days":30}`, `"false"|"30"`},
		{`{"retention_days":1e9}`, `{"enabled":false,"retention_days":3650}`, `"false"|"3650"`},
		{`{"retention_days":[7,8]}`, `{"enabled":false,"retention_days":7}`, `"false"|"7"`},
		{`{"retention_days":0.5}`, `{"enabled":false,"retention_days":30}`, `"false"|"30"`},
	} {
		res := admin.Do("POST", "/api/settings/availability-history", c.body)
		if want := `{"success":true,"data":` + c.want + `}`; res.Body != want {
			t.Errorf("%s: %s, want %s", c.body, res.Body, want)
		}
		calls := s.API.Calls()
		want := strings.Split(c.calls, "|")
		if len(calls) != 2 || !strings.HasSuffix(calls[0], `{"value":`+want[0]+`}`) || !strings.HasSuffix(calls[1], `{"value":`+want[1]+`}`) {
			t.Errorf("%s: calls %v", c.body, calls)
		}
	}
	if row := lastAudit(t, s.World); row != "availability_history_updated|Availability history: enabled=false, retention_days=30|127.0.0.1|1" {
		t.Errorf("audit %s", row)
	}
	s.API.On("PUT /api/config/availability_history_retention_days", http.StatusBadRequest, `{"error":"invalid value"}`)
	res := admin.Do("POST", "/api/settings/availability-history", `{}`)
	if res.Status != http.StatusBadGateway || res.Body != `{"success":false,"error":"invalid value"}` {
		t.Errorf("refused retention: %d %s", res.Status, res.Body)
	}
	s.API.On("PUT /api/config/availability_history_enabled", http.StatusInternalServerError, `{"error":""}`)
	res = admin.Do("POST", "/api/settings/availability-history", `{}`)
	if res.Status != http.StatusBadGateway || res.Body != `{"success":false,"error":"Server error"}` {
		t.Errorf("refused switch: %d %s", res.Status, res.Body)
	}
}

func TestEnrollment(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		s := newSite(t, db, nil, nil)
		admin := s.As("admin")
		s.API.On("GET /api/enrollment/mode", http.StatusOK, `{"mode":"managed"}`)
		s.API.On("GET /api/enrollment/pending", http.StatusOK, `{"devices":[],"count":3}`)
		res := admin.Do("GET", "/api/settings/enrollment", "")
		if res.Body != `{"success":true,"data":{"mode":"managed","require_approval":true,"rich_approve":true,"tag_picker":true,"pending_count":3}}` {
			t.Errorf("get: %s", res.Body)
		}
		s.API.On("GET /api/enrollment/mode", http.StatusInternalServerError, `{}`)
		s.API.On("GET /api/enrollment/pending", http.StatusInternalServerError, `{}`)
		exec(t, db, "INSERT INTO settings (key, value) VALUES ('enrollment_rich_approve', 'No'), ('enrollment_tag_picker', '')")
		res = admin.Do("GET", "/api/settings/enrollment", "")
		if res.Body != `{"success":true,"data":{"mode":"open","require_approval":false,"rich_approve":false,"tag_picker":true,"pending_count":0}}` {
			t.Errorf("get without the server: %s", res.Body)
		}

		s.API.On("PUT /api/enrollment/mode", http.StatusOK, `{"status":"ok"}`)
		s.API.On("GET /api/enrollment/mode", http.StatusOK, `{"mode":"locked"}`)
		s.API.Calls()
		for _, c := range []struct{ body, want, put, audit string }{
			{`{"require_approval":true,"mode":"locked"}`, `200`, `{"mode":"managed"}`, "managed"},
			{`{"require_approval":false}`, `200`, `{"mode":"open"}`, "open"},
			{`{"require_approval":false,"mode":"locked"}`, `200`, `{"mode":"locked"}`, "locked"},
			{`{"require_approval":"yes","mode":"managed"}`, `200`, `{"mode":"managed"}`, "managed"},
			{`{"rich_approve":1,"tag_picker":null}`, `200`, ``, "unchanged"},
			{`{"mode":"closed"}`, `400 {"success":false,"error":"Invalid enrollment mode"}`, ``, ""},
			{`{"mode":null}`, `400 {"success":false,"error":"Invalid enrollment mode"}`, ``, ""},
			{`{"mode":""}`, `400 {"success":false,"error":"Invalid enrollment mode"}`, ``, ""},
		} {
			before := len(s.AuditRows())
			res := admin.Do("PUT", "/api/settings/enrollment", c.body)
			got := strconv.Itoa(res.Status)
			if res.Status != http.StatusOK {
				got += " " + res.Body
			}
			if got != c.want {
				t.Errorf("%s: %s %s", c.body, got, res.Body)
			}
			var put string
			for _, call := range s.API.Calls() {
				if strings.HasPrefix(call, "PUT ") {
					put = strings.TrimPrefix(call, "PUT /api/enrollment/mode ")
				}
			}
			if put != c.put {
				t.Errorf("%s: sent %q", c.body, put)
			}
			rows := s.AuditRows()
			if c.audit == "" && len(rows) != before {
				t.Errorf("%s: logged %v", c.body, rows[before:])
			}
			if c.audit != "" && rows[len(rows)-1] != "enrollment_settings_changed|Enrollment settings updated (mode="+c.audit+")|127.0.0.1|1" {
				t.Errorf("%s: audit %s", c.body, rows[len(rows)-1])
			}
		}
		if got := setting(t, db, "enrollment_rich_approve") + "," + setting(t, db, "enrollment_tag_picker"); got != "true,false" {
			t.Errorf("toggles %s", got)
		}
		res = admin.Do("PUT", "/api/settings/enrollment", `{}`)
		if res.Body != `{"success":true,"data":{"mode":"locked","require_approval":false,"rich_approve":true,"tag_picker":false}}` {
			t.Errorf("answer: %s", res.Body)
		}
		s.API.On("PUT /api/enrollment/mode", http.StatusBadRequest, `{"error":"no"}`)
		res = admin.Do("PUT", "/api/settings/enrollment", `{"mode":"open"}`)
		if res.Status != http.StatusInternalServerError || res.Body != `{"success":false,"error":"Request failed with status code 400"}` {
			t.Errorf("refused mode: %d %s", res.Status, res.Body)
		}
	})
}

// The directory and single sign-on settings are the server's: the routes
// pass the body on and the answer back, and the server masks the secrets.
func TestDirectoryAndSingleSignOn(t *testing.T) {
	s := newSite(t, nil, nil, nil)
	admin := s.As("admin")
	masked := `{"enabled":false,"host":"ldap.example.org","bind_password":"••••••••","port":389}`
	s.API.On("GET /api/auth/ldap/config", http.StatusOK, masked)
	s.API.On("GET /api/auth/oidc/config", http.StatusOK, `{"client_id":"c","client_secret":"••••••••"}`)
	if res := admin.Do("GET", "/api/settings/ldap", ""); res.Body != masked {
		t.Errorf("ldap: %s", res.Body)
	}
	if res := admin.Do("GET", "/api/settings/oidc", ""); res.Body != `{"client_id":"c","client_secret":"••••••••"}` {
		t.Errorf("oidc: %s", res.Body)
	}
	s.API.On("GET /api/auth/ldap/config", http.StatusOK, `{"error":"no config"}`)
	if res := admin.Do("GET", "/api/settings/ldap", ""); res.Status != 500 || res.Body != `{"success":false,"error":"no config"}` {
		t.Errorf("ldap error: %d %s", res.Status, res.Body)
	}
	s.API.On("GET /api/auth/oidc/config", http.StatusOK, `{"error":""}`)
	if res := admin.Do("GET", "/api/settings/oidc", ""); res.Status != 500 || res.Body != `{"success":false,"error":"Failed to load OIDC config"}` {
		t.Errorf("oidc error: %d %s", res.Status, res.Body)
	}
	s.API.Calls()

	s.API.On("PUT /api/auth/ldap/config", http.StatusOK, `{"status":"ok"}`)
	body := `{"enabled":true,"host":"ldap.example.org","bind_password":"throwaway"}`
	if res := admin.Do("PUT", "/api/settings/ldap", body); res.Body != `{"success":true}` {
		t.Errorf("ldap save: %s", res.Body)
	}
	if calls := s.API.Calls(); !reflect.DeepEqual(calls, []string{"PUT /api/auth/ldap/config " + body}) {
		t.Errorf("ldap save sent %v", calls)
	}
	if row := lastAudit(t, s.World); row != "ldap_config_updated|Updated LDAP configuration|127.0.0.1|1" {
		t.Errorf("audit %s", row)
	}
	s.API.On("PUT /api/auth/ldap/config", http.StatusBadRequest, `{"error":"LDAP host is required"}`)
	if res := admin.Do("PUT", "/api/settings/ldap", `{"enabled":true}`); res.Status != 400 || res.Body != `{"success":false,"error":"Request failed with status code 400"}` {
		t.Errorf("ldap refused: %d %s", res.Status, res.Body)
	}
	s.API.On("POST /api/auth/ldap/test", http.StatusOK, `{"success":false,"error":"dial tcp: refused"}`)
	if res := admin.Do("POST", "/api/settings/ldap/test", `{"host":"127.0.0.1"}`); res.Status != 200 || res.Body != `{"success":false,"error":"dial tcp: refused"}` {
		t.Errorf("ldap test: %d %s", res.Status, res.Body)
	}
	s.API.On("POST /api/auth/ldap/test", http.StatusOK, `{"success":true,"message":"Connection successful"}`)
	if res := admin.Do("POST", "/api/settings/ldap/test", `{"host":"127.0.0.1"}`); res.Body != `{"success":true,"message":"Connection successful"}` {
		t.Errorf("ldap test: %s", res.Body)
	}
	s.API.On("POST /api/auth/oidc/test", http.StatusBadRequest, `{"error":"Issuer URL is required"}`)
	if res := admin.Do("POST", "/api/settings/oidc/test", `{}`); res.Status != 200 || res.Body != `{"success":false,"error":"Request failed with status code 400"}` {
		t.Errorf("oidc test: %d %s", res.Status, res.Body)
	}
	s.API.Calls()

	s.API.On("PUT /api/auth/oidc/config", http.StatusOK, `{"status":"ok"}`)
	for _, c := range []struct {
		body string
		ok   bool
	}{
		{`{"enabled":true,"redirect_url":"https://console.example.org/login"}`, false},
		{`{"enabled":true,"redirect_url":5}`, false},
		{`{"enabled":"yes"}`, false},
		{`{"enabled":true,"redirect_url":"ftp://console.example.org/api/auth/oidc/callback"}`, false},
		{`{"enabled":true,"redirect_url":" https://console.example.org:5443/api/auth/oidc/callback/ "}`, true},
		{`{"enabled":true,"redirect_url":"http://10.0.0.5:21114/api/oidc/callback"}`, true},
		{`{"enabled":false,"redirect_url":"anything"}`, true},
		{`{"enabled":0}`, true},
	} {
		res := admin.Do("PUT", "/api/settings/oidc", c.body)
		calls := s.API.Calls()
		if c.ok && (res.Body != `{"success":true}` || len(calls) != 1 || calls[0] != "PUT /api/auth/oidc/config "+c.body) {
			t.Errorf("%s: %s %v", c.body, res.Body, calls)
		}
		if !c.ok && (res.Status != 400 || res.Body != `{"success":false,"error":"settings.oidc_redirect_url_invalid"}` || len(calls) != 0) {
			t.Errorf("%s: %d %s %v", c.body, res.Status, res.Body, calls)
		}
	}
	if row := lastAudit(t, s.World); row != "oidc_config_updated|Updated OIDC configuration|127.0.0.1|1" {
		t.Errorf("audit %s", row)
	}
}

func TestClientSessions(t *testing.T) {
	s := newSite(t, nil, nil, nil)
	admin := s.As("admin")
	s.API.On("GET /api/config/client_session_expiry_days", http.StatusOK, `{"value":"500"}`)
	s.API.On("GET /api/config/client_session_sliding", http.StatusOK, `{"value":"OFF"}`)
	s.API.On("GET /api/config/client_session_max_days", http.StatusOK, `{"value":"0"}`)
	if res := admin.Do("GET", "/api/settings/client-sessions", ""); res.Body != `{"success":true,"data":{"expiry_days":365,"sliding":false,"max_days":30}}` {
		t.Errorf("get: %s", res.Body)
	}
	s.API.On("GET /api/config/client_session_sliding", http.StatusOK, `{"value":"maybe"}`)
	s.API.On("GET /api/config/client_session_expiry_days", http.StatusNotFound, `{}`)
	if res := admin.Do("GET", "/api/settings/client-sessions", ""); res.Body != `{"success":true,"data":{"expiry_days":7,"sliding":true,"max_days":30}}` {
		t.Errorf("get with odd values: %s", res.Body)
	}
	s.API.Calls()

	for _, key := range []string{"expiry_days", "sliding", "max_days"} {
		s.API.On("PUT /api/config/client_session_"+key, http.StatusOK, `{"status":"ok"}`)
	}
	envPath := filepath.Join(s.Console, ".env")
	res := admin.Do("PUT", "/api/settings/client-sessions", `{"expiry_days":"12.9","sliding":"false","max_days":1000}`)
	if res.Body != `{"success":true}` {
		t.Errorf("save: %s", res.Body)
	}
	want := []string{
		`PUT /api/config/client_session_expiry_days {"value":"12"}`,
		`PUT /api/config/client_session_sliding {"value":"true"}`,
		`PUT /api/config/client_session_max_days {"value":"365"}`,
	}
	if calls := s.API.Calls(); !reflect.DeepEqual(calls, want) {
		t.Errorf("calls %v", calls)
	}
	if _, err := os.Stat(envPath); err == nil {
		t.Error("a .env was created")
	}
	// The next client token lasts as long as was just saved
	if days := s.Server.Deps().Authn.TokenExpiryDays(); days != 12 {
		t.Errorf("client tokens last %d days after the save, want 12", days)
	}
	if row := lastAudit(t, s.World); row != "client_session_settings_changed|RustDesk client sessions: 12d sliding=true max=365d|127.0.0.1|1" {
		t.Errorf("audit %s", row)
	}
	if err := os.WriteFile(envPath, []byte("PORT=5000\nAPI_TOKEN_EXPIRY_DAYS=7\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	s.API.Calls()
	if res := admin.Do("PUT", "/api/settings/client-sessions", `{"expiry_days":9,"sliding":false}`); res.Body != `{"success":true}` {
		t.Errorf("save: %s", res.Body)
	}
	if calls := s.API.Calls(); len(calls) != 3 || calls[1] != `PUT /api/config/client_session_sliding {"value":"false"}` {
		t.Errorf("sliding false: %v", calls)
	}
	if content, _ := os.ReadFile(envPath); string(content) != "PORT=5000\nAPI_TOKEN_EXPIRY_DAYS=9\n" {
		t.Errorf(".env: %q", content)
	}
	// A .env that cannot be written does not undo the save
	if err := os.Remove(envPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(envPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if res := admin.Do("PUT", "/api/settings/client-sessions", `{}`); res.Body != `{"success":true}` {
		t.Errorf("save with a .env that is a folder: %s", res.Body)
	}
	if calls := s.API.Calls(); len(calls) != 3 || calls[1] != `PUT /api/config/client_session_sliding {"value":"true"}` {
		t.Errorf("sliding missing: %v", calls)
	}
	s.API.On("PUT /api/config/client_session_sliding", http.StatusInternalServerError, `{"error":"Go server down"}`)
	s.API.Calls()
	res = admin.Do("PUT", "/api/settings/client-sessions", `{}`)
	if res.Status != http.StatusInternalServerError || res.Body != `{"success":false,"error":"Go server down"}` {
		t.Errorf("refused: %d %s", res.Status, res.Body)
	}
	if calls := s.API.Calls(); len(calls) != 3 {
		t.Errorf("all three values are sent, also when one fails: %v", calls)
	}
}

func TestConnectionModeAndPublicEndpoints(t *testing.T) {
	s := newSite(t, nil, nil, nil)
	admin := s.As("admin")
	s.API.On("GET /api/health", http.StatusOK, `{"status":"ok","connection":{"p2p_first":false}}`)
	res := admin.Do("GET", "/api/settings/connection-mode", "")
	if res.Body != `{"success":true,"data":{"mode":"p2p_first","p2p_fallback_ms":2000,"same_nat_relay":true,"allow_shared_nat_initiator":false,"logged_in_only_initiator":false,"operator_only_outbound":false,"source":"defaults","writable":false,"deployment":"defaults","runtime":{"p2p_first":false}}}` {
		t.Errorf("get: %s", res.Body)
	}
	s.API.On("GET /api/health", http.StatusOK, `{"status":"ok","connection":""}`)
	if res := admin.Do("GET", "/api/settings/connection-mode", ""); !strings.HasSuffix(res.Body, `"runtime":null}}`) {
		t.Errorf("get without runtime: %s", res.Body)
	}
	before := len(s.AuditRows())
	res = admin.Do("PUT", "/api/settings/connection-mode", `{"mode":"relay_only"}`)
	if res.Status != http.StatusBadRequest || res.Body != `{"success":false,"error":"settings.connection_mode_not_configurable"}` || len(s.AuditRows()) != before {
		t.Errorf("save without a place for it: %d %s", res.Status, res.Body)
	}

	if res := admin.Do("GET", "/api/settings/public-endpoints", ""); res.Body != `{"success":true,"data":{"public_server_id":"","public_relay_server":"","public_api_url":"","env_override_active":false}}` {
		t.Errorf("get endpoints: %s", res.Body)
	}
	for _, c := range []struct{ body, want string }{
		{`{"public_server_id":"bad host!"}`, `{"success":false,"error":"settings.public_endpoints_invalid_host"}`},
		{`{"public_relay_server":"relay.example.org\nX=1"}`, `{"success":false,"error":"settings.public_endpoints_invalid_host"}`},
		{`{"public_api_url":"ftp://api.example.org"}`, `{"success":false,"error":"settings.public_endpoints_invalid_api_url"}`},
	} {
		if res := admin.Do("PUT", "/api/settings/public-endpoints", c.body); res.Status != http.StatusBadRequest || res.Body != c.want {
			t.Errorf("%s: %d %s", c.body, res.Status, res.Body)
		}
	}
	res = admin.Do("PUT", "/api/settings/public-endpoints", `{"public_server_id":"ID.example.org","public_api_url":"https://api.example.org:21114/"}`)
	if res.Body != `{"success":true,"data":{"public_server_id":"ID.example.org","public_relay_server":"","public_api_url":"https://api.example.org:21114","env_override_active":true},"message":"settings.public_endpoints_saved"}` {
		t.Errorf("save endpoints: %s", res.Body)
	}
	if row := lastAudit(t, s.World); row != "public_endpoints_changed|RustDesk public endpoints updated (ID=ID.example.org, relay=-, api=https://api.example.org:21114)|127.0.0.1|1" {
		t.Errorf("audit %s", row)
	}
	if content, _ := os.ReadFile(filepath.Join(s.DataDir, "public-endpoints.env")); !strings.Contains(string(content), "PUBLIC_SERVER_ID=ID.example.org") {
		t.Errorf("durable file: %q", content)
	}
}

func TestAuditLimit(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		s := newSite(t, db, nil, nil)
		for i := 0; i < 105; i++ {
			exec(t, db, "INSERT INTO audit_log (user_id, action, details, ip_address, created_at) VALUES (1, ?, 'd', '127.0.0.1', ?)",
				"a"+strconv.Itoa(i), fmt.Sprintf("2099-01-01 00:%02d:%02d", 10+i/60, i%60))
		}
		admin := s.As("admin")
		count := func(query string) (int, string) {
			res := admin.Do("GET", "/api/settings/audit"+query, "")
			if res.Status != http.StatusOK {
				return -res.Status, res.Body
			}
			return len(decode(t, res.Body)["data"].([]any)), res.Body
		}
		for query, want := range map[string]int{"": 100, "?limit=abc": 100, "?limit=0": 100, "?limit=3": 3, "?limit=2x": 2, "?limit=1&limit=5": 1, "?limit[x]=4": 100} {
			if got, body := count(query); got != want {
				t.Errorf("%s: %d rows, want %d (%.200s)", query, got, want, body)
			}
		}
		got, body := count("?limit=-1")
		if db.IsPostgres() && got != -500 || !db.IsPostgres() && got < 105 {
			t.Errorf("negative limit: %d %.200s", got, body)
		}
		if got, _ := count("?limit=99999999999999999999"); got != -500 {
			t.Errorf("limit beyond 64 bits: %d", got)
		}
		_, body = count("?limit=1")
		row := decode(t, body)["data"].([]any)[0].(map[string]any)
		if row["username"] != "e2e_admin" || row["action"] != "a104" || row["user_id"] != 1.0 {
			t.Errorf("row %v", row)
		}
		if keys := keyOrder(t, mustRow(t, body)); keys != "id,user_id,action,details,ip_address,created_at,username" {
			t.Errorf("keys %s in %s", keys, mustRow(t, body))
		}
	})
}

// mustRow is the JSON text of the first entry of data.
func mustRow(t *testing.T, body string) string {
	t.Helper()
	data := rawField(t, body, "data")
	start := strings.Index(data, "{")
	end := strings.Index(data, "}")
	if start < 0 || end < start {
		t.Fatalf("no row in %s", data)
	}
	return data[start : end+1]
}
