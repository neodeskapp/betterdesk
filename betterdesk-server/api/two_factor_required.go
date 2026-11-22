package api

import (
	"net/http"

	"github.com/unitronix/betterdesk-server/db"
)

// configRequireTwoFactor is the server_config key of the switch that makes
// two-factor sign-in mandatory for local accounts (Settings, Sign-in,
// "Require two-factor sign-in" in the web console). Directory and single
// sign-on accounts are left to their provider.
const configRequireTwoFactor = "require_two_factor"

// twoFactorSetupDue reports whether the account may not sign in here yet:
// the switch is on and it is a local account without two-factor sign-in.
// It sets it up at its next password sign-in to the web console; this API
// and the desktop client have no step for that.
func (s *Server) twoFactorSetupDue(user *db.User) bool {
	if user == nil || user.TOTPEnabled || s.db == nil {
		return false
	}
	if provider := user.AuthProvider; provider != "" && provider != db.AuthProviderLocal {
		return false
	}
	value, err := s.db.GetConfig(configRequireTwoFactor)
	return err == nil && value == "true"
}

// writeTwoFactorSetupDue answers a sign-in that twoFactorSetupDue refuses.
func writeTwoFactorSetupDue(w http.ResponseWriter) {
	writeJSON(w, http.StatusForbidden, map[string]string{
		"error": "Two-factor sign-in is required for this account. Sign in to the web console once to set it up.",
		"code":  "totp_setup_required",
	})
}
