// Package accountmail sends the mails about an account (an address to
// confirm, a notice that the address is being changed, an account that
// ends or is about to be disabled for inactivity), each in the language
// of the person who reads it: the account's preferred language when it
// has one, else the console's default. The mail service lays them out.
package accountmail

import (
	"context"
	"database/sql"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/brandmail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/i18n"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/mail"
)

// Translate gives the text of a key in the mail's language.
type Translate = brandmail.Translate

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

// Send sends a mail to one address. It reports whether the mail server
// took it.
func Send(ctx context.Context, d *kit.Deps, to string, m brandmail.Mail) (bool, error) {
	if d.Mail == nil {
		return false, nil
	}
	return d.Mail.SendEmail(ctx, mail.Email{To: []string{to}, Mail: m})
}
