package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/authn"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/securitymail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
)

// A password change at sign-in: an admin required it for the account, or
// gave it a temporary password. After the password step (and after the
// second factor, when the account has one) the account is not signed in
// yet: the session holds it as waiting for a new password, as it holds an
// account waiting for the set-up of a mandatory second factor
// (required.go), and only POST /api/auth/password/required acts on it.
// The new password ends the account's other sessions and its access
// tokens; then the sign-in goes on with the set-up of two-factor sign-in
// when that is due, else it is finished.

// The fields of a session that waits for the new password.
const (
	pendingChangeUserID = "pendingPasswordChangeUserId"
	pendingChangeUser   = "pendingPasswordChangeUser"
	pendingChangeSince  = "pendingPasswordChangeAt"
)

// changeWindow is how long the new password may take after the password
// step.
const changeWindow = 15 * time.Minute

type changeDue struct {
	Success                bool `json:"success"`
	PasswordChangeRequired bool `json:"passwordChangeRequired"`
}

// beginPasswordChange puts the account into a new session that waits for
// its new password, and answers the sign-in step with
// passwordChangeRequired.
func (h *handlers) beginPasswordChange(w http.ResponseWriter, r *http.Request, who account, meta session.Meta) {
	sess := kit.Session(r)
	if err := sess.Regenerate(); err != nil {
		h.sessionFailure(w, r, "Session regeneration error (password change):", err)
		return
	}
	sess.Set(pendingChangeUserID, who.ID)
	sess.Set(pendingChangeUser, sessionUser{ID: who.ID, Username: who.Username, Role: who.Role, PreferredLanguage: who.PreferredLanguage})
	sess.Set(pendingMeta, meta)
	sess.Set(pendingChangeSince, h.now().UnixMilli())
	kit.JSON(w, r, http.StatusOK, changeDue{Success: true, PasswordChangeRequired: true})
}

// pendingChange is the account a session waits for the new password of,
// or false when it waits for none or the wait is over (which ends it).
func (h *handlers) pendingChange(sess *session.Session) (account, bool) {
	var rawID, rawUser json.RawMessage
	sess.Get(pendingChangeUserID, &rawID)
	sess.Get(pendingChangeUser, &rawUser)
	if !jsonTruthy(rawID) || !jsonTruthy(rawUser) {
		return account{}, false
	}
	var since int64
	if !sess.Get(pendingChangeSince, &since) || h.now().UnixMilli()-since > changeWindow.Milliseconds() {
		endPendingChange(sess)
		return account{}, false
	}
	who := pendingAccount(rawUser)
	who.id = jsonInt(rawID)
	return who, true
}

// endPendingChange drops the waiting password change from the session.
func endPendingChange(sess *session.Session) {
	for _, key := range []string{pendingChangeUserID, pendingChangeUser, pendingMeta, pendingChangeSince} {
		sess.Delete(key)
	}
}

// passwordChangeDue reports whether the account must choose a new
// password before its sign-in is finished: a local account an admin
// required it of.
func (h *handlers) passwordChangeDue(r *http.Request, userID int64) (bool, error) {
	user, err := h.authn.UserByID(r.Context(), userID)
	if err != nil || user == nil || authn.IsExternalAccount(user) {
		return false, err
	}
	policy, err := h.authn.AccountPolicy(r.Context(), userID)
	return policy.MustChangePassword, err
}

// requiredPassword answers POST /api/auth/password/required with the body
// {newPassword, confirmPassword, return?}: the account the session waits
// for gets the new password, which must keep the password rule and differ
// from the one it signed in with. Then the sign-in goes on: with the
// set-up of two-factor sign-in when it is required and the account has
// none (the answer is that of the password step, totpSetupRequired),
// else it is finished. An account that was disabled or deleted meanwhile
// is refused.
func (h *handlers) requiredPassword(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sess := kit.Session(r)
	who, ok := h.pendingChange(sess)
	if !ok {
		kit.Fail(w, r, http.StatusBadRequest, kit.T(r, "auth.totp_session_expired", nil))
		return
	}
	current, err := h.authn.UserByID(ctx, who.id)
	if err != nil {
		h.serverError(w, r, "Password change error:", err)
		return
	}
	switch {
	case current == nil:
		endPendingChange(sess)
		kit.Fail(w, r, http.StatusBadRequest, kit.T(r, "auth.totp_session_expired", nil))
		return
	case current.Disabled.True():
		endPendingChange(sess)
		refuseDisabled(w, r)
		return
	}
	body := kit.Body(r)
	next, confirm := body.Get("newPassword"), body.Get("confirmPassword")
	text, isText := next.Str()
	if !isText || text == "" {
		kit.Fail(w, r, http.StatusBadRequest, kit.T(r, "auth.password_required", nil))
		return
	}
	if !strictEqual(next, confirm) {
		kit.Fail(w, r, http.StatusBadRequest, kit.T(r, "auth.passwords_mismatch", nil))
		return
	}
	err = h.authn.SetNewPassword(ctx, who.id, text)
	var refusal authn.Refusal
	if errors.As(err, &refusal) {
		refuse(w, r, refusal)
		return
	}
	if err == nil {
		err = h.authn.SetMustChangePassword(ctx, who.id, false)
	}
	if err == nil {
		// A link sent before would replace the new password
		err = h.links.Revoke(ctx, who.id)
	}
	if err == nil {
		// The account's other sessions signed in with the old password
		if _, err = h.sessions.EndAccount(ctx, who.id, ""); err == nil {
			err = h.authn.RevokeClientTokens(ctx, who.id, "", "")
		}
	}
	if err == nil {
		err = h.d.Audit.WriteAbout(ctx, who.id, who.id, "password_changed", "Password changed at sign-in, as required", kit.ClientIP(r))
	}
	if err != nil {
		h.serverError(w, r, "Password change error:", err)
		return
	}
	h.notices.PasswordChanged(ctx, who.id, securitymail.PasswordBySelf)

	var meta session.Meta
	var rawMeta json.RawMessage
	if sess.Get(pendingMeta, &rawMeta); !jsonTruthy(rawMeta) || json.Unmarshal(rawMeta, &meta) != nil {
		meta = h.clientMeta(r, body)
	}
	endPendingChange(sess)
	setupDue, err := h.authn.TwoFactorDue(ctx, who.id, !authn.IsExternalAccount(current))
	if err != nil {
		h.serverError(w, r, "Password change error:", err)
		return
	}
	if setupDue && !current.TOTPEnabled.True() {
		h.holdForSetup(w, r, who.ID, sessionUser{ID: who.ID, Username: who.Username, Role: who.Role, PreferredLanguage: who.PreferredLanguage}, meta)
		return
	}
	h.finishSignIn(w, r, who, meta, "Password changed at sign-in")
}

// finishSignIn signs the account in on a new session once every step of
// its sign-in is done: the attempt counts as a success, the sign-in is
// noted on the account, the audit entries are written and the answer is
// that of a finished sign-in.
func (h *handlers) finishSignIn(w http.ResponseWriter, r *http.Request, who account, meta session.Meta, how string) {
	body := kit.Body(r)
	est, err := h.establish(r, who, meta)
	replaced := len(est.Ended)
	if err != nil {
		h.sessionFailure(w, r, "Session establish error:", err)
		return
	}
	h.recordAttempt(r, who.username, true)
	h.signedInNotes(w, r, who.id, meta)
	err = h.authn.TouchLastLogin(r.Context(), who.id)
	if err == nil {
		details := "User logged in (" + how + "); ip=" + meta.IP + "; ua=" + cutUTF16(meta.UserAgent, 160) + replacedNote(replaced)
		err = h.audit(r, who.id, "login", details)
	}
	if err == nil {
		err = h.auditEnded(r, who.id, est, meta, "New login")
	}
	if err != nil {
		h.sessionFailure(w, r, "Session bookkeeping error:", err)
		return
	}
	kit.JSON(w, r, http.StatusOK, h.signedInAnswer(who, replaced, body))
}
