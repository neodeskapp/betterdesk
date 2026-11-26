package auth

import (
	"context"
	"database/sql"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/brandmail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/mail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/passwordlinks"
	"github.com/neodeskapp/neodesk/neodesk-server/console/securitymail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// "Forgot password?" on the sign-in page: a person names their username
// or email address and, when that is a local, enabled account with an
// address, gets a reset link there that works for an hour. The answer is
// the same whether or not an account was found, and everything else
// happens after it, so neither the answer nor its time says which
// accounts exist.

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
// whether the sign-in page offers "Forgot password?": with mail settings
// and a known address of the console.
func (h *handlers) passwordResetAvailable(w http.ResponseWriter, r *http.Request) {
	kit.OK(w, r, map[string]bool{"available": h.mailReady(r.Context()) && h.consoleBase() != ""})
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

// consoleBase is the address people reach the console at, as configured
// (PANEL_PUBLIC_URL, PUBLIC_URL or the panel's public host), "" when none
// is. Never the request's Host header: whoever asks for a reset would
// choose where the link in the person's mail leads, with a working token.
func (h *handlers) consoleBase() string {
	return strings.TrimRight(h.d.Mail.GetPanelBaseURL(), "/")
}

// passwordResetRequest answers POST /api/auth/password-reset/request
// {login} with {"success":true}, whatever login names. The answer goes
// out before anything is looked up, so its time is the same for every
// login; the accounts are looked up, their links made and mailed after it
// (resetWork). An address gets at most resetsPerAddress requests worked on
// in resetWindow; more get the same answer and nothing is done for them.
func (h *handlers) passwordResetRequest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	login, _ := kit.Body(r).Get("login").Str()
	login = strings.TrimSpace(login)
	base := h.consoleBase()
	if login == "" || len(login) > 200 || base == "" || !h.mailReady(ctx) || !h.resetBudget.take(kit.ClientIP(r), h.now()) {
		kit.OK(w, r, nil)
		return
	}
	job := resetJob{
		login: login, ip: kit.ClientIP(r), base: base,
	}
	kit.OK(w, r, nil)
	resetWork(func() {
		workCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), resetSendTimeout)
		defer cancel()
		if err := h.sendResets(workCtx, job); err != nil {
			h.log.Error("Password reset request error:", err)
		}
	})
}

// resetWork runs the work of a reset request after its answer. Tests
// replace it to see what the answer was given before.
var resetWork = func(f func()) { go f() }

// resetJob is what the work of a reset request needs of the request.
type resetJob struct {
	login, ip, base string
}

// sendResets makes and mails the links of a reset request: for each
// account the login names that got no link in the last resetThrottle, a
// link for an hour, mailed in the person's language.
func (h *handlers) sendResets(ctx context.Context, job resetJob) error {
	targets, err := h.resetTargets(ctx, job.login)
	if err != nil {
		return err
	}
	host := job.base
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	brand := brandmail.Of(h.d.Branding, job.base)
	for _, t := range targets {
		recent, err := h.links.IssuedSince(ctx, t.id, h.now().Add(-resetThrottle))
		if err != nil {
			return err
		}
		if recent {
			continue
		}
		token, expires, err := h.links.IssueFor(ctx, t.id, passwordlinks.PurposeReset, "", passwordlinks.SelfResetLifetime)
		if err != nil {
			return err
		}
		translate := securitymail.Translator(h.d, securitymail.Language(ctx, h.d, t.id))
		m := passwordlinks.BuildMail(translate, passwordlinks.MailFacts{
			Purpose: passwordlinks.PurposeReset, Self: true, Link: passwordlinks.SetPasswordLink(job.base, token),
			Username: t.username, Host: host, Expires: expires.UTC().Format("2006-01-02 15:04") + " UTC", Branding: brand,
		})
		if err := h.d.Audit.WriteAbout(ctx, t.id, t.id, "password_reset_link_sent", "Asked for a password reset link on the sign-in page", job.ip); err != nil {
			return err
		}
		if _, err := h.d.Mail.SendEmail(ctx, mail.Email{To: []string{t.email}, Subject: m.Subject, Text: &m.Text, HTML: &m.HTML, Inline: m.Inline}); err != nil {
			return err
		}
	}
	return nil
}

// How many reset requests of one address are worked on: resetsPerAddress
// in resetWindow. An IPv6 address counts as its /64.
const (
	resetsPerAddress = 5
	resetWindow      = 15 * time.Minute
)

// addressBudget counts the reset requests of each address in the window.
type addressBudget struct {
	mu   sync.Mutex
	seen map[string][]time.Time
}

// take notes a request of the address and reports whether it is within
// the budget.
func (b *addressBudget) take(ip string, now time.Time) bool {
	key := ip
	if addr, err := netip.ParseAddr(ip); err == nil && addr.Is6() && !addr.Is4In6() {
		if p, err := addr.WithZone("").Prefix(64); err == nil {
			key = p.String()
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.seen == nil {
		b.seen = map[string][]time.Time{}
	}
	// The map forgets addresses whose requests are all old
	if len(b.seen) > 10000 {
		for k, times := range b.seen {
			if len(times) == 0 || now.Sub(times[len(times)-1]) > resetWindow {
				delete(b.seen, k)
			}
		}
	}
	kept := b.seen[key][:0]
	for _, t := range b.seen[key] {
		if now.Sub(t) < resetWindow {
			kept = append(kept, t)
		}
	}
	if len(kept) >= resetsPerAddress {
		b.seen[key] = kept
		return false
	}
	b.seen[key] = append(kept, now)
	return true
}
