// Package securitymail tells a person by mail about changes to how their
// account signs in: a sign-in from a browser the account has not used
// before, a new password, two-factor sign-in turned on, off or reset,
// recovery codes used up, renewed or made unusable, and a new email
// address (that one goes to the old address).
//
// Each notice is one function. It returns at once and sends in the
// background, so a route answers before the mail server does. A notice
// goes out only when the console can send mail, the account has an
// address, and the notice's switch in the server's configuration
// (server_config) is not "false": every notice is on unless an admin
// turned it off under Settings, Sign-in, Security notices.
//
// The mails use the console's branded layout (brandmail) with a plain
// text part, in the recipient's language: their preferred language, else
// the console's default. Each says what to do when it was not them.
package securitymail

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/brandmail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/i18n"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/mail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/mail/message"
	"github.com/neodeskapp/neodesk/neodesk-server/console/people"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// The switches in server_config, "true" or "false"; a missing one is on.
const (
	KeyNewSignIn        = "security_notice_new_signin"
	KeyPasswordChanged  = "security_notice_password_changed"
	KeyTwoFactorChanged = "security_notice_two_factor_changed"
	KeyEmailChanged     = "security_notice_email_changed"
)

// Keys are the switches, in the order the settings page shows them.
var Keys = []string{KeyNewSignIn, KeyPasswordChanged, KeyTwoFactorChanged, KeyEmailChanged}

// sendTimeout bounds the sending of one notice.
const sendTimeout = time.Minute

// Notices sends the notices of one console.
type Notices struct {
	d *kit.Deps
	// now is the clock of the times the mails name.
	now func() time.Time
}

// New returns the notices of the console of d. It reads the mail
// settings, the languages and the branding at every notice.
func New(d *kit.Deps) *Notices {
	return &Notices{d: d, now: time.Now}
}

// Enabled reports whether the notice of a switch is on: anything but
// "false" (or "0", "no", "off") in the server's configuration, and a
// database without the table or the key, is on.
func Enabled(ctx context.Context, db *store.DB, key string) bool {
	if db == nil {
		return true
	}
	var value string
	err := db.QueryRow(ctx, `SELECT value FROM server_config WHERE key = ?`, key).Scan(&value)
	if err != nil {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "false", "0", "no", "off":
		return false
	}
	return true
}

// recipient is who a notice goes to.
type recipient struct {
	username, email, language string
	// zone is the person's time zone, "" for none (UTC)
	zone string
}

// recipientOf reads the account's name, address and preferred language;
// nil for an account that is gone.
func (n *Notices) recipientOf(ctx context.Context, userID int64) (*recipient, error) {
	var name, email, language sql.NullString
	err := n.d.Store.QueryRow(ctx, `SELECT username, email, preferred_language FROM users WHERE id = ?`, userID).Scan(&name, &email, &language)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &recipient{username: name.String, email: strings.TrimSpace(email.String), language: strings.TrimSpace(language.String),
		zone: people.TimeZone(ctx, n.d.Store, userID)}, nil
}

// Language is the language a mail to the account is written in: its
// preferred language when the console has it, else the console's default.
func Language(ctx context.Context, d *kit.Deps, userID int64) string {
	var language sql.NullString
	_ = d.Store.QueryRow(ctx, `SELECT preferred_language FROM users WHERE id = ?`, userID).Scan(&language)
	return languageOr(d, strings.TrimSpace(language.String))
}

func languageOr(d *kit.Deps, preferred string) string {
	if d.I18n == nil || d.I18n.Bundle == nil {
		return preferred
	}
	if preferred != "" && d.I18n.Bundle.Has(preferred) {
		return preferred
	}
	return d.I18n.Bundle.DefaultLang
}

// Translator gives the texts of a language with their placeholders
// filled, for the mail builders (passwordlinks.Translate has its form).
func Translator(d *kit.Deps, language string) func(key string, vars map[string]any) string {
	return func(key string, vars map[string]any) string {
		if d.I18n == nil || d.I18n.Bundle == nil {
			return key
		}
		pairs := make([]any, 0, 2*len(vars))
		for k, v := range vars {
			pairs = append(pairs, k, v)
		}
		return d.I18n.Bundle.T(language, key, i18n.V(pairs...))
	}
}

// notice is what one mail says.
type notice struct {
	// key is the switch; subject, heading and lead are language keys
	// below security_notice.
	key                    string
	subject, heading, lead string
	// facts are lines below the lead, already in the mail's language;
	// when writes a time in the recipient's time zone
	facts func(t func(string, map[string]any) string, when func(time.Time) string) []string
	// notYou is the key of what to do when it was not them
	notYou string
	// to is the address when it is not the account's own
	to string
}

// Ready reports whether a notice of the switch would go out to the
// account now: the console can send mail, the switch is on and the
// account has an address.
func (n *Notices) Ready(ctx context.Context, userID int64, key string) bool {
	if n.d.Mail == nil || n.d.Mail.LoadSMTPConfig(ctx) == nil || !Enabled(ctx, n.d.Store, key) {
		return false
	}
	who, err := n.recipientOf(ctx, userID)
	return err == nil && who != nil && who.email != ""
}

// send writes and sends a notice to the account in the background.
func (n *Notices) send(ctx context.Context, userID int64, nt notice) {
	ctx = context.WithoutCancel(ctx)
	go func() {
		ctx, cancel := context.WithTimeout(ctx, sendTimeout)
		defer cancel()
		if err := n.deliver(ctx, userID, nt); err != nil && n.d.Log != nil {
			n.d.Log.Warn("[security notice] not sent:", err.Error())
		}
	}()
}

func (n *Notices) deliver(ctx context.Context, userID int64, nt notice) error {
	m := n.d.Mail
	if m == nil || m.LoadSMTPConfig(ctx) == nil || !Enabled(ctx, n.d.Store, nt.key) {
		return nil
	}
	who, err := n.recipientOf(ctx, userID)
	if err != nil || who == nil {
		return err
	}
	to := who.email
	if nt.to != "" {
		to = nt.to
	}
	if to == "" {
		return nil
	}
	built := n.build(who, nt)
	_, err = m.SendEmail(ctx, mail.Email{To: []string{to}, Subject: built.subject, Text: &built.text, HTML: &built.html, Inline: built.inline})
	return err
}

// built is a written notice.
type built struct {
	subject, text, html string
	inline              []message.Inline
}

// build writes the notice in the recipient's language.
func (n *Notices) build(who *recipient, nt notice) built {
	base := ""
	if n.d.Mail != nil {
		base = strings.TrimRight(n.d.Mail.GetPanelBaseURL(), "/")
	}
	brand := brandmail.Of(n.d.Branding, base)
	t := Translator(n.d, languageOr(n.d, who.language))
	host := base
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	vars := map[string]any{"server": brand.Server, "username": who.username, "host": host}
	subject := t("security_notice."+nt.subject, vars)
	heading := t("security_notice."+nt.heading, vars)
	lead := t("security_notice."+nt.lead, vars)
	var facts []string
	if nt.facts != nil {
		facts = nt.facts(t, func(at time.Time) string { return people.TimeIn(at, who.zone) })
	}
	notYou := t("security_notice."+nt.notYou, vars)
	footer := t("security_notice.footer", vars)
	button, link := "", ""
	if base != "" {
		button, link = t("security_notice.button", vars), base+"/account"
	}
	small := append(append([]string{}, facts...), notYou)
	lines := []string{heading, "", lead, ""}
	lines = append(lines, small...)
	if link != "" {
		lines = append(lines, "", button+": "+link)
	}
	lines = append(lines, "", "--", footer)
	page := brandmail.Page{Branding: brand, Heading: heading, Lead: lead, Small: small, Button: button, Link: link, Footer: footer}
	return built{subject: subject, text: strings.Join(lines, "\n") + "\n", html: brandmail.HTML(page), inline: brandmail.Inline(page)}
}
