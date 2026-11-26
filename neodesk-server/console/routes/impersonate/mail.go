package impersonate

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/authn"
	"github.com/neodeskapp/neodesk/neodesk-server/console/brandmail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/i18n"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/mail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/mail/message"
)

// The notice mail: the person viewed learns who viewed NeoDesk as them,
// when and why, unless Settings, Sign-in turns it off
// (impersonation.NoticeMailKey). It is written in the person's language
// (their preferred language, else the console's default) and goes out
// after the start was answered; a mail that can't go out is logged.

// emailOf is the account's email address, "" for none.
func (h *handlers) emailOf(r *http.Request, id int64) string {
	var email sql.NullString
	if err := h.d.Store.QueryRow(r.Context(), `SELECT email FROM users WHERE id = ?`, id).Scan(&email); err != nil {
		return ""
	}
	return strings.TrimSpace(email.String)
}

// displayName is an account's first and last name, else its username.
func (h *handlers) displayName(ctx context.Context, a *authn.Account) string {
	var first, last sql.NullString
	if err := h.d.Store.QueryRow(ctx, `SELECT first_name, last_name FROM users WHERE id = ?`, a.ID.V).Scan(&first, &last); err != nil {
		return a.Username
	}
	if name := strings.TrimSpace(strings.TrimSpace(first.String) + " " + strings.TrimSpace(last.String)); name != "" {
		return name
	}
	return a.Username
}

// consoleBase is the address people reach the console at: the configured
// public address, else the address of the request.
func (h *handlers) consoleBase(r *http.Request) string {
	if h.d.Mail != nil {
		if base := h.d.Mail.GetPanelBaseURL(); base != "" {
			return strings.TrimRight(base, "/")
		}
	}
	scheme := "http"
	if kit.Secure(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// mailLanguage is the language of a mail to the account: its preferred
// language when the console has it, else the console's default.
func mailLanguage(bundle *i18n.Bundle, preferred string) string {
	if preferred != "" && bundle.Has(preferred) {
		return preferred
	}
	return bundle.DefaultLang
}

// noticeFacts are what a notice says.
type noticeFacts struct {
	// Admin is who viewed, Username the account viewed.
	Admin, Username string
	Reason          string
	// Date is when it started, as the reader sees it.
	Date string
	// Link leads to the person's sign-in history (Your account).
	Link     string
	Branding brandmail.Branding
}

// noticeMail is a notice: a subject, a text part and the same as HTML.
type noticeMail struct {
	Subject, Text, HTML string
	Inline              []message.Inline
}

// buildNotice writes the notice in a language, in the console's branded
// layout. The keys are below impersonation in the language files.
func buildNotice(bundle *i18n.Bundle, lang string, f noticeFacts) noticeMail {
	vars := i18n.V("admin", f.Admin, "username", f.Username, "reason", f.Reason, "date", f.Date, "server", f.Branding.Server)
	t := func(key string) string { return bundle.T(lang, "impersonation."+key, vars) }
	subject, heading, lead := t("notice_subject"), t("notice_heading"), t("notice_lead")
	button, footer := t("notice_button"), t("notice_footer")
	small := []string{t("notice_read_only"), t("notice_unexpected")}
	lines := []string{heading, "", lead, ""}
	lines = append(lines, small...)
	lines = append(lines, "", button+": "+f.Link, "", "--", footer)
	page := brandmail.Page{Branding: f.Branding, Heading: heading, Lead: lead, Button: button, Link: f.Link,
		LinkHint: t("notice_link_hint"), Small: small, Footer: footer}
	return noticeMail{Subject: subject, Text: strings.Join(lines, "\n") + "\n", HTML: brandmail.HTML(page), Inline: brandmail.Inline(page)}
}

// notify sends the notice mail in the background, when it is on and the
// person has an address.
func (h *handlers) notify(r *http.Request, admin, target *authn.Account, reason string, at time.Time) {
	ctx := context.WithoutCancel(r.Context())
	if h.d.Mail == nil || !h.svc.NoticeMail(ctx) {
		return
	}
	to := h.emailOf(r, target.ID.V)
	if to == "" {
		return
	}
	base := h.consoleBase(r)
	m := buildNotice(h.d.I18n.Bundle, mailLanguage(h.d.I18n.Bundle, target.PreferredLanguage.String), noticeFacts{
		Admin: h.displayName(ctx, admin), Username: target.Username, Reason: reason,
		Date: at.UTC().Format("2006-01-02 15:04") + " UTC", Link: base + "/account",
		Branding: brandmail.Of(h.d.Branding, base),
	})
	service := h.d.Mail
	go func() {
		ctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		sent, err := service.SendEmail(ctx, mail.Email{To: []string{to}, Subject: m.Subject, Text: &m.Text, HTML: &m.HTML, Inline: m.Inline})
		if err != nil || !sent {
			h.log.Warn("The mail about viewing NeoDesk as "+target.Username+" did not go out:", err)
		}
	}()
}
