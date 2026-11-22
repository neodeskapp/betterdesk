package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/unitronix/betterdesk-server/console/authn"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/session"
)

// Mandatory two-factor sign-in (Settings, Sign-in, "Require two-factor
// sign-in"). A local account without a second factor that signs in with
// its password is not signed in yet: the session holds it as waiting for
// the set-up, and only the two routes below act on that. The first code of
// the new secret turns two-factor sign-in on and finishes the sign-in.

// The fields of a session that waits for the set-up.
const (
	pendingSetupUserID = "pendingTotpSetupUserId"
	pendingSetupUser   = "pendingTotpSetupUser"
	pendingSetupSince  = "pendingTotpSetupAt"
	pendingSetupWrong  = "pendingTotpSetupFailures"
)

// setupWindow is how long the set-up may take after the password step: a
// person has to find and open an authenticator app.
const setupWindow = 15 * time.Minute

// beginRequiredSetup puts the account into a new session that waits for
// the set-up of its second factor, and answers the password step with
// totpSetupRequired.
func (h *handlers) beginRequiredSetup(w http.ResponseWriter, r *http.Request, user *authn.User, meta session.Meta) {
	sess := kit.Session(r)
	if err := sess.Regenerate(); err != nil {
		h.sessionFailure(w, r, "Session regeneration error (2FA set-up):", err)
		return
	}
	sess.Set(pendingSetupUserID, user.ID)
	sess.Set(pendingSetupUser, user)
	sess.Set(pendingMeta, meta)
	sess.Set(pendingSetupSince, h.now().UnixMilli())
	kit.JSON(w, r, http.StatusOK, setupDue{Success: true, TOTPSetupRequired: true})
}

type setupDue struct {
	Success           bool `json:"success"`
	TOTPSetupRequired bool `json:"totpSetupRequired"`
}

// pendingSetup is the account a session waits for the set-up of, or false
// when it waits for none or the wait is over (which ends it).
func (h *handlers) pendingSetup(sess *session.Session) (account, bool) {
	var rawID, rawUser json.RawMessage
	sess.Get(pendingSetupUserID, &rawID)
	sess.Get(pendingSetupUser, &rawUser)
	if !jsonTruthy(rawID) || !jsonTruthy(rawUser) {
		return account{}, false
	}
	var since int64
	if !sess.Get(pendingSetupSince, &since) || h.now().UnixMilli()-since > setupWindow.Milliseconds() {
		endPendingSetup(sess)
		return account{}, false
	}
	who := pendingAccount(rawUser)
	who.id = jsonInt(rawID)
	return who, true
}

// endPendingSetup drops the waiting set-up from the session.
func endPendingSetup(sess *session.Session) {
	for _, key := range []string{pendingSetupUserID, pendingSetupUser, pendingMeta, pendingSetupSince, pendingSetupWrong} {
		sess.Delete(key)
	}
}

// requiredSetup answers POST /api/auth/totp/required/setup: a new secret
// with its QR code for the account the session waits for. It replaces the
// secret of an earlier call.
func (h *handlers) requiredSetup(w http.ResponseWriter, r *http.Request) {
	who, ok := h.pendingSetup(kit.Session(r))
	if !ok {
		kit.Fail(w, r, http.StatusBadRequest, kit.T(r, "auth.totp_session_expired", nil))
		return
	}
	setup, err := h.authn.GenerateTOTPSetup(r.Context(), who.id)
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

type requiredEnabled struct {
	signedIn
	RecoveryCodes []string `json:"recoveryCodes"`
}

// requiredEnable answers POST /api/auth/totp/required/enable with the body
// {code, return?}: the current code of the secret from requiredSetup turns
// two-factor sign-in on and finishes the sign-in. The answer is that of a
// finished sign-in with the recovery codes, which are shown this once. A
// wrong code is a failed attempt of the account at the address; the fifth
// ends the wait, so the password is asked again. An account that was
// disabled or deleted meanwhile is refused.
func (h *handlers) requiredEnable(w http.ResponseWriter, r *http.Request) {
	sess := kit.Session(r)
	who, ok := h.pendingSetup(sess)
	if !ok {
		kit.Fail(w, r, http.StatusBadRequest, kit.T(r, "auth.totp_session_expired", nil))
		return
	}
	current, err := h.authn.UserByID(r.Context(), who.id)
	if err != nil {
		h.serverError(w, r, "TOTP enable error:", err)
		return
	}
	switch {
	case current == nil:
		endPendingSetup(sess)
		kit.Fail(w, r, http.StatusBadRequest, kit.T(r, "auth.totp_session_expired", nil))
		return
	case current.Disabled.True():
		endPendingSetup(sess)
		refuseDisabled(w, r)
		return
	}
	lockout, err := h.authn.CheckBruteForce(r.Context(), who.username, kit.ClientIP(r))
	if err != nil {
		h.serverError(w, r, "TOTP enable error:", err)
		return
	}
	if lockout.Blocked {
		endPendingSetup(sess)
		h.refuseLockedOut(w, r, lockout, h.sessions.ClientMeta(r, ""))
		return
	}
	code := kit.Body(r).Get("code")
	text, _ := code.Str()
	var codes []string
	if code.Truthy() && hasLength(code, totpCodeLength) {
		codes, err = h.authn.VerifyAndEnableTOTP(r.Context(), who.id, text)
	} else {
		err = authn.ErrInvalidTOTPCode
	}
	var refusal authn.Refusal
	switch {
	case errors.Is(err, authn.ErrInvalidTOTPCode):
		h.recordAttempt(r, who.username, false)
		var wrong int
		sess.Get(pendingSetupWrong, &wrong)
		if wrong++; wrong >= maxWrongCodes {
			endPendingSetup(sess)
		} else {
			sess.Set(pendingSetupWrong, wrong)
		}
		kit.Fail(w, r, http.StatusUnauthorized, kit.T(r, "auth.totp_invalid_code", nil))
		return
	case errors.As(err, &refusal):
		refuse(w, r, refusal)
		return
	case err != nil:
		h.serverError(w, r, "TOTP enable error:", err)
		return
	}

	var meta session.Meta
	var rawMeta json.RawMessage
	if sess.Get(pendingMeta, &rawMeta); !jsonTruthy(rawMeta) || json.Unmarshal(rawMeta, &meta) != nil {
		meta = h.clientMeta(r, kit.Body(r))
	}
	replaced, err := h.establish(r, who, meta)
	if err != nil {
		h.sessionFailure(w, r, "TOTP session regeneration error:", err)
		return
	}
	h.recordAttempt(r, who.username, true)
	h.clearGuestCookie(w, r)
	err = h.authn.TouchLastLogin(r.Context(), who.id)
	if err == nil {
		err = h.audit(r, who.id, "totp_enabled", "2FA enabled (required at sign-in)")
	}
	if err == nil {
		details := "User logged in (2FA set up at sign-in); ip=" + meta.IP + "; ua=" + cutUTF16(meta.UserAgent, 160) + replacedNote(replaced)
		err = h.audit(r, who.id, "login", details)
	}
	if err == nil && replaced > 0 {
		err = h.audit(r, who.id, "session_replaced", fmt.Sprintf("2FA login replaced %d prior session(s); newIp=%s", replaced, meta.IP))
	}
	if err != nil {
		h.sessionFailure(w, r, "TOTP post-auth bookkeeping error:", err)
		return
	}
	kit.JSON(w, r, http.StatusOK, requiredEnabled{signedIn: h.signedInAnswer(who, replaced, kit.Body(r)), RecoveryCodes: codes})
}
