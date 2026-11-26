package users

import (
	"cmp"
	"context"
	"database/sql"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/brandmail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/mail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/passwordlinks"
)

// Inviting by mail: an account made with POST /api/users/invite gets no
// password. Its email address gets a link where the person chooses one
// (passwordlinks); until then nobody can sign in to it.

// mailReady reports whether the console has SMTP settings to send with.
func (h *handlers) mailReady(ctx context.Context) bool {
	return h.d.Mail != nil && h.d.Mail.LoadSMTPConfig(ctx) != nil
}

// consoleBase is the address people reach the console at: the configured
// public address, else the address of the request.
func (h *handlers) consoleBase(r *http.Request) string {
	if h.d.Mail != nil {
		if base := h.d.Mail.GetPanelBaseURL(); base != "" {
			return base
		}
	}
	scheme := "http"
	if kit.Secure(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// senderName is the signed-in account's first and last name, else its
// username.
func (h *handlers) senderName(r *http.Request) string {
	fallback := ""
	if u := kit.User(r); u != nil {
		fallback = u.Username
	}
	var first, last sql.NullString
	if err := h.d.Store.QueryRow(r.Context(), `SELECT first_name, last_name FROM users WHERE id = ?`, kit.UserID(r)).Scan(&first, &last); err != nil {
		return fallback
	}
	if name := strings.TrimSpace(strings.TrimSpace(first.String) + " " + strings.TrimSpace(last.String)); name != "" {
		return name
	}
	return fallback
}

// sendLink makes a new link of the purpose for the account, which ends
// its earlier one, and mails it to the address in the request's language.
// It reports whether the mail server took the mail.
func (h *handlers) sendLink(r *http.Request, userID int64, username, to, purpose string) (bool, error) {
	ctx := r.Context()
	by := ""
	if u := kit.User(r); u != nil {
		by = u.Username
	}
	token, expires, err := h.links.Issue(ctx, userID, purpose, by)
	if err != nil {
		return false, err
	}
	base := strings.TrimRight(h.consoleBase(r), "/")
	host := base
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	m := passwordlinks.BuildMail(func(key string, vars map[string]any) string {
		pairs := make([]any, 0, 2*len(vars))
		for k, v := range vars {
			pairs = append(pairs, k, v)
		}
		return kit.T(r, key, kit.Vars(pairs...))
	}, passwordlinks.MailFacts{
		Purpose: purpose, Link: passwordlinks.SetPasswordLink(base, token), Username: username,
		Sender: h.senderName(r), Host: host, Expires: brandmail.When(expires),
		Branding: brandmail.Of(h.d.Branding, base),
	})
	sent, err := h.d.Mail.SendEmail(ctx, mail.Email{To: []string{to}, Subject: m.Subject, Text: &m.Text, HTML: &m.HTML, Inline: m.Inline, Lang: m.Lang})
	if err != nil || !sent {
		return false, err
	}
	return true, nil
}

// usernameChars are the characters a username may have.
var usernameChars = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)

// usernameFromEmail is the username of an invited account that was given
// none: the part of the address before the @, with what a username may not
// hold left out, and a number after it while the name is taken.
func (h *handlers) usernameFromEmail(r *http.Request, email string) (string, error) {
	local, _, _ := strings.Cut(email, "@")
	base := strings.Trim(usernameChars.ReplaceAllString(local, ""), ".-_")
	if len(base) > 60 {
		base = base[:60]
	}
	for len(base) < 3 {
		base += "_"
	}
	name := base
	for n := 2; ; n++ {
		taken, err := usernameTaken(r.Context(), h.d.Store, name)
		if err != nil || !taken {
			return name, err
		}
		if n > 999 {
			return "", sayKey(r, http.StatusBadRequest, "users.username_exists")
		}
		name = base + "-" + strconv.Itoa(n)
	}
}

// resendInvite answers POST /api/users/{id}/invite: a new invite link for
// an account that has not signed in yet, mailed to its address. The link
// before stops working.
func (h *handlers) resendInvite(w http.ResponseWriter, r *http.Request) {
	userID, ok := pathID(r)
	if !ok {
		invalidID(w, r)
		return
	}
	ctx := r.Context()
	user, err := accountByID(ctx, h.d.Store, userID)
	if err != nil {
		h.serverError(w, r, "Resend invite error:", err)
		return
	}
	if user == nil {
		failKey(w, r, http.StatusNotFound, "users.not_found")
		return
	}
	if ok, err := h.mayChange(r, user); err != nil {
		h.serverError(w, r, "Resend invite error:", err)
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
		failKey(w, r, http.StatusBadRequest, "users.invite_email_required")
		return
	case !user.LastLogin.IsNull():
		failKey(w, r, http.StatusConflict, "users.invite_signed_in")
		return
	case !h.mailReady(ctx):
		failKey(w, r, http.StatusConflict, "users.invite_needs_email")
		return
	}
	delivered, err := h.sendLink(r, user.ID.V, user.username(), email, passwordlinks.PurposeInvite)
	if err != nil {
		h.serverError(w, r, "Resend invite error:", err)
		return
	}
	if !delivered {
		failKey(w, r, http.StatusBadGateway, "users.link_mail_failed")
		return
	}
	if err := h.d.LogAction(r, "user_invite_sent", "Sent an invite to user: "+user.username()); err != nil {
		h.serverError(w, r, "Resend invite error:", err)
		return
	}
	answer(w, r, http.StatusOK, done{Success: true})
}

// isLocalAccount reports whether the account signs in with a password of
// this console.
func isLocalAccount(a *account) bool {
	v := a.AuthProvider.Value()
	return !v.Truthy() || (v.IsString() && v.Str() == "local")
}

// pendingInvite is an invite that was not used yet.
type pendingInvite struct {
	UserID    int64  `json:"user_id"`
	ExpiresAt string `json:"expires_at"`
	Expired   bool   `json:"expired"`
}

// invites answers GET /api/users/invites: the accounts whose invite was
// not used yet, with when it expires, and whether the console can send
// mail (mail_ready). Expired invites stay listed for 30 days.
func (h *handlers) invites(w http.ResponseWriter, r *http.Request) {
	pending, err := h.links.Invites(r.Context())
	if err != nil {
		h.serverError(w, r, "List invites error:", err)
		return
	}
	out := make([]pendingInvite, 0, len(pending))
	for id, inv := range pending {
		out = append(out, pendingInvite{UserID: id, ExpiresAt: inv.ExpiresAt.Format(time.RFC3339), Expired: inv.Expired})
	}
	slices.SortFunc(out, func(a, b pendingInvite) int { return cmp.Compare(a.UserID, b.UserID) })
	answer(w, r, http.StatusOK, struct {
		Success bool `json:"success"`
		Data    any  `json:"data"`
	}{true, map[string]any{"invites": out, "mail_ready": h.mailReady(r.Context())}})
}
