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

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
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
			// a body without the mode is refused, not read as open (BUGS C6)
			{`{}`, `400 {"success":false,"error":"No settings to save"}`, "restricted"},
			{`["open"]`, `400 {"success":false,"error":"No settings to save"}`, "restricted"},
			{`{"mode":["open"]}`, `200 {"success":true,"data":{"mode":"open"}}`, "open"},
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
	// Days that aren't a whole number from 1 to 3650 are refused with what
	// is wrong instead of saved as 30 or 3650 (L418)
	for _, body := range []string{`{"enabled":"x","retention_days":"45.9"}`, `{"enabled":0,"retention_days":-3}`, `{"retention_days":1e9}`,
		`{"retention_days":[7,8]}`, `{"retention_days":0.5}`, `{"retention_days":0}`, `{"retention_days":"abc"}`, `{"retention_days":3651}`} {
		res := admin.Do("POST", "/api/settings/availability-history", body)
		if res.Status != http.StatusBadRequest || res.Body != `{"success":false,"error":"settings.general.retention_error"}` {
			t.Errorf("%s: %d %s", body, res.Status, res.Body)
		}
		if calls := s.API.Calls(); len(calls) != 0 {
			t.Errorf("%s: calls %v", body, calls)
		}
	}
	for _, c := range []struct{ body, want, calls string }{
		{`{"enabled":"x","retention_days":"45"}`, `{"enabled":true,"retention_days":45}`, `"true"|"45"`},
		{`{"enabled":0,"retention_days":3650}`, `{"enabled":false,"retention_days":3650}`, `"false"|"3650"`},
		{`{"retention_days":1}`, `{"enabled":false,"retention_days":1}`, `"false"|"1"`},
		{`{"enabled":true}`, `{"enabled":true,"retention_days":30}`, `"true"|"30"`},
		{`{"retention_days":null}`, `{"enabled":false,"retention_days":30}`, `"false"|"30"`},
	} {
		res := admin.Do("POST", "/api/settings/availability-history", c.body)
		if want := `{"success":true,"data":` + c.want + `}`; res.Body != want {
			t.Errorf("%s: %s, want %s", c.body, res.Body, want)
		}
		// the values there before are read first (BUGS SE6)
		calls := s.API.Calls()
		want := strings.Split(c.calls, "|")
		if len(calls) != 4 || !strings.HasPrefix(calls[0], "GET ") || !strings.HasPrefix(calls[1], "GET ") ||
			!strings.HasSuffix(calls[2], `{"value":`+want[0]+`}`) || !strings.HasSuffix(calls[3], `{"value":`+want[1]+`}`) {
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
	// a refused retention puts the switch back as it was (BUGS SE6)
	s.API.On("GET /api/config/availability_history_enabled", http.StatusOK, `{"value":"true"}`)
	s.API.Calls()
	res = admin.Do("POST", "/api/settings/availability-history", `{"enabled":false}`)
	if calls := s.API.Calls(); res.Status != http.StatusBadGateway || len(calls) != 5 ||
		calls[2] != `PUT /api/config/availability_history_enabled {"value":"false"}` || calls[4] != `PUT /api/config/availability_history_enabled {"value":"true"}` {
		t.Errorf("refused retention after a stored switch: %d %s, calls %v", res.Status, res.Body, calls)
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
		if res.Status != http.StatusInternalServerError || res.Body != `{"success":false,"error":"no"}` {
			t.Errorf("refused mode: %d %s", res.Status, res.Body)
		}
		s.API.OnText("PUT /api/enrollment/mode", http.StatusInternalServerError, "Internal server error\n")
		res = admin.Do("PUT", "/api/settings/enrollment", `{"mode":"open"}`)
		if res.Status != http.StatusInternalServerError || res.Body != `{"success":false,"error":"Failed to set enrollment mode"}` {
			t.Errorf("mode refused with text: %d %s", res.Status, res.Body)
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
	// a body with none of the settings is refused, not stored as an empty
	// configuration (BUGS C6)
	for _, path := range []string{"/api/settings/ldap", "/api/settings/oidc"} {
		for _, body := range []string{`{}`, `{"other":1}`, `[]`} {
			if res := admin.Do("PUT", path, body); res.Status != http.StatusBadRequest || res.Body != `{"success":false,"error":"No settings to save"}` || len(s.API.Calls()) != 0 {
				t.Errorf("%s %s: %d %s", path, body, res.Status, res.Body)
			}
		}
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
		`GET /api/config/client_session_expiry_days`,
		`GET /api/config/client_session_sliding`,
		`GET /api/config/client_session_max_days`,
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
	if calls := s.API.Calls(); len(calls) != 6 || calls[4] != `PUT /api/config/client_session_sliding {"value":"false"}` {
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
	if res := admin.Do("PUT", "/api/settings/client-sessions", `{"sliding":true}`); res.Body != `{"success":true}` {
		t.Errorf("save with a .env that is a folder: %s", res.Body)
	}
	if calls := s.API.Calls(); len(calls) != 6 || calls[4] != `PUT /api/config/client_session_sliding {"value":"true"}` {
		t.Errorf("sliding missing: %v", calls)
	}
	// a refused value stops the save and puts back the values the server
	// took, as they were (BUGS SE6)
	s.API.On("GET /api/config/client_session_expiry_days", http.StatusOK, `{"value":"30"}`)
	s.API.On("PUT /api/config/client_session_sliding", http.StatusInternalServerError, `{"error":"Go server down"}`)
	s.API.Calls()
	res = admin.Do("PUT", "/api/settings/client-sessions", `{"max_days":30}`)
	if res.Status != http.StatusInternalServerError || res.Body != `{"success":false,"error":"Go server down"}` {
		t.Errorf("refused: %d %s", res.Status, res.Body)
	}
	want = []string{
		`GET /api/config/client_session_expiry_days`,
		`GET /api/config/client_session_sliding`,
		`GET /api/config/client_session_max_days`,
		`PUT /api/config/client_session_expiry_days {"value":"7"}`,
		`PUT /api/config/client_session_sliding {"value":"true"}`,
		`PUT /api/config/client_session_expiry_days {"value":"30"}`,
	}
	if calls := s.API.Calls(); !reflect.DeepEqual(calls, want) {
		t.Errorf("calls of a refused save: %v", calls)
	}
	// a body with none of the values is refused, not saved as the
	// defaults (BUGS C6)
	if res := admin.Do("PUT", "/api/settings/client-sessions", `{}`); res.Status != http.StatusBadRequest || res.Body != `{"success":false,"error":"No settings to save"}` || len(s.API.Calls()) != 0 {
		t.Errorf("empty body: %d %s", res.Status, res.Body)
	}
}

func TestConnectionModeAndPublicEndpoints(t *testing.T) {
	s := newSite(t, nil, nil, nil)
	admin := s.As("admin")
	// the running settings come from /api/health/detail, since /api/health
	// answers status and version only (BUGS UM1)
	s.API.On("GET /api/health", http.StatusOK, `{"status":"ok","version":"1"}`)
	s.API.On("GET /api/health/detail", http.StatusOK, `{"status":"ok","connection":{"p2p_first":false}}`)
	// Without a systemd unit or a compose file (a container, a Windows
	// service) the settings are what the server runs with, and they are
	// saved through it (L375, L376)
	s.API.On("GET /api/connection/config", http.StatusOK, `{"mode":"relay_only","p2p_fallback_ms":5000,"same_nat_relay":true,`+
		`"allow_shared_nat_initiator":false,"logged_in_only_initiator":true,"operator_only_outbound":false}`)
	res := admin.Do("GET", "/api/settings/connection-mode", "")
	if res.Body != `{"success":true,"data":{"mode":"relay_only","p2p_fallback_ms":5000,"same_nat_relay":true,"allow_shared_nat_initiator":false,"logged_in_only_initiator":true,"operator_only_outbound":false,"source":"go-api","writable":true,"deployment":"defaults","runtime":{"p2p_first":false}}}` {
		t.Errorf("get: %s", res.Body)
	}
	s.API.On("GET /api/health/detail", http.StatusOK, `{"status":"ok","connection":""}`)
	if res := admin.Do("GET", "/api/settings/connection-mode", ""); !strings.HasSuffix(res.Body, `"runtime":null}}`) {
		t.Errorf("get without runtime: %s", res.Body)
	}
	s.API.On("GET /api/health/detail", http.StatusInternalServerError, `{"error":"x"}`)
	if res := admin.Do("GET", "/api/settings/connection-mode", ""); !strings.HasSuffix(res.Body, `"runtime":null}}`) {
		t.Errorf("get without the detail: %s", res.Body)
	}
	s.API.On("PUT /api/connection/config", http.StatusOK, `{"config":{"mode":"relay_only","p2p_fallback_ms":3000,"same_nat_relay":true,`+
		`"allow_shared_nat_initiator":false,"logged_in_only_initiator":true,"operator_only_outbound":false}}`)
	s.API.Calls()
	res = admin.Do("PUT", "/api/settings/connection-mode", `{"mode":"relay_only","p2p_fallback_ms":3000,"logged_in_only_initiator":true}`)
	if res.Status != http.StatusOK || !strings.Contains(res.Body, `"source":"go-api"`) || !strings.Contains(res.Body, `"restartRequired":null`) {
		t.Errorf("save through the server: %d %s", res.Status, res.Body)
	}
	if calls := s.API.Calls(); len(calls) != 2 || !strings.HasPrefix(calls[1], `PUT /api/connection/config {"mode":"relay_only","p2p_fallback_ms":3000,`) {
		t.Errorf("save through the server: calls %v", calls)
	}
	// A fallback time the server can't take is refused with what is wrong,
	// not turned into 2000 (L418)
	before := len(s.AuditRows())
	for _, body := range []string{`{"mode":"p2p_first","p2p_fallback_ms":99999}`, `{"p2p_fallback_ms":-1}`, `{"p2p_fallback_ms":2.5}`,
		`{"p2p_fallback_ms":"abc"}`, `{"p2p_fallback_ms":""}`, `{"p2p_fallback_ms":null}`} {
		res = admin.Do("PUT", "/api/settings/connection-mode", body)
		if res.Status != http.StatusBadRequest || res.Body != `{"success":false,"error":"settings.general.conn_fallback_error"}` {
			t.Errorf("%s: %d %s", body, res.Status, res.Body)
		}
	}
	if len(s.AuditRows()) != before || len(s.API.Calls()) != 0 {
		t.Error("a refused save was audited or sent")
	}
	// When the server doesn't answer the page says so instead of showing
	// defaults as what it runs with (L376)
	s.API.On("GET /api/connection/config", http.StatusBadGateway, `{"error":"down"}`)
	if res := admin.Do("GET", "/api/settings/connection-mode", ""); res.Status != http.StatusInternalServerError {
		t.Errorf("get without the server: %d %s", res.Status, res.Body)
	}

	if res := admin.Do("GET", "/api/settings/public-endpoints", ""); res.Body != `{"success":true,"data":{"public_server_id":"","public_relay_server":"","public_api_url":"","env_override_active":false}}` {
		t.Errorf("get endpoints: %s", res.Body)
	}
	for _, c := range []struct{ body, want string }{
		{`{"public_server_id":"bad host!"}`, `{"success":false,"error":"settings.public_endpoints_invalid_host"}`},
		{`{"public_relay_server":"relay.example.org\nX=1"}`, `{"success":false,"error":"settings.public_endpoints_invalid_host"}`},
		{`{"public_api_url":"ftp://api.example.org"}`, `{"success":false,"error":"settings.public_endpoints_invalid_api_url"}`},
		// a body with none of the addresses is refused (BUGS C6)
		{`{}`, `{"success":false,"error":"No settings to save"}`},
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
		// The limit is held between 1 and 500 (audit M5): a negative one reads
		// one row, and one beyond every integer type no longer fails
		if got, body := count("?limit=-1"); got != 1 {
			t.Errorf("negative limit: %d %.200s", got, body)
		}
		if got, body := count("?limit=99999999999999999999"); got != 105 {
			t.Errorf("limit beyond 64 bits: %d %.200s", got, body)
		}
		if got, body := count("?limit=0x10"); got != 100 {
			t.Errorf("hexadecimal limit: %d %.200s", got, body)
		}
		_, body := count("?limit=1")
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

// Changing the enrollment mode asks enrollment.manage besides
// server.config, the permission the Device tokens page asks: an account
// whose enrollment.manage is taken away reads the mode but cannot change
// it (DECISIONS A170, A129).
func TestEnrollmentModeChangeAsksEnrollmentManage(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		s := newSite(t, db, routetest.Overrides{"server_admin": {"enrollment.manage": false}}, nil)
		s.API.On("GET /api/enrollment/mode", http.StatusOK, `{"mode":"managed"}`)
		s.API.On("GET /api/enrollment/pending", http.StatusOK, `{"devices":[],"count":0}`)
		s.API.On("PUT /api/enrollment/mode", http.StatusOK, `{"status":"ok"}`)
		limited := s.As("server_admin")
		if res := limited.Do("GET", "/api/settings/enrollment", ""); res.Status != http.StatusOK {
			t.Errorf("read without enrollment.manage: %d %s", res.Status, res.Body)
		}
		s.API.Calls()
		if res := limited.Do("PUT", "/api/settings/enrollment", `{"mode":"open"}`); res.Status != http.StatusForbidden {
			t.Errorf("change without enrollment.manage: %d %s, want 403", res.Status, res.Body)
		}
		for _, call := range s.API.Calls() {
			if strings.HasPrefix(call, "PUT ") {
				t.Errorf("the refused change reached the API server: %s", call)
			}
		}
		if res := s.As("admin").Do("PUT", "/api/settings/enrollment", `{"mode":"open"}`); res.Status != http.StatusOK {
			t.Errorf("change as admin: %d %s", res.Status, res.Body)
		}
	})
}
