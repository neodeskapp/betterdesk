package settings_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// source is one entry of GET /api/panel/settings/sources.
type source struct {
	ValueSource string `json:"value_source"`
	EnvVar      string `json:"env_var"`
	Locked      bool   `json:"locked"`
	Default     any    `json:"default"`
	HasDefault  bool   `json:"-"`
}

func readSources(t *testing.T, s *site) map[string]source {
	t.Helper()
	res := s.As("admin").Do("GET", "/api/panel/settings/sources", "")
	var answer struct {
		Success bool                       `json:"success"`
		Data    map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.Body), &answer); res.Status != http.StatusOK || err != nil || !answer.Success {
		t.Fatalf("sources: %d %s", res.Status, res.Body)
	}
	out := map[string]source{}
	for key, raw := range answer.Data {
		var src source
		if err := json.Unmarshal(raw, &src); err != nil {
			t.Fatalf("%s: %s", key, raw)
		}
		var fields map[string]any
		_ = json.Unmarshal(raw, &fields)
		_, src.HasDefault = fields["default"]
		out[key] = src
	}
	return out
}

func check(t *testing.T, got map[string]source, key string, want source) {
	t.Helper()
	g, ok := got[key]
	if !ok {
		t.Errorf("%s: not in the answer", key)
		return
	}
	want.HasDefault = want.Default != nil
	if !reflect.DeepEqual(g, want) {
		t.Errorf("%s: %+v, want %+v", key, g, want)
	}
}

// The API server's configuration answers nothing saved
func nothingSaved(s *site) {
	for _, key := range []string{"client_session_expiry_days", "client_session_sliding", "client_session_max_days",
		"availability_history_enabled", "availability_history_retention_days", "require_two_factor"} {
		s.API.On("GET /api/config/"+key, http.StatusOK, `{"key":"`+key+`","value":""}`)
	}
	s.API.On("GET /api/connection/config", http.StatusOK, `{"mode":"p2p_first"}`)
}

// Nothing is set: every setting the server has a built-in default for
// says so, with the default it applies, and nothing is locked.
func TestSourcesWithNothingSet(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		s := newSite(t, db, nil, nil)
		nothingSaved(s)
		s.API.On("GET /api/enrollment/mode", http.StatusOK, `{"mode":"open","env_override":false,"source":"configuration"}`)
		got := readSources(t, s)

		check(t, got, "enrollment.mode", source{ValueSource: "default", EnvVar: "ENROLLMENT_MODE", Default: "open"})
		check(t, got, "device_scope.mode", source{ValueSource: "default", EnvVar: "DEVICE_SCOPE_DEFAULT", Default: "open"})
		check(t, got, "client_sessions.expiry_days", source{ValueSource: "default", EnvVar: "API_TOKEN_EXPIRY_DAYS", Default: float64(7)})
		check(t, got, "client_sessions.sliding", source{ValueSource: "default", Default: true})
		check(t, got, "client_sessions.max_days", source{ValueSource: "default", Default: float64(30)})
		check(t, got, "availability_history.enabled", source{ValueSource: "default", Default: false})
		check(t, got, "availability_history.retention_days", source{ValueSource: "default", Default: float64(30)})
		check(t, got, "two_factor.required", source{ValueSource: "default", Default: false})
		check(t, got, "console_sessions.single_session", source{ValueSource: "default", EnvVar: "SESSION_MAX_PER_USER", Default: false})
		check(t, got, "public_endpoints.public_server_id", source{ValueSource: "default", EnvVar: "PUBLIC_SERVER_ID"})
		check(t, got, "smtp.host", source{ValueSource: "default", EnvVar: "SMTP_HOST"})
		check(t, got, "smtp.port", source{ValueSource: "default", EnvVar: "SMTP_PORT", Default: float64(587)})
		check(t, got, "smtp.from", source{ValueSource: "default", EnvVar: "SMTP_FROM", Default: "neodesk@localhost"})
		check(t, got, "clock.ntp_servers", source{ValueSource: "default", EnvVar: "NTP_SERVERS", Default: "pool.ntp.org,time.google.com,time.cloudflare.com"})
		check(t, got, "clock.max_skew_ms", source{ValueSource: "default", EnvVar: "BILLING_MAX_CLOCK_SKEW_MS", Default: float64(2000)})
		check(t, got, "connection_mode.mode", source{ValueSource: "stored", Default: "p2p_first"})
		check(t, got, "connection_mode.p2p_fallback_ms", source{ValueSource: "stored", Default: float64(2000)})
	})
}

// What the environment sets is named with its variable; where the
// environment wins the console can't change it (locked). Device scope was
// "open" on the page while DEVICE_SCOPE_DEFAULT=restricted hid the
// devices: the page now reads the value in force.
func TestSourcesFromTheEnvironment(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		s := newSite(t, db, nil, map[string]string{
			"DEVICE_SCOPE_DEFAULT":          "restricted",
			"PUBLIC_ENDPOINTS_ENV_OVERRIDE": "Y",
			"PUBLIC_SERVER_ID":              "id.example.org",
			"PUBLIC_RELAY_SERVER":           "relay.example.org",
			"SESSION_MAX_PER_USER":          "3",
			"SMTP_HOST":                     "mail.example.org",
			"API_TOKEN_EXPIRY_DAYS":         "9",
		})
		nothingSaved(s)
		s.API.On("GET /api/config/client_session_max_days", http.StatusOK, `{"key":"client_session_max_days","value":"60"}`)
		s.API.On("GET /api/enrollment/mode", http.StatusOK, `{"mode":"locked","env_override":true,"source":"environment"}`)
		if err := os.WriteFile(filepath.Join(s.Console, ".env"), []byte("NTP_SERVERS=ntp.example.org\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		got := readSources(t, s)

		check(t, got, "enrollment.mode", source{ValueSource: "environment", EnvVar: "ENROLLMENT_MODE", Locked: true, Default: "open"})
		check(t, got, "device_scope.mode", source{ValueSource: "environment", EnvVar: "DEVICE_SCOPE_DEFAULT", Default: "open"})
		check(t, got, "public_endpoints.public_server_id", source{ValueSource: "environment", EnvVar: "PUBLIC_SERVER_ID", Locked: true})
		check(t, got, "public_endpoints.public_relay_server", source{ValueSource: "environment", EnvVar: "PUBLIC_RELAY_SERVER", Locked: true})
		check(t, got, "public_endpoints.public_api_url", source{ValueSource: "default", EnvVar: "PUBLIC_API_URL"})
		check(t, got, "console_sessions.single_session", source{ValueSource: "environment", EnvVar: "SESSION_MAX_PER_USER", Locked: true, Default: false})
		check(t, got, "smtp.host", source{ValueSource: "environment", EnvVar: "SMTP_HOST"})
		check(t, got, "client_sessions.expiry_days", source{ValueSource: "environment", EnvVar: "API_TOKEN_EXPIRY_DAYS", Default: float64(7)})
		check(t, got, "client_sessions.max_days", source{ValueSource: "stored", Default: float64(30)})
		check(t, got, "clock.ntp_servers", source{ValueSource: "environment", EnvVar: "NTP_SERVERS", Default: "pool.ntp.org,time.google.com,time.cloudflare.com"})

		if res := s.As("admin").Do("GET", "/api/settings/device-scope", ""); res.Body != `{"success":true,"data":{"mode":"restricted"}}` {
			t.Errorf("device scope in force: %s", res.Body)
		}
	})
}

// A value saved in the console is stored, also over an environment that
// is only a first default.
func TestSourcesStoredInTheConsole(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		s := newSite(t, db, nil, map[string]string{"DEVICE_SCOPE_DEFAULT": "restricted", "SMTP_HOST": "env.example.org"})
		nothingSaved(s)
		s.API.On("GET /api/enrollment/mode", http.StatusOK, `{"mode":"managed","env_override":false,"source":"database"}`)
		exec(t, db, "INSERT INTO settings (key, value) VALUES ('device_scope_default', 'open'), ('smtp_config', '{\"host\":\"stored.example.org\",\"port\":25}')")
		got := readSources(t, s)

		check(t, got, "enrollment.mode", source{ValueSource: "stored", EnvVar: "ENROLLMENT_MODE", Default: "open"})
		check(t, got, "device_scope.mode", source{ValueSource: "stored", EnvVar: "DEVICE_SCOPE_DEFAULT", Default: "open"})
		check(t, got, "smtp.host", source{ValueSource: "stored", EnvVar: "SMTP_HOST"})
		check(t, got, "smtp.port", source{ValueSource: "stored", EnvVar: "SMTP_PORT", Default: float64(587)})
	})
}

// Only server.config reads the sources.
func TestSourcesNeedServerConfig(t *testing.T) {
	s := newSite(t, nil, nil, nil)
	for _, role := range []string{"viewer", "operator"} {
		if res := s.As(role).Do("GET", "/api/panel/settings/sources", ""); res.Status != http.StatusForbidden {
			t.Errorf("%s: %d %s", role, res.Status, res.Body)
		}
	}
	if res := s.SignedOut().Do("GET", "/api/panel/settings/sources", ""); res.Status != http.StatusUnauthorized {
		t.Errorf("signed out: %d %s", res.Status, res.Body)
	}
}
