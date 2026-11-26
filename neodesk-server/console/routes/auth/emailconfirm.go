package auth

import (
	"errors"
	"net/http"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/lifecycle"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/emailchange"
)

// The page /confirm-email, where the link of an address change leads,
// uses two routes that need no session: one says which account and
// address the link is for, the other confirms it. Unknown, used and
// expired links get the same answer.

type emailChangeAnswer struct {
	Username  string `json:"username"`
	Email     string `json:"email"`
	ExpiresAt string `json:"expires_at"`
}

// emailLinkUnusable answers 404 for a link that cannot be used.
func emailLinkUnusable(w http.ResponseWriter, r *http.Request) {
	kit.Fail(w, r, http.StatusNotFound, kit.T(r, "email_change.unusable", nil))
}

// emailChangeLookup answers POST /api/auth/email-change/lookup {token}.
func (h *handlers) emailChangeLookup(w http.ResponseWriter, r *http.Request) {
	c, err := h.life.LookupEmailChange(r.Context(), linkToken(r))
	if errors.Is(err, lifecycle.ErrUnusable) {
		emailLinkUnusable(w, r)
		return
	}
	if err != nil {
		h.serverError(w, r, "Email change error:", err)
		return
	}
	kit.OK(w, r, emailChangeAnswer{Username: c.Username, Email: c.Email, ExpiresAt: c.ExpiresAt.Format(time.RFC3339)})
}

// emailChangeConfirm answers POST /api/auth/email-change/confirm {token}:
// the account gets the new address and the link is spent. The account's
// sessions end, but the one of the request when it is the account's own,
// and so do its client access tokens and password links.
func (h *handlers) emailChangeConfirm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	before := ""
	if c, err := h.life.LookupEmailChange(ctx, linkToken(r)); err == nil {
		_ = h.d.Store.QueryRow(ctx, `SELECT COALESCE(email, '') FROM users WHERE id = ?`, c.UserID).Scan(&before)
	}
	c, err := h.life.ConfirmEmailChange(ctx, linkToken(r))
	if errors.Is(err, lifecycle.ErrUnusable) {
		emailLinkUnusable(w, r)
		return
	}
	if err == nil {
		err = emailchange.EndOthers(r, h.d, h.links, c.UserID, true)
	}
	if err == nil {
		if before == "" {
			before = "none"
		}
		err = h.d.Audit.WriteAbout(ctx, c.UserID, c.UserID, "email_changed",
			"Confirmed the new email address of user "+c.Username+": "+c.Email+" (was "+before+")", kit.ClientIP(r))
	}
	if err != nil {
		h.serverError(w, r, "Email change error:", err)
		return
	}
	kit.OK(w, r, emailChangeAnswer{Username: c.Username, Email: c.Email, ExpiresAt: c.ExpiresAt.Format(time.RFC3339)})
}
