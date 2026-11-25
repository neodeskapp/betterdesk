package auth

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/brandmail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/mail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/passwordlinks"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// "Forgot password?" on the sign-in page: a person names their username
// or email address and, when that is a local, enabled account with an
// address, gets a reset link there that works for an hour. The answer is
// the same whether or not an account was found, and the mail goes out
// after it, so neither the answer nor its time says which accounts exist.

// resetThrottle is how long an account gets no second link from the
// sign-in page, so nobody fills its inbox.
const resetThrottle = 2 * time.Minute

// resetSendTimeout bounds the mail of one request.
const resetSendTimeout = time.Minute

// maxResetTargets caps the accounts one address can reach.
const maxResetTargets = 5

// mailReady reports whether the console has SMTP settings to send with.
func (h *handlers) mailReady(ctx context.Context) bool {
	return h.d.Mail != nil && h.d.Mail.LoadSMTPConfig(ctx) != nil
}

// passwordResetAvailable answers GET /api/auth/password-reset/available:
// whether the sign-in page offers "Forgot password?".
func (h *handlers) passwordResetAvailable(w http.ResponseWriter, r *http.Request) {
	kit.OK(w, r, map[string]bool{"available": h.mailReady(r.Context())})
}

// resetTarget is an account a reset link goes to.
type resetTarget struct {
	id       int64
	username string
	email    string
}

// resetTargets are the local, enabled accounts with an address whose
// username is login, or whose address is login in any letter case.
func (h *handlers) resetTargets(ctx context.Context, login string) ([]resetTarget, error) {
	rows, err := h.d.Store.Query(ctx, `SELECT id, username, email, auth_provider, disabled FROM users
		WHERE username = ? OR LOWER(email) = LOWER(?) ORDER BY id`, login, login)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []resetTarget
	for rows.Next() {
		var (
			t               resetTarget
			email, provider sql.NullString
			disabled        store.Bool
		)
		if err := rows.Scan(&t.id, &t.username, &email, &provider, &disabled); err != nil {
			return nil, err
		}
		t.email = strings.TrimSpace(email.String)
		local := provider.String == "" || provider.String == "local"
		if local && !disabled.True() && t.email != "" && len(out) < maxResetTargets {
			out = append(out, t)
		}
	}
	return out, rows.Err()
}

// consoleBase is the address people reach the console at: the configured
// public address, else the address of the request.
func (h *handlers) consoleBase(r *http.Request) string {
	if base := h.d.Mail.GetPanelBaseURL(); base != "" {
		return strings.TrimRight(base, "/")
	}
	scheme := "http"
	if kit.Secure(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// passwordResetRequest answers POST /api/auth/password-reset/request
// {login} with {"success":true}, whatever login names.
func (h *handlers) passwordResetRequest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	login, _ := kit.Body(r).Get("login").Str()
	login = strings.TrimSpace(login)
	if login == "" || len(login) > 200 || !h.mailReady(ctx) {
		kit.OK(w, r, nil)
		return
	}
	targets, err := h.resetTargets(ctx, login)
	if err != nil {
		h.serverError(w, r, "Password reset request error:", err)
		return
	}
	base := h.consoleBase(r)
	host := base
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	brand := brandmail.Of(h.d.Branding, base)
	translate := func(key string, vars map[string]any) string {
		pairs := make([]any, 0, 2*len(vars))
		for k, v := range vars {
			pairs = append(pairs, k, v)
		}
		return kit.T(r, key, kit.Vars(pairs...))
	}
	var mails []mail.Email
	for _, t := range targets {
		recent, err := h.links.IssuedSince(ctx, t.id, h.now().Add(-resetThrottle))
		if err != nil {
			h.serverError(w, r, "Password reset request error:", err)
			return
		}
		if recent {
			continue
		}
		token, expires, err := h.links.IssueFor(ctx, t.id, passwordlinks.PurposeReset, "", passwordlinks.SelfResetLifetime)
		if err != nil {
			h.serverError(w, r, "Password reset request error:", err)
			return
		}
		m := passwordlinks.BuildMail(translate, passwordlinks.MailFacts{
			Purpose: passwordlinks.PurposeReset, Self: true, Link: passwordlinks.SetPasswordLink(base, token),
			Username: t.username, Host: host, Expires: expires.UTC().Format("2006-01-02 15:04") + " UTC", Branding: brand,
		})
		mails = append(mails, mail.Email{To: []string{t.email}, Subject: m.Subject, Text: &m.Text, HTML: &m.HTML, Inline: m.Inline})
		if err := h.audit(r, t.id, "password_reset_link_sent", "Asked for a password reset link on the sign-in page"); err != nil {
			h.serverError(w, r, "Password reset request error:", err)
			return
		}
	}
	if len(mails) > 0 {
		sender := h.d.Mail
		go func() {
			sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), resetSendTimeout)
			defer cancel()
			for _, m := range mails {
				_, _ = sender.SendEmail(sendCtx, m)
			}
		}()
	}
	kit.OK(w, r, nil)
}
