package users

import (
	"net/http"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/passwordlinks"
	"github.com/neodeskapp/neodesk/neodesk-server/console/securitymail"
)

// sendResetLink answers POST /api/users/{id}/password-reset-link: a link
// to set a new password, mailed to the account's address in the person's
// language (their preferred one, else the console's default). It works
// once, for 24 hours; the link sent before stops working. The password
// stays as it is until the link is used. The answer says until when the
// link works (expires_at).
func (h *handlers) sendResetLink(w http.ResponseWriter, r *http.Request) {
	userID, ok := pathID(r)
	if !ok {
		invalidID(w, r)
		return
	}
	ctx := r.Context()
	user, err := accountByID(ctx, h.d.Store, userID)
	if err != nil {
		h.serverError(w, r, "Reset link error:", err)
		return
	}
	if user == nil {
		failKey(w, r, http.StatusNotFound, "users.not_found")
		return
	}
	if ok, err := h.mayChange(r, user); err != nil {
		h.serverError(w, r, "Reset link error:", err)
		return
	} else if !ok {
		sent(w, r, errRoleAboveOwn)
		return
	}
	if d := externalPassword(r, user); d != nil {
		deny(w, r, d)
		return
	}
	email := ""
	if v := user.Email.Value(); v.IsString() {
		email = strings.TrimSpace(v.Str())
	}
	switch {
	case email == "":
		failKey(w, r, http.StatusBadRequest, "users.reset_email_required")
		return
	case !h.mailReady(ctx):
		failKey(w, r, http.StatusConflict, "users.invite_needs_email")
		return
	}
	language := securitymail.Language(ctx, h.d, user.ID.V)
	delivered, expires, err := h.sendLink(r, user.ID.V, user.username(), email, passwordlinks.PurposeReset, language)
	if err != nil {
		h.serverError(w, r, "Reset link error:", err)
		return
	}
	if !delivered {
		failKey(w, r, http.StatusBadGateway, "users.link_mail_failed")
		return
	}
	if err := h.d.LogAccountAction(r, user.ID.V, "password_reset_link_sent", "Sent a password reset link to user: "+user.username()); err != nil {
		h.serverError(w, r, "Reset link error:", err)
		return
	}
	answer(w, r, http.StatusOK, struct {
		Success bool `json:"success"`
		Data    any  `json:"data"`
	}{true, map[string]string{"expires_at": expires.UTC().Format(time.RFC3339)}})
}

// cancelResetLink answers DELETE /api/users/{id}/password-reset-link: the
// reset link the account was sent stops working before it was used. An
// account without a waiting link gets 404.
func (h *handlers) cancelResetLink(w http.ResponseWriter, r *http.Request) {
	userID, ok := pathID(r)
	if !ok {
		invalidID(w, r)
		return
	}
	ctx := r.Context()
	user, err := accountByID(ctx, h.d.Store, userID)
	if err != nil {
		h.serverError(w, r, "Cancel reset link error:", err)
		return
	}
	if user == nil {
		failKey(w, r, http.StatusNotFound, "users.not_found")
		return
	}
	if ok, err := h.mayChange(r, user); err != nil {
		h.serverError(w, r, "Cancel reset link error:", err)
		return
	} else if !ok {
		sent(w, r, errRoleAboveOwn)
		return
	}
	cancelled, err := h.links.CancelReset(ctx, user.ID.V)
	if err != nil {
		h.serverError(w, r, "Cancel reset link error:", err)
		return
	}
	if !cancelled {
		failKey(w, r, http.StatusNotFound, "users.reset_none")
		return
	}
	if err := h.d.LogAccountAction(r, user.ID.V, "password_reset_cancelled", "Cancelled the password reset link of user: "+user.username()); err != nil {
		h.serverError(w, r, "Cancel reset link error:", err)
		return
	}
	answer(w, r, http.StatusOK, done{Success: true})
}
