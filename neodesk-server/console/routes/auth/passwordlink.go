package auth

import (
	"errors"
	"net/http"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/passwordlinks"
	"github.com/neodeskapp/neodesk/neodesk-server/console/securitymail"
)

// The page /set-password, where a link from an invite or a password reset
// mail leads, uses two routes that need no session: one says which
// account the link is for, the other sets the password and spends the
// link. Unknown, used and expired links get the same answer.

// passwordLinkAnswer is what the page learns of a usable link.
type passwordLinkAnswer struct {
	Username  string `json:"username"`
	Purpose   string `json:"purpose"`
	ExpiresAt string `json:"expires_at"`
}

// linkUnusable answers 404 for a link that cannot be used.
func linkUnusable(w http.ResponseWriter, r *http.Request) {
	kit.Fail(w, r, http.StatusNotFound, kit.T(r, "password_link.unusable", nil))
}

// linkToken is the token of the request's body, "" for none.
func linkToken(r *http.Request) string {
	s, _ := kit.Body(r).Get("token").Str()
	return s
}

// passwordLinkLookup answers POST /api/auth/password-link/lookup {token}.
func (h *handlers) passwordLinkLookup(w http.ResponseWriter, r *http.Request) {
	link, err := h.links.Lookup(r.Context(), linkToken(r))
	if errors.Is(err, passwordlinks.ErrUnusable) {
		linkUnusable(w, r)
		return
	}
	if err != nil {
		h.serverError(w, r, "Password link error:", err)
		return
	}
	kit.OK(w, r, passwordLinkAnswer{Username: link.Username, Purpose: link.Purpose, ExpiresAt: link.ExpiresAt.Format(time.RFC3339)})
}

// passwordLinkRedeem answers POST /api/auth/password-link/redeem {token,
// password}: the account gets the password and the link is spent. Every
// session and access token of the account ends; the second factor stays
// as it is.
func (h *handlers) passwordLinkRedeem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	token := linkToken(r)
	password, isText := kit.Body(r).Get("password").Str()
	// The link first: a page with a used link learns that, not a rule
	if _, err := h.links.Lookup(ctx, token); errors.Is(err, passwordlinks.ErrUnusable) {
		linkUnusable(w, r)
		return
	} else if err != nil {
		h.serverError(w, r, "Password link error:", err)
		return
	}
	if !isText || password == "" {
		kit.Fail(w, r, http.StatusBadRequest, kit.T(r, "users.password_required", nil))
		return
	}
	switch auth.CheckNewPassword(password) {
	case auth.ErrPasswordTooShort:
		kit.Fail(w, r, http.StatusBadRequest, kit.T(r, "users.password_too_short", nil))
		return
	case auth.ErrPasswordTooLong:
		kit.Fail(w, r, http.StatusBadRequest, kit.T(r, "auth.password_max_length", nil))
		return
	}
	hash, err := h.authn.HashPassword(ctx, password)
	if err != nil {
		h.serverError(w, r, "Password link error:", err)
		return
	}
	link, err := h.links.Redeem(ctx, token, hash)
	if errors.Is(err, passwordlinks.ErrUnusable) {
		linkUnusable(w, r)
		return
	}
	if err == nil {
		h.authn.FirstPasswordReset(link.Username)
		_, err = h.sessions.EndAccount(ctx, link.UserID, "")
	}
	if err == nil {
		err = h.authn.RevokeClientTokens(ctx, link.UserID, "", "")
	}
	if err == nil {
		// A password change an admin asked for is done
		err = h.authn.SetMustChangePassword(ctx, link.UserID, false)
	}
	if err == nil {
		action, details := "password_reset_by_link", "Password set from a reset link"
		if link.Purpose == passwordlinks.PurposeInvite {
			action, details = "user_invite_accepted", "Password set from the invite"
		}
		err = h.audit(r, link.UserID, action, details)
	}
	if err != nil {
		h.serverError(w, r, "Password link error:", err)
		return
	}
	if link.Purpose == passwordlinks.PurposeReset {
		h.notices.PasswordChanged(ctx, link.UserID, securitymail.PasswordByLink)
	}
	kit.OK(w, r, map[string]string{"username": link.Username})
}
