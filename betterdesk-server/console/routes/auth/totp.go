package auth

import (
	"errors"
	"net/http"

	"github.com/unitronix/betterdesk-server/console/authn"
	"github.com/unitronix/betterdesk-server/console/kit"
)

// totpCodeLength is the number of digits of a two-factor code.
const totpCodeLength = 6

type totpSetupAnswer struct {
	Success bool `json:"success"`
	*authn.TOTPSetup
}

// totpSetup answers POST /api/auth/totp/setup: a new secret for the
// signed-in account with its QR code. The secret is stored at once and
// replaces one from an unfinished set-up; two-factor sign-in stays off
// until totpEnable.
func (h *handlers) totpSetup(w http.ResponseWriter, r *http.Request) {
	userID := kit.UserID(r)
	enabled, err := h.authn.IsTOTPEnabled(r.Context(), userID)
	if err != nil {
		h.serverError(w, r, "TOTP setup error:", err)
		return
	}
	if enabled {
		kit.Fail(w, r, http.StatusBadRequest, kit.T(r, "auth.totp_already_enabled", nil))
		return
	}
	setup, err := h.authn.GenerateTOTPSetup(r.Context(), userID)
	var refusal authn.Refusal
	switch {
	case errors.As(err, &refusal):
		refuse(w, r, refusal)
	case err != nil:
		h.serverError(w, r, "TOTP setup error:", err)
	default:
		kit.JSON(w, r, http.StatusOK, totpSetupAnswer{Success: true, TOTPSetup: setup})
	}
}

type totpEnabledAnswer struct {
	Success       bool     `json:"success"`
	RecoveryCodes []string `json:"recoveryCodes"`
}

// totpEnable answers POST /api/auth/totp/enable with the body {code}: the
// current code of the secret from totpSetup turns two-factor sign-in on.
// The answer holds the recovery codes, which are shown this once.
func (h *handlers) totpEnable(w http.ResponseWriter, r *http.Request) {
	code := kit.Body(r).Get("code")
	if !code.Truthy() || !hasLength(code, totpCodeLength) {
		kit.Fail(w, r, http.StatusBadRequest, kit.T(r, "auth.totp_invalid_code", nil))
		return
	}
	// A list of six items passes the check above and is never the code:
	// the empty text stands in for it
	text, _ := code.Str()
	codes, err := h.authn.VerifyAndEnableTOTP(r.Context(), kit.UserID(r), text)
	var refusal authn.Refusal
	switch {
	case errors.As(err, &refusal):
		refuse(w, r, refusal)
		return
	case err == nil:
		err = h.audit(r, kit.UserID(r), "totp_enabled", "2FA enabled")
	}
	if err != nil {
		h.serverError(w, r, "TOTP enable error:", err)
		return
	}
	kit.JSON(w, r, http.StatusOK, totpEnabledAnswer{Success: true, RecoveryCodes: codes})
}

// totpDisable answers POST /api/auth/totp/disable with the body
// {password}: with the account's password it turns two-factor sign-in off
// and deletes the secret and the recovery codes.
func (h *handlers) totpDisable(w http.ResponseWriter, r *http.Request) {
	password := kit.Body(r).Get("password")
	if !password.Truthy() {
		kit.Fail(w, r, http.StatusBadRequest, kit.T(r, "auth.password_required", nil))
		return
	}
	userID := kit.UserID(r)
	user, err := h.authn.UserByID(r.Context(), userID)
	if err != nil {
		h.serverError(w, r, "TOTP disable error:", err)
		return
	}
	if user == nil {
		kit.Fail(w, r, http.StatusNotFound, kit.T(r, "users.not_found", nil))
		return
	}
	// A password that is not text was a fault of the hash compare
	text, isText := password.Str()
	if !isText {
		h.serverError(w, r, "TOTP disable error:", errNotText)
		return
	}
	valid, err := h.authn.VerifyPassword(r.Context(), text, user.PasswordHash.String)
	if err != nil {
		h.serverError(w, r, "TOTP disable error:", err)
		return
	}
	if !valid {
		kit.Fail(w, r, http.StatusUnauthorized, kit.T(r, "auth.invalid_credentials", nil))
		return
	}
	if err = h.authn.DisableTOTP(r.Context(), userID); err == nil {
		err = h.audit(r, userID, "totp_disabled", "2FA disabled")
	}
	if err != nil {
		h.serverError(w, r, "TOTP disable error:", err)
		return
	}
	kit.OK(w, r, nil)
}

type totpStatusAnswer struct {
	Success bool `json:"success"`
	Enabled bool `json:"enabled"`
}

// totpStatus answers GET /api/auth/totp/status: whether the signed-in
// account has two-factor sign-in on.
func (h *handlers) totpStatus(w http.ResponseWriter, r *http.Request) {
	enabled, err := h.authn.IsTOTPEnabled(r.Context(), kit.UserID(r))
	if err != nil {
		h.serverError(w, r, "TOTP status error:", err)
		return
	}
	kit.JSON(w, r, http.StatusOK, totpStatusAnswer{Success: true, Enabled: enabled})
}
