package users

import (
	"net/http"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/passwordlinks"
)

// sendResetLink answers POST /api/users/{id}/password-reset-link: a link
// to set a new password, mailed to the account's address. It works once,
// for 24 hours; the link sent before stops working. The password stays as
// it is until the link is used.
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
	email := ""
	if v := user.Email.Value(); v.IsString() {
		email = strings.TrimSpace(v.Str())
	}
	switch {
	case !isLocalAccount(user):
		failKey(w, r, http.StatusBadRequest, "users.link_local_only")
		return
	case email == "":
		failKey(w, r, http.StatusBadRequest, "users.reset_email_required")
		return
	case !h.mailReady(ctx):
		failKey(w, r, http.StatusConflict, "users.invite_needs_email")
		return
	}
	delivered, err := h.sendLink(r, user.ID.V, user.username(), email, passwordlinks.PurposeReset)
	if err != nil {
		h.serverError(w, r, "Reset link error:", err)
		return
	}
	if !delivered {
		failKey(w, r, http.StatusBadGateway, "users.link_mail_failed")
		return
	}
	if err := h.d.LogAction(r, "password_reset_link_sent", "Sent a password reset link to user: "+user.username()); err != nil {
		h.serverError(w, r, "Reset link error:", err)
		return
	}
	answer(w, r, http.StatusOK, done{Success: true})
}
