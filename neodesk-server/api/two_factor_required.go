package api

import (
	"net/http"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

// configRequireTwoFactor is the server_config key of the switch that makes
// two-factor sign-in mandatory for local accounts (Settings, Sign-in,
// "Require two-factor sign-in" in the web console). Directory and single
// sign-on accounts are left to their provider. Pro license accounts are
// left out too: the web console, where the set-up happens, refuses them.
const configRequireTwoFactor = "require_two_factor"

// twoFactorSetupDue reports whether the account may not sign in here yet:
// the switch is on and it is a local account without two-factor sign-in.
// It sets it up at its next password sign-in to the web console; this API
// and the desktop client have no step for that.
func (s *Server) twoFactorSetupDue(user *db.User) bool {
	if user == nil || user.TOTPEnabled || s.db == nil || auth.IsProRole(user.Role) {
		return false
	}
	if provider := user.AuthProvider; provider != "" && provider != db.AuthProviderLocal {
		return false
	}
	// The web console's admins can require it of one account
	if rules, err := s.db.AccountSignInRules(user.ID); err == nil && rules.TwoFactorRequired {
		return true
	}
	value, err := s.db.GetConfig(configRequireTwoFactor)
	return err == nil && value == "true"
}

// passwordChangeDue reports whether the account may not sign in here yet:
// an admin of the web console required a new password of the local
// account (or gave it a temporary one), which it chooses at its next
// sign-in to the web console.
func (s *Server) passwordChangeDue(user *db.User) bool {
	if user == nil || s.db == nil {
		return false
	}
	if provider := user.AuthProvider; provider != "" && provider != db.AuthProviderLocal {
		return false
	}
	rules, err := s.db.AccountSignInRules(user.ID)
	return err == nil && rules.MustChangePassword
}

// writePasswordChangeDue answers a sign-in that passwordChangeDue refuses.
func writePasswordChangeDue(w http.ResponseWriter) {
	writeJSON(w, http.StatusForbidden, map[string]string{
		"error": "This account must choose a new password. Sign in to the web console once to set it.",
		"code":  "password_change_required",
	})
}

// writeTwoFactorSetupDue answers a sign-in that twoFactorSetupDue refuses.
func writeTwoFactorSetupDue(w http.ResponseWriter) {
	writeJSON(w, http.StatusForbidden, map[string]string{
		"error": "Two-factor sign-in is required for this account. Sign in to the web console once to set it up.",
		"code":  "totp_setup_required",
	})
}
