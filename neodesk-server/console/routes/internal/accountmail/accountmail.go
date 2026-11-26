// Package accountmail sends the mails about an account (an address to
// confirm, a notice that the address is being changed, an account that
// ends or is about to be disabled for inactivity) in the console's
// branded layout, each in the language of the person who reads it: the
// account's preferred language when it has one, else the console's
// default.
package accountmail

import (
	"context"
	"database/sql"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/brandmail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/i18n"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/mail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/mail/message"
	"github.com/neodeskapp/neodesk/neodesk-server/console/passwordlinks"
)

// Translate gives the text of a key in the mail's language.
type Translate = passwordlinks.Translate

// Ready reports whether the console has SMTP settings to send with.
func Ready(ctx context.Context, d *kit.Deps) bool {
	return d.Mail != nil && d.Mail.LoadSMTPConfig(ctx) != nil
}

// DefaultLang is the console's default language.
func DefaultLang(d *kit.Deps) string {
	if d.I18n != nil && d.I18n.Bundle != nil && d.I18n.Bundle.DefaultLang != "" {
		return d.I18n.Bundle.DefaultLang
	}
	return "en"
}

// LangOf is the language of the account's mails: its preferred language
// when the console has that language, else the default.
func LangOf(ctx context.Context, d *kit.Deps, userID int64) string {
	var lang sql.NullString
	if err := d.Store.QueryRow(ctx, `SELECT preferred_language FROM users WHERE id = ?`, userID).Scan(&lang); err == nil {
		if code := strings.TrimSpace(lang.String); code != "" && d.I18n != nil && d.I18n.Bundle != nil && d.I18n.Bundle.Has(code) {
			return code
		}
	}
	return DefaultLang(d)
}

// In gives the texts of a language.
func In(d *kit.Deps, lang string) Translate {
	return func(key string, vars map[string]any) string {
		if d.I18n == nil || d.I18n.Bundle == nil {
			return key
		}
		pairs := make([]any, 0, 2*len(vars))
		for k, v := range vars {
			pairs = append(pairs, k, v)
		}
		return d.I18n.Bundle.Translate(lang, key, i18n.V(pairs...))
	}
}

// Base is the address people reach the console at: the configured public
// address, else fallback (the address of the request).
func Base(d *kit.Deps, fallback string) string {
	if d.Mail != nil {
		if base := d.Mail.GetPanelBaseURL(); base != "" {
			return strings.TrimRight(base, "/")
		}
	}
	return strings.TrimRight(fallback, "/")
}

// Host is the host part of a base address.
func Host(base string) string {
	if i := strings.Index(base, "://"); i >= 0 {
		return base[i+3:]
	}
	return base
}

// Page is what a mail says; its texts are already in the reader's
// language.
type Page struct {
	Subject  string
	Heading  string
	Lead     string
	Button   string
	Link     string
	LinkHint string
	Small    []string
	Footer   string
}

// Built is a mail ready to send.
type Built struct {
	Subject string
	Text    string
	HTML    string
	Inline  []message.Inline
}

// Build lays out a page in the console's branding, with a plain-text part
// that says the same.
func Build(b brandmail.Branding, p Page) Built {
	lines := []string{p.Heading, "", p.Lead, ""}
	if p.Button != "" && p.Link != "" {
		lines = append(lines, p.Button+": "+p.Link, "")
	}
	lines = append(lines, p.Small...)
	lines = append(lines, "", "--", p.Footer)
	page := brandmail.Page{
		Branding: b, Heading: p.Heading, Lead: p.Lead, Button: p.Button, Link: p.Link,
		LinkHint: p.LinkHint, Small: p.Small, Footer: p.Footer,
	}
	return Built{Subject: p.Subject, Text: strings.Join(lines, "\n") + "\n", HTML: brandmail.HTML(page), Inline: brandmail.Inline(page)}
}

// Send sends a built mail to one address. It reports whether the mail
// server took it.
func Send(ctx context.Context, d *kit.Deps, to string, m Built) (bool, error) {
	if d.Mail == nil {
		return false, nil
	}
	return d.Mail.SendEmail(ctx, mail.Email{To: []string{to}, Subject: m.Subject, Text: &m.Text, HTML: &m.HTML, Inline: m.Inline})
}

// Branding is the console's name, accent colour and logo for a mail.
func Branding(d *kit.Deps, base string) brandmail.Branding {
	return brandmail.Of(d.Branding, base)
}
