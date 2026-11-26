package users

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/lifecycle"
	"github.com/neodeskapp/neodesk/neodesk-server/console/passwordlinks"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/accountmail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/emailchange"
)

// Email address changes (package emailchange): an admin's in Edit (PATCH
// /api/users/{id} with "email"), the person's own on Your account, and
// cancelling or resending a change that waits for its confirmation.

// pendingEmail is an address that waits for its confirmation, as the list
// sends it.
type pendingEmail struct {
	Email     string `json:"email"`
	ExpiresAt string `json:"expires_at"`
	Expired   bool   `json:"expired"`
}

func pendingOf(c *lifecycle.Change) *pendingEmail {
	if c == nil {
		return nil
	}
	return &pendingEmail{Email: c.Email, ExpiresAt: c.ExpiresAt.UTC().Format(time.RFC3339), Expired: c.Expired}
}

// changer is the group's address changer.
func (h *handlers) changer() *emailchange.Changer {
	return &emailchange.Changer{
		D: h.d, Life: h.life, Links: h.links,
		Invited: func(ctx context.Context, t emailchange.Target) (bool, error) {
			user, err := accountByID(ctx, h.d.Store, float64(t.ID))
			if err != nil || user == nil || user.signedIn() || !isLocalAccount(user) {
				return false, err
			}
			pending, err := h.links.Invites(ctx)
			if err != nil {
				return false, err
			}
			_, invited := pending[t.ID]
			return invited, nil
		},
		SendInvite: func(r *http.Request, t emailchange.Target, email string) error {
			delivered, _, err := h.sendLink(r, t.ID, t.Username, email, passwordlinks.PurposeInvite, "")
			if err != nil {
				return err
			}
			if !delivered {
				return sayKey(r, http.StatusBadGateway, "users.link_mail_failed")
			}
			return h.d.LogAccountAction(r, t.ID, "user_invite_sent", "Sent an invite to user: "+t.Username)
		},
	}
}

// targetOf is the account as the changer needs it.
func targetOf(a *account) emailchange.Target {
	email := ""
	if v := a.Email.Value(); v.IsString() {
		email = strings.TrimSpace(v.Str())
	}
	provider, _ := providerOf(a)
	return emailchange.Target{ID: a.ID.V, Username: a.username(), Email: email, Provider: provider}
}

// changeRefusal turns the changer's refusal into the group's reply.
func changeRefusal(r *http.Request, err error) error {
	var refusal *emailchange.Refusal
	if errors.As(err, &refusal) {
		return say(refusal.Status, kit.T(r, refusal.Key, kit.Vars(varPairs(refusal.Vars)...)))
	}
	return err
}

func varPairs(vars map[string]any) []any {
	out := make([]any, 0, 2*len(vars))
	for k, v := range vars {
		out = append(out, k, v)
	}
	return out
}

// answerChange answers a request about the address: the address now and
// the one that waits for its confirmation.
func (h *handlers) answerChange(w http.ResponseWriter, r *http.Request, user *account) {
	ctx := r.Context()
	current, err := accountByID(ctx, h.d.Store, float64(user.ID.V))
	if err != nil {
		h.serverError(w, r, "Email change error:", err)
		return
	}
	if current == nil {
		current = user
	}
	pending, err := h.life.PendingEmail(ctx, user.ID.V)
	if err != nil {
		h.serverError(w, r, "Email change error:", err)
		return
	}
	t := targetOf(current)
	answer(w, r, http.StatusOK, struct {
		Success bool `json:"success"`
		Data    any  `json:"data"`
	}{true, map[string]any{
		"email": t.Email, "auth_provider": orLocal(t.Provider), "email_pending": pendingOf(pending),
		"mail_ready": accountmail.Ready(ctx, h.d),
	}})
}

func orLocal(p string) string {
	if p == "" {
		return "local"
	}
	return p
}

// accountFor loads the account of the path for a change of its address:
// one the caller may change.
func (h *handlers) accountFor(w http.ResponseWriter, r *http.Request) *account {
	userID, ok := pathID(r)
	if !ok {
		invalidID(w, r)
		return nil
	}
	user, err := accountByID(r.Context(), h.d.Store, userID)
	if err != nil {
		h.serverError(w, r, "Email change error:", err)
		return nil
	}
	if user == nil {
		failKey(w, r, http.StatusNotFound, "users.not_found")
		return nil
	}
	if ok, err := h.mayChange(r, user); err != nil {
		h.serverError(w, r, "Email change error:", err)
		return nil
	} else if !ok {
		sent(w, r, errRoleAboveOwn)
		return nil
	}
	return user
}

// ownAccount loads the signed-in account.
func (h *handlers) ownAccount(w http.ResponseWriter, r *http.Request) *account {
	user, err := accountByID(r.Context(), h.d.Store, float64(kit.UserID(r)))
	if err != nil {
		h.serverError(w, r, "Email change error:", err)
		return nil
	}
	if user == nil {
		failKey(w, r, http.StatusNotFound, "users.not_found")
		return nil
	}
	return user
}

// changeFailed answers what a change returned.
func (h *handlers) changeFailed(w http.ResponseWriter, r *http.Request, err error) {
	if sent(w, r, changeRefusal(r, err)) {
		return
	}
	h.serverError(w, r, "Email change error:", err)
}

// cancelEmailChange answers DELETE /api/users/{id}/email-change.
func (h *handlers) cancelEmailChange(w http.ResponseWriter, r *http.Request) {
	if user := h.accountFor(w, r); user != nil {
		h.cancelChange(w, r, user)
	}
}

// resendEmailChange answers POST /api/users/{id}/email-change/resend.
func (h *handlers) resendEmailChange(w http.ResponseWriter, r *http.Request) {
	if user := h.accountFor(w, r); user != nil {
		h.resendChange(w, r, user, isSessionAccount(r, user.ID.V))
	}
}

// ownEmail answers GET /api/users/me/email: the signed-in account's
// address, the one that waits for its confirmation, and whether the
// console can send mail.
func (h *handlers) ownEmail(w http.ResponseWriter, r *http.Request) {
	if user := h.ownAccount(w, r); user != nil {
		h.answerChange(w, r, user)
	}
}

// changeOwnEmail answers POST /api/users/me/email {email}: the new address
// gets a link that confirms it (package emailchange).
func (h *handlers) changeOwnEmail(w http.ResponseWriter, r *http.Request) {
	user := h.ownAccount(w, r)
	if user == nil {
		return
	}
	normalized, err := normalizeEmail(member(kit.Body(r), "email"))
	if err != nil {
		if !sent(w, r, err) {
			h.clientError(w, r, err)
		}
		return
	}
	if _, err := h.changer().Request(r, targetOf(user), textOf(normalized), true); err != nil {
		h.changeFailed(w, r, err)
		return
	}
	h.answerChange(w, r, user)
}

// cancelOwnEmailChange answers DELETE /api/users/me/email-change.
func (h *handlers) cancelOwnEmailChange(w http.ResponseWriter, r *http.Request) {
	if user := h.ownAccount(w, r); user != nil {
		h.cancelChange(w, r, user)
	}
}

// resendOwnEmailChange answers POST /api/users/me/email-change/resend.
func (h *handlers) resendOwnEmailChange(w http.ResponseWriter, r *http.Request) {
	if user := h.ownAccount(w, r); user != nil {
		h.resendChange(w, r, user, true)
	}
}

func (h *handlers) cancelChange(w http.ResponseWriter, r *http.Request, user *account) {
	if err := h.changer().Cancel(r, targetOf(user)); err != nil {
		h.changeFailed(w, r, err)
		return
	}
	h.answerChange(w, r, user)
}

func (h *handlers) resendChange(w http.ResponseWriter, r *http.Request, user *account, self bool) {
	if _, err := h.changer().Resend(r, targetOf(user), self); err != nil {
		h.changeFailed(w, r, err)
		return
	}
	h.answerChange(w, r, user)
}
