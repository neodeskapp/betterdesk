// LDAP configuration API handlers for the NeoDesk server.
// Endpoints for managing LDAP/AD authentication provider settings.
package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
)

// ldapConfigKeys are the server_config keys used to store LDAP settings.
var ldapConfigKeys = []string{
	"ldap.enabled",
	"ldap.host",
	"ldap.port",
	"ldap.use_tls",
	"ldap.start_tls",
	"ldap.skip_tls_verify",
	"ldap.bind_dn",
	"ldap.bind_password",
	"ldap.base_dn",
	"ldap.user_filter",
	"ldap.user_attr_id",
	"ldap.user_attr_email",
	"ldap.user_attr_name",
	"ldap.group_base_dn",
	"ldap.group_filter",
	"ldap.group_attr_name",
	"ldap.default_role",
	"ldap.group_role_map",
	"ldap.direct_bind",
	"ldap.direct_bind_dn",
	"ldap.conn_timeout_sec",
	"ldap.allow_signup",
}

// loadLDAPConfigFromDB reads LDAP configuration from the server_config table.
func (s *Server) loadLDAPConfigFromDB() *auth.LDAPConfig {
	cfg := &auth.LDAPConfig{
		Port:           389,
		UserFilter:     "(sAMAccountName={{username}})",
		UserAttrID:     "sAMAccountName",
		UserAttrEmail:  "mail",
		UserAttrName:   "displayName",
		GroupFilter:    "(member={{dn}})",
		GroupAttrName:  "cn",
		DefaultRole:    "viewer",
		ConnTimeoutSec: 10,
		AllowSignup:    true,
	}

	getString := func(key string) string {
		v, err := s.db.GetConfig(key)
		if err != nil || v == "" {
			return ""
		}
		return v
	}
	getBool := func(key string) bool {
		return getString(key) == "true"
	}
	getInt := func(key, fallback string) int {
		v := getString(key)
		if v == "" {
			v = fallback
		}
		n, _ := strconv.Atoi(v)
		return n
	}

	cfg.Enabled = getBool("ldap.enabled")
	if v := getString("ldap.host"); v != "" {
		cfg.Host = v
	}
	cfg.Port = getInt("ldap.port", "389")
	cfg.UseTLS = getBool("ldap.use_tls")
	cfg.StartTLS = getBool("ldap.start_tls")
	cfg.SkipTLSVerify = getBool("ldap.skip_tls_verify")
	if v := getString("ldap.bind_dn"); v != "" {
		cfg.BindDN = v
	}
	if v := getString("ldap.bind_password"); v != "" {
		cfg.BindPassword = v
	}
	if v := getString("ldap.base_dn"); v != "" {
		cfg.BaseDN = v
	}
	if v := getString("ldap.user_filter"); v != "" {
		cfg.UserFilter = v
	}
	if v := getString("ldap.user_attr_id"); v != "" {
		cfg.UserAttrID = v
	}
	if v := getString("ldap.user_attr_email"); v != "" {
		cfg.UserAttrEmail = v
	}
	if v := getString("ldap.user_attr_name"); v != "" {
		cfg.UserAttrName = v
	}
	if v := getString("ldap.group_base_dn"); v != "" {
		cfg.GroupBaseDN = v
	}
	if v := getString("ldap.group_filter"); v != "" {
		cfg.GroupFilter = v
	}
	if v := getString("ldap.group_attr_name"); v != "" {
		cfg.GroupAttrName = v
	}
	if v := getString("ldap.default_role"); v != "" {
		cfg.DefaultRole = v
	}
	if v := getString("ldap.group_role_map"); v != "" {
		cfg.GroupRoleMap = v
	}
	cfg.DirectBind = getBool("ldap.direct_bind")
	if v := getString("ldap.direct_bind_dn"); v != "" {
		cfg.DirectBindDN = v
	}
	if n := getInt("ldap.conn_timeout_sec", "10"); n > 0 {
		cfg.ConnTimeoutSec = n
	}
	// A directory that was never saved with the switch creates accounts
	if v := getString("ldap.allow_signup"); v != "" {
		cfg.AllowSignup = v == "true"
	}

	return cfg
}

// saveLDAPConfigToDB writes LDAP configuration to the server_config table.
func (s *Server) saveLDAPConfigToDB(cfg *auth.LDAPConfig) error {
	setString := func(key, val string) error {
		return s.db.SetConfig(key, val)
	}
	setBool := func(key string, val bool) error {
		v := "false"
		if val {
			v = "true"
		}
		return s.db.SetConfig(key, v)
	}
	setInt := func(key string, val int) error {
		return s.db.SetConfig(key, strconv.Itoa(val))
	}

	if err := setBool("ldap.enabled", cfg.Enabled); err != nil {
		return err
	}
	if err := setString("ldap.host", cfg.Host); err != nil {
		return err
	}
	if err := setInt("ldap.port", cfg.Port); err != nil {
		return err
	}
	if err := setBool("ldap.use_tls", cfg.UseTLS); err != nil {
		return err
	}
	if err := setBool("ldap.start_tls", cfg.StartTLS); err != nil {
		return err
	}
	if err := setBool("ldap.skip_tls_verify", cfg.SkipTLSVerify); err != nil {
		return err
	}
	if err := setString("ldap.bind_dn", cfg.BindDN); err != nil {
		return err
	}
	if err := setString("ldap.bind_password", cfg.BindPassword); err != nil {
		return err
	}
	if err := setString("ldap.base_dn", cfg.BaseDN); err != nil {
		return err
	}
	if err := setString("ldap.user_filter", cfg.UserFilter); err != nil {
		return err
	}
	if err := setString("ldap.user_attr_id", cfg.UserAttrID); err != nil {
		return err
	}
	if err := setString("ldap.user_attr_email", cfg.UserAttrEmail); err != nil {
		return err
	}
	if err := setString("ldap.user_attr_name", cfg.UserAttrName); err != nil {
		return err
	}
	if err := setString("ldap.group_base_dn", cfg.GroupBaseDN); err != nil {
		return err
	}
	if err := setString("ldap.group_filter", cfg.GroupFilter); err != nil {
		return err
	}
	if err := setString("ldap.group_attr_name", cfg.GroupAttrName); err != nil {
		return err
	}
	if err := setString("ldap.default_role", cfg.DefaultRole); err != nil {
		return err
	}
	if err := setString("ldap.group_role_map", cfg.GroupRoleMap); err != nil {
		return err
	}
	if err := setBool("ldap.direct_bind", cfg.DirectBind); err != nil {
		return err
	}
	if err := setString("ldap.direct_bind_dn", cfg.DirectBindDN); err != nil {
		return err
	}
	if err := setInt("ldap.conn_timeout_sec", cfg.ConnTimeoutSec); err != nil {
		return err
	}
	if err := setBool("ldap.allow_signup", cfg.AllowSignup); err != nil {
		return err
	}

	return nil
}

// handleGetLDAPConfig returns the current LDAP configuration (password masked).
// GET /api/auth/ldap/config
func (s *Server) handleGetLDAPConfig(w http.ResponseWriter, r *http.Request) {
	cfg := s.loadLDAPConfigFromDB()

	// Mask bind password in response
	maskedCfg := *cfg
	if maskedCfg.BindPassword != "" {
		maskedCfg.BindPassword = "••••••••"
	}

	writeJSON(w, http.StatusOK, maskedCfg)
}

// handleSaveLDAPConfig saves the LDAP configuration.
// PUT /api/auth/ldap/config
func (s *Server) handleSaveLDAPConfig(w http.ResponseWriter, r *http.Request) {
	var raw json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON"})
		return
	}
	var cfg auth.LDAPConfig
	// sent.BindPassword is nil when the body has no bind_password, and
	// sent.AllowSignup when it has no allow_signup
	var sent struct {
		BindPassword *string `json:"bind_password"`
		AllowSignup  *bool   `json:"allow_signup"`
	}
	if json.Unmarshal(raw, &cfg) != nil || json.Unmarshal(raw, &sent) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON"})
		return
	}

	// A directory that is on must have settings a sign-in can use: a
	// broken filter or role map line is refused with its field, not saved
	// to fail every sign-in later
	if cfg.Enabled {
		if err := auth.CheckLDAPConfig(&cfg, false); err != nil {
			writeSettingError(w, http.StatusBadRequest, err)
			return
		}
	}

	// The masked placeholder, or no password at all, keeps the current
	// password; an empty one clears it
	if cfg.BindPassword == "••••••••" || sent.BindPassword == nil {
		currentCfg := s.loadLDAPConfigFromDB()
		cfg.BindPassword = currentCfg.BindPassword
	}
	// A body without the Auto-create switch keeps it as it is
	if sent.AllowSignup == nil {
		cfg.AllowSignup = s.loadLDAPConfigFromDB().AllowSignup
	}

	// Set defaults
	if cfg.Port == 0 {
		if cfg.UseTLS {
			cfg.Port = 636
		} else {
			cfg.Port = 389
		}
	}
	if cfg.ConnTimeoutSec <= 0 {
		cfg.ConnTimeoutSec = 10
	}

	if err := s.saveLDAPConfigToDB(&cfg); err != nil {
		log.Printf("[LDAP] Failed to save config: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to save configuration"})
		return
	}

	// The provider takes the settings as a restart loads them, with the
	// defaults for empty fields: an empty Group filter is the default
	// filter now and after a restart alike
	if s.ldapProvider != nil {
		s.ldapProvider.UpdateConfig(s.loadLDAPConfigFromDB())
	}

	log.Printf("[LDAP] Configuration saved (enabled=%v, host=%s, port=%d)", cfg.Enabled, cfg.Host, cfg.Port)

	if s.auditLog != nil {
		s.auditLog.Log("ldap_config_updated", s.eventActor(r), getUsernameFromCtx(r), map[string]string{
			"enabled": strconv.FormatBool(cfg.Enabled),
			"host":    cfg.Host,
			"port":    strconv.Itoa(cfg.Port),
		})
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// writeSettingError answers a refused directory or single sign-on
// setting: its reason and its field.
func writeSettingError(w http.ResponseWriter, status int, err *auth.SettingError) {
	writeJSON(w, status, map[string]string{"error": err.Error(), "field": err.Field})
}

// handleLDAPVerifyCredentials checks username/password against LDAP only.
// Used by the Node.js console to detect local/SSO username collisions when a
// local account already exists but the user signs in with IdP credentials.
// POST /api/auth/ldap/verify
func (s *Server) handleLDAPVerifyCredentials(w http.ResponseWriter, r *http.Request) {
	clientIP := s.signInIP(r)
	if s.signInBlocked(clientIP) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{
			"error": "Too many login attempts. Please try again later.",
		})
		return
	}

	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON"})
		return
	}
	if body.Username == "" || body.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Username and password required"})
		return
	}

	userKey := "user:" + strings.ToLower(body.Username)
	if s.signInBlocked(userKey) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{
			"error": "Too many login attempts. Please try again later.",
		})
		return
	}

	// Every check but one the directory accepts counts against the address
	if s.ldapProvider == nil || !s.ldapProvider.IsEnabled() {
		s.countFailedSignIn(clientIP, userKey)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "LDAP is not enabled"})
		return
	}

	dirCtx, cancel := directoryContext(r.Context())
	defer cancel()
	result, err := s.ldapAuthenticate(dirCtx, body.Username, body.Password)
	if errors.Is(err, errDirectoryTimeout) {
		writeDirectoryTimeout(w)
		return
	}
	if err != nil {
		log.Printf("[LDAP] Verify error for %s: %v", body.Username, err)
		s.countFailedSignIn(clientIP, userKey)
		writeJSON(w, http.StatusOK, map[string]any{"authenticated": false})
		return
	}

	authenticated := result != nil && result.Authenticated
	if !authenticated {
		s.countFailedSignIn(clientIP, userKey)
	}
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": authenticated})
}

// handleTestLDAPConnection tests the LDAP connection with current or provided config.
// POST /api/auth/ldap/test
func (s *Server) handleTestLDAPConnection(w http.ResponseWriter, r *http.Request) {
	var cfg auth.LDAPConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON"})
		return
	}

	// A masked or empty password stands for the stored one, but only for the
	// stored host and bind DN: a test against another host or account must
	// bring its own password, or the stored one would go to that host.
	if cfg.BindPassword == "••••••••" || cfg.BindPassword == "" {
		cfg.BindPassword = ""
		stored := s.loadLDAPConfigFromDB()
		if strings.EqualFold(strings.TrimSpace(cfg.Host), strings.TrimSpace(stored.Host)) &&
			strings.TrimSpace(cfg.BindDN) == strings.TrimSpace(stored.BindDN) {
			cfg.BindPassword = stored.BindPassword
		}
	}

	// The test refuses what a save would refuse before it connects, so a
	// broken filter never tests as successful
	if err := auth.CheckLDAPConfig(&cfg, true); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": err.Error(), "field": err.Field})
		return
	}

	if cfg.Port == 0 {
		if cfg.UseTLS {
			cfg.Port = 636
		} else {
			cfg.Port = 389
		}
	}
	if cfg.ConnTimeoutSec <= 0 {
		cfg.ConnTimeoutSec = 10
	}

	provider := auth.NewLDAPProvider(&cfg)
	// Force enabled for test
	testCfg := cfg
	testCfg.Enabled = true
	provider.UpdateConfig(&testCfg)

	if err := provider.TestConnection(); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "Connection successful",
	})
}
