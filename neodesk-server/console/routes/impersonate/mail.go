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
	"github.com/neodeskapp/neodesk/neodesk-server/console/mail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/people"
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
	return people.NameOf(ctx, h.d.Store, a.ID.V, a.Username)
}

// consoleBase is the address people reach the console at, as configured
// (PANEL_PUBLIC_URL, PUBLIC_URL or the panel's public host), "" when none
// is. Never the request's Host header: the admin who starts would choose
// where the button in the server's mail leads.
func (h *handlers) consoleBase() string {
	if h.d.Mail != nil {
		return strings.TrimRight(h.d.Mail.GetPanelBaseURL(), "/")
	}
	return ""
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
	// At is when it started, Zone the reader's time zone.
	At   time.Time
	Zone *time.Location
	// Base is the console's address; the button leads to the person's
	// sign-in history (Your account).
	Base string
}

// buildNotice writes the notice in a language: who viewed the console as
// the reader, when and why, that nothing could be changed, and a button
// to the reader's sign-ins. The keys are below mail.impersonation.
func buildNotice(bundle *i18n.Bundle, lang string, f noticeFacts) brandmail.Mail {
	t := func(key string, vars map[string]any) string {
		pairs := make([]any, 0, 2*len(vars))
		for k, v := range vars {
			pairs = append(pairs, k, v)
		}
		return bundle.T(lang, key, i18n.V(pairs...))
	}
	host := brandmail.HostOf(f.Base)
	vars := map[string]any{"admin": f.Admin, "username": f.Username, "reason": f.Reason, "host": host}
	m := brandmail.Mail{
		Kind: "impersonation", T: t, Lang: lang, Automatic: true, Host: host,
		Subject: t("mail.impersonation.subject", vars), Preheader: t("mail.impersonation.preheader", vars),
		Heading: t("mail.impersonation.heading", vars), Lead: t("mail.impersonation.lead", vars),
		Facts: []brandmail.Fact{
			{Label: t("mail.impersonation.label_when", nil), Value: brandmail.DateTime(f.At, lang, f.Zone)},
			{Label: t("mail.impersonation.label_reason", nil), Value: f.Reason},
		},
		Details: []string{t("mail.impersonation.unexpected", vars)},
		Why:     t("mail.impersonation.why", vars),
	}
	if f.Base != "" {
		m.Button, m.Link = t("mail.impersonation.button", vars), f.Base+"/account"
	}
	return m
}

// PreviewNotice is the notice with made-up facts, for the mail previews.
func PreviewNotice(bundle *i18n.Bundle, lang, base string, at time.Time) brandmail.Mail {
	return buildNotice(bundle, lang, noticeFacts{Admin: "Jana Weber", Username: "mmueller", Reason: "Ticket 4711: printer settings", At: at, Zone: time.Local, Base: base})
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
	m := buildNotice(h.d.I18n.Bundle, mailLanguage(h.d.I18n.Bundle, target.PreferredLanguage.String), noticeFacts{
		Admin: h.displayName(ctx, admin), Username: target.Username, Reason: reason,
		At: at, Zone: brandmail.Zone(people.TimeZone(ctx, h.d.Store, target.ID.V)), Base: h.consoleBase(),
	})
	service := h.d.Mail
	go func() {
		ctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		sent, err := service.SendEmail(ctx, mail.Email{To: []string{to}, Mail: m})
		if err != nil || !sent {
			h.log.Warn("The mail about viewing NeoDesk as "+target.Username+" did not go out:", err)
		}
	}()
}
